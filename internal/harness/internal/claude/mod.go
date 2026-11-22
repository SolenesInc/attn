package claude

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

	"github.com/victorarias/attn/internal/claudemod"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/unixsock"
	"os/exec"
)

const (
	claudeModPollHold          = 25 * time.Second
	claudeModFirstPollTripwire = 30 * time.Second
	claudeModReachWindow       = 5 * time.Second
	claudeModAckWait           = 10 * time.Second
	claudeVersionLimit         = 5 * time.Second
	claudeModPollPath          = "/claude/next"
	claudeModAckPath           = "/claude/ack"
	claudeModReturnPath        = "/claude/return"
	claudeModCompactedPath     = "/claude/compacted"
	claudeModOutcomeHeld       = "held"
	claudeModOutcomeAppended   = "appended"
	claudeModOutcomeSubmitted  = "submitted"
	claudeModOutcomeStopped    = "stopped"
	claudeModOutcomeRefused    = "refused"
	claudeModCompactCompacted  = "compacted"
	claudeModCompactSkipped    = "skipped"
	claudeModCompactBusy       = "busy"
	claudeModCompactReady      = "ready"
	claudeModCompactFailed     = "failed"
	claudeVersionUnknown       = "Claude Code of unknown version"
	claudeShortSessionLength   = 8
)

var errClaudeModStopping = errors.New("the daemon is stopping; retry once it is back")

type claudeMod struct {
	host      harness.Host
	mu        sync.Mutex
	server    *http.Server
	closed    bool
	terminals map[harness.TerminalID]*claudeModTerminal
	firstPoll map[harness.TerminalID]stepclock.Timer
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
	refusedCompact    protocol.SessionID
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

func newClaudeMod(host harness.Host) *claudeMod {
	return &claudeMod{
		host:      host,
		terminals: make(map[harness.TerminalID]*claudeModTerminal),
		firstPoll: make(map[harness.TerminalID]stepclock.Timer),
		settling:  make(map[string]bool),
	}
}

func (m *claudeMod) forLaunch(terminal harness.TerminalID) claudeModSpec {
	if !m.host.AgentSockets() {
		return claudeModSpec{}
	}
	m.awaitFirstPoll(terminal)
	if err := m.serve(); err != nil {
		m.host.Logf("claude mod: this session is typed into: %v", err)
		return claudeModSpec{}
	}
	return claudeModSpec{Dir: claudemod.Dir(m.host.DataDir()), Socket: claudemod.SocketPath(m.host.DataDir())}
}

func (m *claudeMod) serve() error {
	if err := m.listen(); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	dir := claudemod.Dir(m.host.DataDir())
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
	timer = m.host.Clock().AfterFunc(claudemod.FirstPollTimer, claudeModFirstPollTripwire, func() {
		m.host.Life().Do("claudeModFirstPollMissed", func() { m.firstPollMissed(terminal, &timer) })
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
	claude := m.claudeVersionLabel()
	if session, ok := m.host.Sessions().Showing(terminal); ok {
		m.host.Events().Reach(session, harness.Reach{Reason: claudeModMissedReason(session, claude)})
	}
	if _, live := m.host.Terminals().Live(context.Background())[terminal]; live {
		m.host.Logf("claude mod: first_poll_tripwire=%s; %s in terminal %s launched with the mod but never polled, attn types into this session", claudeModFirstPollTripwire, claude, terminal)
	}
}

func claudeModMissedReason(session protocol.SessionID, claude string) string {
	return fmt.Sprintf("attn resumed Claude session %s, but the mod in %s never polled within %s, so attn did not type the mail into it; the mail is delivered if the mod polls later. If this Claude Code version broke the mod, attn needs a fix for that version", claudeShortSession(session), claude, claudeModFirstPollTripwire)
}

func claudeShortSession(id protocol.SessionID) string {
	if len(id) <= claudeShortSessionLength {
		return string(id)
	}
	return string(id[:claudeShortSessionLength])
}

func (m *claudeMod) claudeVersionLabel() string {
	binary, err := m.host.Executable(Facts(), "")
	if err != nil {
		return claudeVersionUnknown
	}
	ctx, cancel := context.WithTimeout(m.host.Life().Context(), claudeVersionLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.Env = append(os.Environ(), m.host.LoginEnv()...)
	out, err := cmd.Output()
	version, ok := strings.CutSuffix(strings.TrimSpace(string(out)), " (Claude Code)")
	if err != nil || !ok {
		return claudeVersionUnknown
	}
	return "Claude Code " + version
}

func (m *claudeMod) listen() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil {
		return nil
	}
	if m.closed {
		return errClaudeModStopping
	}
	if err := unixsock.EnsurePrivateDir(claudemod.SocketDir(m.host.DataDir())); err != nil {
		return err
	}
	socket := claudemod.SocketPath(m.host.DataDir())
	listener, err := unixsock.ListenAtomically(socket)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+claudeModPollPath, m.servePoll)
	mux.HandleFunc("POST "+claudeModAckPath, m.serveAck)
	mux.HandleFunc("POST "+claudeModReturnPath, m.serveReturn)
	mux.HandleFunc("POST "+claudeModCompactedPath, m.serveCompacted)
	server := &http.Server{Handler: mux}
	if !m.host.Life().Go("claudeModServer", func() { _ = server.Serve(listener) }) {
		_ = listener.Close()
		_ = os.Remove(socket)
		return errClaudeModStopping
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
	socket := claudemod.SocketPath(m.host.DataDir())
	_ = os.Remove(socket)
	_ = os.Remove(filepath.Dir(socket))
}

func (m *claudeMod) reachable(terminal harness.TerminalID) bool {
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
	hold := m.host.Clock().NewTimer(claudemod.PollHoldTimer, claudeModPollHold)
	defer hold.Stop()
	select {
	case <-poll.given:
	case <-poll.replaced:
		w.WriteHeader(http.StatusConflict)
		return
	case <-hold.C():
	case <-r.Context().Done():
	case <-m.host.Life().Context().Done():
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
	t := m.connectLocked(terminal, instance)
	if t == nil {
		return nil
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

func (m *claudeMod) addTerminalLocked(terminal harness.TerminalID) *claudeModTerminal {
	t := &claudeModTerminal{id: terminal, replacedInstances: make(map[string]bool), handed: make(map[string]*claudeModDelivery), held: make(map[string]*claudeModHeld), compacting: make(map[string]protocol.SessionID)}
	m.terminals[terminal] = t
	return t
}

func (m *claudeMod) connectLocked(terminal harness.TerminalID, instance string) *claudeModTerminal {
	t := m.terminals[terminal]
	if t == nil {
		t = m.addTerminalLocked(terminal)
		m.host.Life().Go("claudeModReachable", func() {
			if session, ok := m.host.Sessions().Showing(terminal); ok {
				m.host.Events().Reach(session, harness.Reach{Up: true})
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
			if session := t.refusedCompact; session != "" {
				t.refusedCompact = ""
				m.host.Life().Go("claudeModCompactRetry", func() { m.compactReady(session) })
			}
			m.host.Life().Go("claudeModReloaded", func() {
				if session, ok := m.host.Sessions().Showing(terminal); ok {
					m.host.Events().Reloaded(session)
				}
			})
		}
		t.instance = instance
	}
	return t
}

func (m *claudeMod) expectModsToReconnect() {
	if !m.host.AgentSockets() {
		return
	}
	sessions := m.host.Sessions()
	live := m.host.Terminals().Live(context.Background())
	for _, session := range sessions.List() {
		if session.Agent != claudeID || sessions.Remote(session.ID) {
			continue
		}
		if _, watched := sessions.Evidence(session.ID); !watched {
			continue
		}
		terminal := sessions.PrimaryTerminal(session.ID)
		if _, running := live[terminal]; running {
			m.expectModToReconnect(terminal)
		}
	}
}

func (m *claudeMod) expectModToReconnect(terminal harness.TerminalID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.terminals[terminal] == nil {
		m.armReachLocked(m.addTerminalLocked(terminal))
	}
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
		delivery.answer <- harness.Custody{Outcome: harness.Taken, At: time.Now()}
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
	timer = m.host.Clock().AfterFunc(claudemod.ReachTimer, claudeModReachWindow, func() { m.reachLost(t, &timer) })
	t.reach = timer
}

func (m *claudeMod) reachLost(t *claudeModTerminal, timer *stepclock.Timer) {
	if !m.dropUnreachable(t, timer) {
		return
	}
	if session, ok := m.host.Sessions().Showing(t.id); ok {
		m.host.Events().Reach(session, harness.Reach{})
	}
}

func (m *claudeMod) dropUnreachable(t *claudeModTerminal, timer *stepclock.Timer) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.terminals[t.id] != t || t.reach != *timer || t.poll != nil {
		return false
	}
	if len(t.handed) > 0 {
		m.armReachLocked(t)
		return false
	}
	delete(m.terminals, t.id)
	if t.instance == "" {
		m.host.Logf("claude mod: reach_window=%s; terminal %s did not reconnect after the daemon started, attn types into it", claudeModReachWindow, t.id)
	}
	for _, delivery := range t.queued {
		delivery.answer <- harness.Custody{Reason: "claude mod: reach_window=" + claudeModReachWindow.String() + "; the mod stopped polling before taking input " + delivery.input.ID + ", send it again"}
	}
	t.queued = nil
	m.returnHeldLocked(t, heldIDs(t))
	m.failCompactingLocked(t, "the Claude mod stopped polling before it reported the compact")
	return true
}

func heldIDs(t *claudeModTerminal) []string {
	ids := make([]string, 0, len(t.held))
	for id := range t.held {
		ids = append(ids, id)
	}
	return ids
}

type claudeModAck struct {
	Terminal harness.TerminalID        `json:"terminal"`
	Instance string                    `json:"instance"`
	ID       string                    `json:"id"`
	Outcome  string                    `json:"outcome"`
	Reason   string                    `json:"reason"`
	Ring     *protocol.InboxRingRecord `json:"ring"`
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
	if t != nil && t.instance != "" && t.instance != ack.Instance {
		return
	}
	if ack.Ring != nil && (t == nil || t.held[ack.ID] == nil) {
		m.settledLocked(ack.ID, *ack.Ring, true)
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
	taken := harness.Custody{Outcome: harness.Taken, At: time.Now()}
	switch ack.Outcome {
	case claudeModOutcomeHeld:
		if delivery != nil {
			t.accepted++
			t.held[ack.ID] = &claudeModHeld{session: delivery.session, input: delivery.input, accepted: t.accepted}
		}
		answer(harness.Custody{Outcome: harness.Held, At: taken.At})
	case claudeModOutcomeSubmitted:
		m.typedLocked(t, ack.ID)
		answer(taken)
	case claudeModOutcomeAppended:
		m.typedLocked(t, ack.ID)
	case claudeModOutcomeStopped:
		answer(harness.Custody{Outcome: harness.Stopped})
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
	session, shown := m.host.Sessions().Showing(ret.Terminal)
	m.mu.Lock()
	settled := m.takeReturnsLocked(ret, session, shown)
	m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string][]string{"settled": settled})
}

func (m *claudeMod) takeReturnsLocked(ret claudeModReturn, session protocol.SessionID, shown bool) []string {
	t := m.terminals[ret.Terminal]
	switch {
	case t == nil || t.instance == "":
		t = m.connectLocked(ret.Terminal, ret.Instance)
		m.armReachLocked(t)
	case t.instance != ret.Instance:
		return nil
	}
	settled := []string{}
	var known []string
	for _, back := range ret.Returns {
		held := t.held[back.ID] != nil
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
					m.settledLocked(back.ID, *back.Ring, false)
				}
			}
		case held:
			known = append(known, back.ID)
			settled = append(settled, back.ID)
		case back.UserWords && shown:
			m.host.Events().Returned(session, back.ID, back.Text)
			settled = append(settled, back.ID)
		case !back.UserWords:
			m.host.Logf("claude mod: dropped returned input %s; the next one replaces it", back.ID)
			settled = append(settled, back.ID)
		}
	}
	m.returnHeldLocked(t, known)
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
	for _, held := range back {
		switch {
		case held.input.Ring != nil:
			m.settledLocked(held.input.ID, *held.input.Ring, false)
		case held.input.Voice == harness.VoiceUser:
			m.host.Events().Returned(held.session, held.input.ID, held.input.Text)
		default:
			m.host.Logf("claude mod: dropped returned input %s; the next one replaces it", held.input.ID)
		}
	}
}

func (m *claudeMod) typedLocked(t *claudeModTerminal, id string) {
	held := t.held[id]
	delete(t.held, id)
	if held != nil && held.input.Ring != nil {
		m.settledLocked(id, *held.input.Ring, true)
	}
}

func (m *claudeMod) settledLocked(id string, ring protocol.InboxRingRecord, placed bool) {
	m.host.Events().Settled(id, ring, placed, func() { m.reported(id) })
}

func (m *claudeMod) reported(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, askedByMod := m.settling[id]; askedByMod {
		m.settling[id] = true
	}
}

func (m *claudeMod) compactReady(session protocol.SessionID) {
	m.host.Events().Compaction(session, harness.Compaction{Result: harness.CompactionReady})
}

type claudeModCompacted struct {
	Terminal harness.TerminalID `json:"terminal"`
	Instance string             `json:"instance"`
	ID       string             `json:"id"`
	Outcome  string             `json:"outcome"`
	Reason   string             `json:"reason"`
}

func (c claudeModCompacted) outcome() (harness.Compaction, bool) {
	switch c.Outcome {
	case claudeModCompactCompacted:
		return harness.Compaction{Result: harness.Compacted}, true
	case claudeModCompactSkipped:
		return harness.Compaction{Result: harness.CompactionFailed, Reason: "Claude skipped it: " + c.Reason}, true
	case claudeModCompactFailed:
		return harness.Compaction{Result: harness.CompactionFailed, Reason: c.Reason}, true
	}
	return harness.Compaction{Result: harness.Compacted}, false
}

func (m *claudeMod) serveCompacted(w http.ResponseWriter, r *http.Request) {
	var compacted claudeModCompacted
	if err := json.NewDecoder(r.Body).Decode(&compacted); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	outcome, ok := compacted.outcome()
	if !ok && compacted.Outcome != claudeModCompactBusy && compacted.Outcome != claudeModCompactReady {
		http.Error(w, "unknown outcome "+compacted.Outcome, http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	var session protocol.SessionID
	if t := m.terminals[compacted.Terminal]; t != nil && t.instance == compacted.Instance {
		if compacted.Outcome == claudeModCompactReady {
			session, t.refusedCompact = t.refusedCompact, ""
		} else {
			session = t.compacting[compacted.ID]
			delete(t.compacting, compacted.ID)
		}
		if session != "" && compacted.Outcome == claudeModCompactBusy {
			t.refusedCompact = session
		}
	}
	m.mu.Unlock()
	switch {
	case session == "":
	case compacted.Outcome == claudeModCompactBusy:
		m.host.Events().Compaction(session, harness.Compaction{Input: compacted.ID, Result: harness.CompactionBusy, Reason: compacted.Reason})
	case compacted.Outcome == claudeModCompactReady:
		m.compactReady(session)
	default:
		outcome.Input = compacted.ID
		m.host.Events().Compaction(session, outcome)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *claudeMod) failCompactingLocked(t *claudeModTerminal, why string) {
	for id, session := range t.compacting {
		delete(t.compacting, id)
		m.host.Life().Go("claudeModCompactLost", func() {
			m.host.Events().Compaction(session, harness.Compaction{Input: id, Result: harness.CompactionFailed, Reason: why})
		})
	}
}
