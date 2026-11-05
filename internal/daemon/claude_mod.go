package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/claudemod"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/stepclock"
)

const (
	ClaudeModPollHoldTimer     stepclock.Name = "claude-mod-poll-hold"
	ClaudeModReachTimer        stepclock.Name = "claude-mod-reach"
	ClaudeModAckTimer          stepclock.Name = "claude-mod-ack"
	ClaudeModFirstPollTimer    stepclock.Name = "claude-mod-first-poll"
	ClaudeModReportRetryTimer  stepclock.Name = "claude-mod-report-retry"
	claudeModPollHold                         = 25 * time.Second
	claudeModFirstPollTripwire                = 30 * time.Second
	claudeModReachWindow                      = 5 * time.Second
	claudeModAckWait                          = 10 * time.Second
	claudeModPollPath                         = "/claude/next"
	claudeModAckPath                          = "/claude/ack"
	claudeModReturnPath                       = "/claude/return"
	claudeModCompactedPath                    = "/claude/compacted"
	claudeModOutcomeHeld                      = "held"
	claudeModOutcomeAppended                  = "appended"
	claudeModOutcomeSubmitted                 = "submitted"
	claudeModOutcomeStopped                   = "stopped"
	claudeModOutcomeRefused                   = "refused"
	claudeModCompactCompacted                 = "compacted"
	claudeModCompactSkipped                   = "skipped"
	claudeModCompactBusy                      = "busy"
	claudeModCompactFailed                    = "failed"
)

type claudeMod struct {
	d         *Daemon
	mu        sync.Mutex
	server    *http.Server
	closed    bool
	terminals map[harness.TerminalID]*claudeModTerminal
	firstPoll map[harness.TerminalID]stepclock.Timer
	returned  map[protocol.SessionID][]sessionmonitor.Delivery
	reports   []protocol.Placed
	reporting bool
	settling  map[string]bool
}

type claudeModTerminal struct {
	id                harness.TerminalID
	instance          string
	replacedInstances map[string]bool
	poll              *claudeModPoll
	reach             stepclock.Timer
	queued            []*claudeModDelivery
	handed            map[string]*claudeModDelivery
	held              map[string]*claudeModHeld
	compacting        map[string]protocol.SessionID
	accepted          uint64
}

type claudeModPoll struct {
	given    chan struct{}
	replaced chan struct{}
	delivery *claudeModDelivery
}

type claudeModDelivery struct {
	session protocol.SessionID
	input   harness.Input
	compact bool
	answer  chan harness.Custody
}

func (delivery *claudeModDelivery) wire() any {
	if delivery.compact {
		return map[string]string{"id": delivery.input.ID, "kind": "compact"}
	}
	return claudeModMessage{ID: delivery.input.ID, Kind: "input", Text: delivery.input.Text, UserWords: delivery.input.Voice == harness.VoiceUser, Ring: delivery.input.Ring}
}

type claudeModMessage struct {
	ID        string                    `json:"id"`
	Kind      string                    `json:"kind,omitempty"`
	Text      string                    `json:"text"`
	UserWords bool                      `json:"user_words,omitempty"`
	Ring      *protocol.InboxRingRecord `json:"ring,omitempty"`
}

type claudeModHeld struct {
	session  protocol.SessionID
	input    harness.Input
	accepted uint64
}

func (d *Daemon) claudeMod() *claudeMod {
	d.claudeModOnce.Do(func() {
		d.claudeModState = &claudeMod{
			d:         d,
			terminals: make(map[harness.TerminalID]*claudeModTerminal),
			firstPoll: make(map[harness.TerminalID]stepclock.Timer),
			returned:  make(map[protocol.SessionID][]sessionmonitor.Delivery),
			settling:  make(map[string]bool),
		}
	})
	return d.claudeModState
}

func (d *Daemon) claudeModEnabled() bool {
	return parseBooleanSetting(d.homeSetting(SettingClaudeModEnabled))
}

func (d *Daemon) claudeModForLaunch(terminal harness.TerminalID, agent string) agentdriver.ClaudeMod {
	if agent != string(protocol.SessionAgentClaude) || !d.claudeModEnabled() {
		return agentdriver.ClaudeMod{}
	}
	if err := d.claudeMod().serve(); err != nil {
		d.logf("claude mod: this session is typed into: %v", err)
		return agentdriver.ClaudeMod{}
	}
	d.claudeMod().awaitFirstPoll(terminal)
	return agentdriver.ClaudeMod{Dir: claudemod.Dir(d.dataRoot), Socket: claudemod.SocketPath(d.dataRoot)}
}

func (m *claudeMod) serve() error {
	if err := m.listen(); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	dir := claudemod.Dir(m.d.dataRoot)
	if err := claudemod.Install(dir); err != nil {
		return fmt.Errorf("install in %s: %w", dir, err)
	}
	return nil
}

func (m *claudeMod) awaitFirstPoll(terminal harness.TerminalID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if earlier := m.firstPoll[terminal]; earlier != nil {
		earlier.Stop()
	}
	var timer stepclock.Timer
	timer = m.d.clock.AfterFunc(ClaudeModFirstPollTimer, claudeModFirstPollTripwire, func() {
		m.d.life.Do("claudeModFirstPollMissed", func() { m.firstPollMissed(terminal, &timer) })
	})
	m.firstPoll[terminal] = timer
}

func (m *claudeMod) firstPollMissed(terminal harness.TerminalID, timer *stepclock.Timer) {
	m.mu.Lock()
	missed := m.firstPoll[terminal] == *timer
	if missed {
		delete(m.firstPoll, terminal)
	}
	m.mu.Unlock()
	if !missed {
		return
	}
	if session, ok := m.d.shownIn(terminal); ok {
		m.report(protocol.ClaudeModReachReportMessage{Cmd: protocol.CmdClaudeModReachReport, SessionID: session, Change: protocol.ClaudeModReachChangeFirstPollMissed})
	}
	if m.d.terminalLive(terminal) {
		m.d.logf("claude mod: first_poll_tripwire=%s; Claude in terminal %s launched with the mod but never polled, attn types into this session", claudeModFirstPollTripwire, terminal)
	}
}

func (m *claudeMod) listen() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil {
		return nil
	}
	if m.closed {
		return errDaemonStopping
	}
	if err := ensurePrivateSocketDir(claudemod.SocketDir(m.d.dataRoot)); err != nil {
		return err
	}
	socket := claudemod.SocketPath(m.d.dataRoot)
	listener, err := listenUnixAtomically(socket)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+claudeModPollPath, m.servePoll)
	mux.HandleFunc("POST "+claudeModAckPath, m.serveAck)
	mux.HandleFunc("POST "+claudeModReturnPath, m.serveReturn)
	mux.HandleFunc("POST "+claudeModCompactedPath, m.serveCompacted)
	server := &http.Server{Handler: mux}
	if !m.d.life.Go("claudeModServer", func() { _ = server.Serve(listener) }) {
		_ = listener.Close()
		_ = os.Remove(socket)
		return errDaemonStopping
	}
	m.server = server
	return nil
}

func (m *claudeMod) close() {
	m.mu.Lock()
	server := m.server
	m.server, m.closed = nil, true
	m.mu.Unlock()
	if server == nil {
		return
	}
	_ = server.Close()
	socket := claudemod.SocketPath(m.d.dataRoot)
	_ = os.Remove(socket)
	_ = os.Remove(filepath.Dir(socket))
}

func (m *claudeMod) reachable(terminal harness.TerminalID) bool {
	if !m.d.claudeModEnabled() {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.terminals[terminal] != nil
}

func (m *claudeMod) servePoll(w http.ResponseWriter, r *http.Request) {
	terminal, instance := harness.TerminalID(r.URL.Query().Get("terminal")), r.URL.Query().Get("instance")
	if terminal == "" || instance == "" {
		http.Error(w, "terminal and instance are required", http.StatusBadRequest)
		return
	}
	poll := &claudeModPoll{given: make(chan struct{}), replaced: make(chan struct{})}
	t := m.openPoll(terminal, instance, poll)
	if t == nil {
		w.WriteHeader(http.StatusConflict)
		return
	}
	hold := m.d.clock.NewTimer(ClaudeModPollHoldTimer, claudeModPollHold)
	defer hold.Stop()
	select {
	case <-poll.given:
	case <-poll.replaced:
		w.WriteHeader(http.StatusConflict)
		return
	case <-hold.C():
	case <-r.Context().Done():
	case <-m.d.life.Done():
	}
	delivery := m.closePoll(t, poll, r.Context().Err() != nil)
	if delivery == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(delivery.wire())
}

func (m *claudeMod) openPoll(terminal harness.TerminalID, instance string, poll *claudeModPoll) *claudeModTerminal {
	m.mu.Lock()
	defer m.mu.Unlock()
	if timer := m.firstPoll[terminal]; timer != nil {
		timer.Stop()
		delete(m.firstPoll, terminal)
	}
	t := m.terminals[terminal]
	if t == nil {
		t = &claudeModTerminal{id: terminal, replacedInstances: make(map[string]bool), handed: make(map[string]*claudeModDelivery), held: make(map[string]*claudeModHeld), compacting: make(map[string]protocol.SessionID)}
		m.terminals[terminal] = t
		m.d.life.Go("claudeModReachable", func() {
			if session, ok := m.d.shownIn(terminal); ok {
				m.reportReachChanged(session)
			}
		})
	}
	if t.replacedInstances[instance] {
		return nil
	}
	if t.instance != instance {
		if t.instance != "" {
			t.replacedInstances[t.instance] = true
			for id, delivery := range t.handed {
				delete(t.handed, id)
				delivery.answer <- harness.Custody{Reason: "claude mod: reloaded before answering for input " + id}
			}
			m.returnHeldLocked(t, heldIDs(t))
			m.failCompactingLocked(t, "the Claude mod reloaded before it reported the compact")
		}
		t.instance = instance
		for session := range m.returned {
			m.d.life.Go("claudeModPlaceReturned", func() { m.placeReturnedNow(session) })
		}
	}
	if t.poll != nil {
		close(t.poll.replaced)
	}
	t.poll = poll
	if t.reach != nil {
		t.reach.Stop()
		t.reach = nil
	}
	m.giveLocked(t)
	return t
}

func (m *claudeMod) closePoll(t *claudeModTerminal, poll *claudeModPoll, clientGone bool) *claudeModDelivery {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.poll == poll {
		t.poll = nil
		m.armReachLocked(t)
	}
	delivery := poll.delivery
	switch {
	case delivery == nil:
	case clientGone:
		delete(t.handed, delivery.input.ID)
		t.queued = append([]*claudeModDelivery{delivery}, t.queued...)
		return nil
	case delivery.compact:
		delete(t.handed, delivery.input.ID)
		t.compacting[delivery.input.ID] = delivery.session
		delivery.answer <- harness.Custody{Taken: true, At: time.Now()}
	}
	return delivery
}

func (m *claudeMod) giveLocked(t *claudeModTerminal) {
	if t.poll == nil || len(t.queued) == 0 {
		return
	}
	delivery := t.queued[0]
	t.queued = t.queued[1:]
	t.handed[delivery.input.ID] = delivery
	poll := t.poll
	poll.delivery = delivery
	t.poll = nil
	close(poll.given)
	m.armReachLocked(t)
}

func (m *claudeMod) armReachLocked(t *claudeModTerminal) {
	if t.reach != nil {
		t.reach.Stop()
	}
	var timer stepclock.Timer
	timer = m.d.clock.AfterFunc(ClaudeModReachTimer, claudeModReachWindow, func() { m.reachLost(t, &timer) })
	t.reach = timer
}

func (m *claudeMod) reachLost(t *claudeModTerminal, timer *stepclock.Timer) {
	for _, session := range m.dropUnreachable(t, timer) {
		m.reportReachChanged(session)
		m.placeReturned(session)
	}
}

func (m *claudeMod) dropUnreachable(t *claudeModTerminal, timer *stepclock.Timer) []protocol.SessionID {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.terminals[t.id] != t || t.reach != *timer || t.poll != nil {
		return nil
	}
	if len(t.handed) > 0 {
		m.armReachLocked(t)
		return nil
	}
	delete(m.terminals, t.id)
	sessions := make([]protocol.SessionID, 0, len(t.queued))
	for _, delivery := range t.queued {
		delivery.answer <- harness.Custody{Reason: "claude mod: reach_window=" + claudeModReachWindow.String() + "; the mod stopped polling before taking input " + delivery.input.ID + ", send it again"}
		sessions = append(sessions, delivery.session)
	}
	t.queued = nil
	m.returnHeldLocked(t, heldIDs(t))
	m.failCompactingLocked(t, "the Claude mod stopped polling before it reported the compact")
	return sessions
}

func heldIDs(t *claudeModTerminal) []string {
	ids := make([]string, 0, len(t.held))
	for id := range t.held {
		ids = append(ids, id)
	}
	return ids
}

type claudeModAck struct {
	Terminal      harness.TerminalID        `json:"terminal"`
	Instance      string                    `json:"instance"`
	ID            string                    `json:"id"`
	Outcome       string                    `json:"outcome"`
	Reason        string                    `json:"reason"`
	ComposerEmpty bool                      `json:"composer_empty"`
	Ring          *protocol.InboxRingRecord `json:"ring"`
}

func (m *claudeMod) serveAck(w http.ResponseWriter, r *http.Request) {
	var ack claudeModAck
	if err := json.NewDecoder(r.Body).Decode(&ack); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch ack.Outcome {
	case claudeModOutcomeHeld, claudeModOutcomeAppended, claudeModOutcomeSubmitted, claudeModOutcomeStopped, claudeModOutcomeRefused:
	default:
		http.Error(w, "unknown outcome "+ack.Outcome, http.StatusBadRequest)
		return
	}
	m.acknowledge(ack)
	w.WriteHeader(http.StatusNoContent)
}

func (m *claudeMod) acknowledge(ack claudeModAck) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.terminals[ack.Terminal]
	if t != nil && t.instance != ack.Instance {
		return
	}
	if ack.Ring != nil && (t == nil || t.held[ack.ID] == nil) {
		m.reportLocked(protocol.InboxRingSettleMessage{Cmd: protocol.CmdInboxRingSettle, DeliveryID: ack.ID, Outcome: protocol.InboxRingOutcomePlaced, Ring: *ack.Ring})
	}
	if t == nil {
		return
	}
	delivery := t.handed[ack.ID]
	delete(t.handed, ack.ID)
	answer := func(custody harness.Custody) {
		if delivery != nil {
			delivery.answer <- custody
		}
	}
	taken := harness.Custody{Taken: true, At: time.Now()}
	switch ack.Outcome {
	case claudeModOutcomeHeld:
		if delivery != nil {
			t.accepted++
			t.held[ack.ID] = &claudeModHeld{session: delivery.session, input: delivery.input, accepted: t.accepted}
		}
		answer(harness.Custody{Taken: true, At: taken.At, Held: true})
	case claudeModOutcomeSubmitted:
		m.typedLocked(t, ack.ID)
		answer(taken)
	case claudeModOutcomeAppended:
		m.typedLocked(t, ack.ID)
	case claudeModOutcomeStopped:
		answer(harness.Custody{Stopped: true, ComposerEmpty: ack.ComposerEmpty})
	case claudeModOutcomeRefused:
		answer(harness.Custody{Reason: "claude mod: Claude refused input " + ack.ID + ": " + ack.Reason})
	}
}

type claudeModReturn struct {
	Terminal harness.TerminalID `json:"terminal"`
	Instance string             `json:"instance"`
	Returns  []claudeModMessage `json:"returns"`
}

func (m *claudeMod) serveReturn(w http.ResponseWriter, r *http.Request) {
	var ret claudeModReturn
	if err := json.NewDecoder(r.Body).Decode(&ret); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	session, shown := m.d.shownIn(ret.Terminal)
	m.mu.Lock()
	settled := m.takeReturnsLocked(ret, session, shown)
	m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string][]string{"settled": settled})
}

func (m *claudeMod) takeReturnsLocked(ret claudeModReturn, session protocol.SessionID, shown bool) []string {
	t := m.terminals[ret.Terminal]
	if t != nil && t.instance != ret.Instance {
		return nil
	}
	settled := []string{}
	var known []string
	placeUserWords := false
	for _, back := range ret.Returns {
		held := t != nil && t.held[back.ID] != nil
		switch {
		case back.Ring != nil:
			done, settling := m.settling[back.ID]
			if done {
				delete(m.settling, back.ID)
				settled = append(settled, back.ID)
			} else if !settling {
				m.settling[back.ID] = false
				if held {
					known = append(known, back.ID)
				} else {
					m.reportLocked(protocol.InboxRingSettleMessage{Cmd: protocol.CmdInboxRingSettle, DeliveryID: back.ID, Outcome: protocol.InboxRingOutcomeReturned, Ring: *back.Ring})
				}
			}
		case held:
			known = append(known, back.ID)
			settled = append(settled, back.ID)
		case back.UserWords && shown:
			m.returned[session] = append(m.returned[session], sessionmonitor.ReturnedUserInput(back.ID, back.Text))
			placeUserWords = true
			settled = append(settled, back.ID)
		case !back.UserWords:
			m.d.logf("claude mod: dropped returned input %s; the next one replaces it", back.ID)
			settled = append(settled, back.ID)
		}
	}
	if t != nil {
		m.returnHeldLocked(t, known)
	}
	if placeUserWords {
		m.d.life.Go("claudeModReturn", func() { m.placeReturnedNow(session) })
	}
	return settled
}

func (m *claudeMod) returnHeldLocked(t *claudeModTerminal, ids []string) {
	var back []*claudeModHeld
	for _, id := range ids {
		if held := t.held[id]; held != nil {
			delete(t.held, id)
			back = append(back, held)
		}
	}
	slices.SortFunc(back, func(a, b *claudeModHeld) int { return cmp.Compare(a.accepted, b.accepted) })
	sessions := map[protocol.SessionID]bool{}
	for _, held := range back {
		switch {
		case held.input.Ring != nil:
			m.reportLocked(protocol.InboxRingSettleMessage{Cmd: protocol.CmdInboxRingSettle, DeliveryID: held.input.ID, Outcome: protocol.InboxRingOutcomeReturned, Ring: *held.input.Ring})
		case held.input.Voice == harness.VoiceUser:
			m.returned[held.session] = append(m.returned[held.session], sessionmonitor.ReturnedUserInput(held.input.ID, held.input.Text))
			sessions[held.session] = true
		default:
			m.d.logf("claude mod: dropped returned input %s; the next one replaces it", held.input.ID)
		}
	}
	if len(sessions) > 0 {
		m.d.life.Go("claudeModReturn", func() {
			for session := range sessions {
				m.placeReturnedNow(session)
			}
		})
	}
}

func (m *claudeMod) typedLocked(t *claudeModTerminal, id string) {
	held := t.held[id]
	delete(t.held, id)
	if held != nil && held.input.Ring != nil {
		m.reportLocked(protocol.InboxRingSettleMessage{Cmd: protocol.CmdInboxRingSettle, DeliveryID: id, Outcome: protocol.InboxRingOutcomePlaced, Ring: *held.input.Ring})
	}
}

func (m *claudeMod) reportReachChanged(session protocol.SessionID) {
	m.report(protocol.ClaudeModReachReportMessage{Cmd: protocol.CmdClaudeModReachReport, SessionID: session, Change: protocol.ClaudeModReachChangeChanged})
}

func (m *claudeMod) report(msg protocol.Placed) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reportLocked(msg)
}

func (m *claudeMod) reportLocked(msg protocol.Placed) {
	m.reports = append(m.reports, msg)
	if !m.reporting {
		m.reporting = m.d.life.Go("claudeModReports", m.sendReports)
	}
}

func (m *claudeMod) sendReports() {
	unreachable := false
	for {
		m.mu.Lock()
		if len(m.reports) == 0 {
			m.reporting = false
			m.mu.Unlock()
			return
		}
		next := m.reports[0]
		m.mu.Unlock()
		_, err := Call[struct{}](m.d.life.Context(), m.d.router(), next)
		if err != nil && !m.d.homeAnswered(err) {
			if !unreachable {
				m.d.logf("claude mod: %s waits for the home, trying every %s: %v", next.CommandName(), bridge.ReconnectInterval, err)
			}
			unreachable = true
			retry := m.d.clock.NewTimer(ClaudeModReportRetryTimer, bridge.ReconnectInterval)
			select {
			case <-retry.C():
			case <-m.d.life.Done():
				retry.Stop()
				return
			}
			continue
		}
		if err != nil {
			m.d.logf("claude mod: the home refused %s; dropped it: %v", next.CommandName(), err)
		}
		unreachable = false
		m.mu.Lock()
		m.reports = m.reports[1:]
		if settle, ok := next.(protocol.InboxRingSettleMessage); ok {
			if _, askedByMod := m.settling[settle.DeliveryID]; askedByMod {
				m.settling[settle.DeliveryID] = true
			}
		}
		m.mu.Unlock()
	}
}

func (d *Daemon) homeAnswered(err error) bool {
	return d.enrollmentStatus().IsHome() || bridgeAnswered(err)
}

func bridgeAnswered(err error) bool {
	var rejected *bridge.CallRejected
	if !errors.As(err, &rejected) {
		return false
	}
	return !strings.Contains(rejected.Reason, errOutpostBridgeReplaced.Error()) && !strings.Contains(rejected.Reason, "daemon is stopping")
}

func (d *Daemon) claudeModReaches(session protocol.SessionID) bool {
	result, err := Call[protocol.ClaudeModReachResult](d.life.Context(), d.router(), protocol.ClaudeModReachMessage{Cmd: protocol.CmdClaudeModReach, SessionID: session})
	return err == nil && result.Reachable
}

func (d *Daemon) readClaudeModReach(_ context.Context, msg protocol.ClaudeModReachMessage) (protocol.ClaudeModReachResult, error) {
	return protocol.ClaudeModReachResult{Reachable: d.claudeMod().reachable(d.primaryTerminal(msg.SessionID))}, nil
}

func (d *Daemon) receiveClaudeModReachReport(_ context.Context, msg protocol.ClaudeModReachReportMessage) (struct{}, error) {
	if msg.Change == protocol.ClaudeModReachChangeFirstPollMissed {
		d.tenderWakeModMissed(msg.SessionID)
	} else {
		d.kickSessionInboxAddresses(msg.SessionID)
	}
	return struct{}{}, nil
}

type claudeModCompacted struct {
	Terminal harness.TerminalID `json:"terminal"`
	Instance string             `json:"instance"`
	ID       string             `json:"id"`
	Outcome  string             `json:"outcome"`
	Reason   string             `json:"reason"`
}

func (c claudeModCompacted) outcome() (compactOutcome, bool) {
	switch c.Outcome {
	case claudeModCompactCompacted:
		return compactOutcome{}, true
	case claudeModCompactSkipped:
		return compactOutcome{failed: "Claude skipped it: " + c.Reason}, true
	case claudeModCompactFailed:
		return compactOutcome{failed: c.Reason}, true
	}
	return compactOutcome{}, false
}

func (m *claudeMod) serveCompacted(w http.ResponseWriter, r *http.Request) {
	var compacted claudeModCompacted
	if err := json.NewDecoder(r.Body).Decode(&compacted); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	outcome, ok := compacted.outcome()
	if !ok && compacted.Outcome != claudeModCompactBusy {
		http.Error(w, "unknown outcome "+compacted.Outcome, http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	var session protocol.SessionID
	if t := m.terminals[compacted.Terminal]; t != nil && t.instance == compacted.Instance {
		session = t.compacting[compacted.ID]
		delete(t.compacting, compacted.ID)
	}
	m.mu.Unlock()
	switch {
	case session == "":
	case compacted.Outcome == claudeModCompactBusy:
		m.d.compactions.retryAfterBusy(m.d, session, compacted.ID, compacted.Reason)
	default:
		m.d.compactions.report(m.d, session, compacted.ID, outcome)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *claudeMod) failCompactingLocked(t *claudeModTerminal, why string) {
	for id, session := range t.compacting {
		delete(t.compacting, id)
		m.d.life.Go("claudeModCompactLost", func() { m.d.compactions.report(m.d, session, id, compactOutcome{failed: why}) })
	}
}
