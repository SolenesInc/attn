package daemon

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/claudemod"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/stepclock"
)

const (
	ClaudeModPollHoldTimer    stepclock.Name = "claude-mod-poll-hold"
	ClaudeModReachTimer       stepclock.Name = "claude-mod-reach"
	ClaudeModAckTimer         stepclock.Name = "claude-mod-ack"
	claudeModPollHold                        = 25 * time.Second
	claudeModReachWindow                     = 5 * time.Second
	claudeModAckWait                         = 10 * time.Second
	claudeModPollPath                        = "/claude/next"
	claudeModAckPath                         = "/claude/ack"
	claudeModReturnPath                      = "/claude/return"
	claudeModOutcomeHeld                     = "held"
	claudeModOutcomeAppended                 = "appended"
	claudeModOutcomeSubmitted                = "submitted"
	claudeModOutcomeStopped                  = "stopped"
	claudeModOutcomeRefused                  = "refused"
)

type claudeMod struct {
	d                    *Daemon
	mu                   sync.Mutex
	server               *http.Server
	closed               bool
	terminals            map[harness.TerminalID]*claudeModTerminal
	ringsBackBeforeStamp map[string]bool
	returned             map[protocol.SessionID][]sessionmonitor.Delivery
}

type claudeModTerminal struct {
	id       harness.TerminalID
	instance string
	poll     *claudeModPoll
	reach    stepclock.Timer
	queued   []*claudeModDelivery
	handed   map[string]*claudeModDelivery
	held     map[string]*claudeModHeld
	accepted uint64
}

type claudeModPoll struct {
	given    chan struct{}
	replaced chan struct{}
	delivery *claudeModDelivery
}

type claudeModDelivery struct {
	session protocol.SessionID
	input   harness.Input
	answer  chan harness.Custody
}

type claudeModHeld struct {
	session  protocol.SessionID
	input    harness.Input
	ring     *returnableInboxRing
	accepted uint64
}

func (d *Daemon) claudeMod() *claudeMod {
	d.claudeModOnce.Do(func() {
		d.claudeModState = &claudeMod{
			d:                    d,
			terminals:            make(map[harness.TerminalID]*claudeModTerminal),
			ringsBackBeforeStamp: make(map[string]bool),
			returned:             make(map[protocol.SessionID][]sessionmonitor.Delivery),
		}
	})
	return d.claudeModState
}

func (d *Daemon) claudeModEnabled() bool {
	return d.enrollmentStatus().IsHome() && parseBooleanSetting(d.homeSetting(SettingClaudeModEnabled))
}

func (d *Daemon) claudeModForLaunch(agent string) agentdriver.ClaudeMod {
	if agent != string(protocol.SessionAgentClaude) || !d.claudeModEnabled() {
		return agentdriver.ClaudeMod{}
	}
	if err := d.claudeMod().serve(); err != nil {
		d.logf("claude mod: this session is typed into: %v", err)
		return agentdriver.ClaudeMod{}
	}
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
	_ = json.NewEncoder(w).Encode(map[string]string{"id": delivery.input.ID, "text": delivery.input.Text})
}

func (m *claudeMod) openPoll(terminal harness.TerminalID, instance string, poll *claudeModPoll) *claudeModTerminal {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.terminals[terminal]
	if t == nil {
		t = &claudeModTerminal{id: terminal, handed: make(map[string]*claudeModDelivery), held: make(map[string]*claudeModHeld)}
		m.terminals[terminal] = t
	}
	if t.instance != instance {
		if t.instance != "" {
			for id, delivery := range t.handed {
				delete(t.handed, id)
				delivery.answer <- harness.Custody{Reason: "claude mod: reloaded before answering for input " + id}
			}
			m.returnHeldLocked(t, heldIDs(t))
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
	if delivery != nil && clientGone {
		delete(t.handed, delivery.input.ID)
		t.queued = append([]*claudeModDelivery{delivery}, t.queued...)
		return nil
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
		m.d.kickSessionInboxAddresses(session)
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
	Terminal harness.TerminalID `json:"terminal"`
	Instance string             `json:"instance"`
	ID       string             `json:"id"`
	Outcome  string             `json:"outcome"`
	Reason   string             `json:"reason"`
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
	if t == nil || t.instance != ack.Instance {
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
		answer(taken)
	case claudeModOutcomeSubmitted:
		delete(t.held, ack.ID)
		answer(taken)
	case claudeModOutcomeAppended:
		delete(t.held, ack.ID)
	case claudeModOutcomeStopped:
		answer(harness.Custody{Stopped: true})
	case claudeModOutcomeRefused:
		answer(harness.Custody{Reason: "claude mod: Claude refused input " + ack.ID + ": " + ack.Reason})
	}
}

type claudeModReturn struct {
	Terminal harness.TerminalID `json:"terminal"`
	Instance string             `json:"instance"`
	IDs      []string           `json:"ids"`
}

func (m *claudeMod) serveReturn(w http.ResponseWriter, r *http.Request) {
	var ret claudeModReturn
	if err := json.NewDecoder(r.Body).Decode(&ret); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	if t := m.terminals[ret.Terminal]; t != nil && t.instance == ret.Instance {
		m.returnHeldLocked(t, ret.IDs)
	}
	m.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
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
	var rest []*claudeModHeld
	for _, held := range back {
		switch {
		case held.ring == nil && strings.HasPrefix(held.input.ID, string(sessionmonitor.InboxRing)+"/"):
			m.ringsBackBeforeStamp[held.input.ID] = true
		case held.ring == nil && held.input.Voice == harness.VoiceUser:
			m.returned[held.session] = append(m.returned[held.session], sessionmonitor.ReturnedUserInput(held.input.ID, held.input.Text))
			sessions[held.session] = true
		default:
			rest = append(rest, held)
		}
	}
	if len(sessions) > 0 || len(rest) > 0 {
		m.d.life.Go("claudeModReturn", func() { m.redeliver(sessions, rest) })
	}
}

func (m *claudeMod) redeliver(sessions map[protocol.SessionID]bool, rest []*claudeModHeld) {
	for session := range sessions {
		m.placeReturnedNow(session)
	}
	for _, held := range rest {
		if held.ring != nil {
			m.d.returnInboxRing(*held.ring)
			continue
		}
		m.d.logf("claude mod: dropped returned input %s; the next one replaces it", held.input.ID)
	}
}

func (m *claudeMod) rememberRing(id string, ring returnableInboxRing) (alreadyBack bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ringsBackBeforeStamp[id] {
		delete(m.ringsBackBeforeStamp, id)
		return true
	}
	for _, t := range m.terminals {
		if held := t.held[id]; held != nil {
			held.ring = &ring
			return false
		}
	}
	return false
}
