package fakeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/harness"
)

const (
	claudeModSocketEnv   = "ATTN_CLAUDE_MOD_SOCKET"
	fakeModPromptMarker  = "› Prompt from the attn plugin"
	fakeModMessageMarker = "The attn plugin sent a message"
	ModEventHeld         = "held"
	ModEventRefusedStop  = "stopped"
	ModEventReplaced     = "replaced"
	fakeModEventBacklog  = 64
	fakeModOutcomeSubmit = "submitted"
	fakeModOutcomeAppend = "appended"
	fakeModOutcomeAbort  = "interrupted"
	fakeModTurnSteer     = "steer"
	fakeModTurnInterrupt = "interrupt"
	fakeModKindCompact   = "compact"
	fakeModTurnRunning   = "a turn is running"
	ModNewerTurnBegan    = "a newer turn began before the compaction could be applied"
	fakeModPollRetry     = time.Second
)

const (
	methodModCompactOutcome = "mod_compact_outcome"
	methodModCompactHold    = "mod_compact_hold"
	methodModCompactArrived = "mod_compact_arrived"
	methodModCompactRelease = "mod_compact_release"
	methodModCompactReport  = "mod_compact_report"
	methodModAckHold        = "mod_ack_hold"
	methodModAckArrived     = "mod_ack_arrived"
	methodModAckRelease     = "mod_ack_release"
	methodModSubmitHold     = "mod_submit_hold"
	methodModSubmitRelease  = "mod_submit_release"
	methodModPause          = "mod_pause"
	methodModResume         = "mod_resume"
	methodModGiveBack       = "mod_give_back"
	methodModReaddressHeld  = "mod_readdress_held"
	methodModTool           = "mod_tool_boundary"
	methodModAbortLands     = "mod_abort_lands"
)

const (
	ModCompactCompacted = "compacted"
	ModCompactSkipped   = "skipped"
	ModCompactBusy      = "busy"
	modCompactReady     = "ready"
	ModCompactFailed    = "failed"
)

type ModCompactReport struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

type fakeModHold struct {
	arrived chan struct{}
	release chan struct{}
	taken   bool
}

type ModEvent struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type fakeModMessage struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind,omitempty"`
	Text      string          `json:"text"`
	UserWords bool            `json:"user_words,omitempty"`
	Turn      string          `json:"turn,omitempty"`
	Ring      json.RawMessage `json:"ring,omitempty"`
}

type fakeMod struct {
	dir      string
	socket   string
	terminal string
	client   *http.Client
	agent    *agent
	claude   *claude
	events   chan ModEvent
	reports  chan ModCompactReport

	returning sync.Mutex

	mu       sync.Mutex
	instance string
	cancel   context.CancelFunc
	working  bool
	stopped  bool
	held     []fakeModMessage
	steers   []fakeModMessage
	aborts   []fakeModMessage
	back     []fakeModMessage

	nextCompact ModCompactReport
	compactHold *fakeModHold
	ackHold     *fakeModHold
	submitHold  *fakeModHold
	refused     bool
	turnsEnded  int
	newerTurn   bool

	paused chan struct{}
}

func newFakeMod(dir string) *fakeMod {
	socket := os.Getenv(claudeModSocketEnv)
	if dir == "" || socket == "" {
		return nil
	}
	return &fakeMod{
		dir:      dir,
		socket:   socket,
		terminal: string(harness.TerminalFromEnv()),
		events:   make(chan ModEvent, fakeModEventBacklog),
		reports:  make(chan ModCompactReport, fakeModEventBacklog),
		client: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}},
	}
}

func (c *claude) startMod(a *agent) {
	if c.mod != nil {
		c.mod.agent, c.mod.claude = a, c
		c.mod.load()
	}
}

func (m *fakeMod) load() {
	if m == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.instance, m.cancel, m.refused, m.newerTurn = uuid.NewString(), cancel, false, false
	instance := m.instance
	m.mu.Unlock()
	go m.pollLoop(ctx, instance)
}

func (m *fakeMod) pollLoop(ctx context.Context, instance string) {
	query := url.Values{"terminal": {m.terminal}, "instance": {instance}}.Encode()
	for ctx.Err() == nil {
		if paused := m.pausedUntil(); paused != nil {
			select {
			case <-ctx.Done():
			case <-paused:
			}
			continue
		}
		m.sendGivenBack(instance)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://attn/claude/next?"+query, nil)
		if err != nil {
			return
		}
		response, err := m.client.Do(request)
		if err != nil {
			select {
			case <-ctx.Done():
			case <-time.After(fakeModPollRetry):
			}
			continue
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		switch {
		case err != nil:
			continue
		case response.StatusCode == http.StatusConflict:
			m.events <- ModEvent{Kind: ModEventReplaced}
			return
		case response.StatusCode == http.StatusOK:
			var message fakeModMessage
			switch {
			case json.Unmarshal(body, &message) != nil || ctx.Err() != nil:
			case message.Kind == fakeModKindCompact:
				go m.compact(instance, message.ID)
			default:
				m.place(instance, message)
			}
		}
	}
}

func (m *fakeMod) pausedUntil() chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.paused
}

func (m *fakeMod) place(instance string, message fakeModMessage) {
	m.mu.Lock()
	if hold := m.ackHold; hold != nil && !hold.taken {
		hold.taken = true
		m.mu.Unlock()
		close(hold.arrived)
		<-hold.release
		m.mu.Lock()
		if m.instance != instance {
			m.mu.Unlock()
			return
		}
	}
	switch {
	case m.stopped:
		m.mu.Unlock()
		m.post("/claude/ack", map[string]any{"terminal": m.terminal, "instance": instance, "id": message.ID, "outcome": ModEventRefusedStop})
		m.events <- ModEvent{Kind: ModEventRefusedStop, Text: message.Text}
	case m.working && message.Turn == fakeModTurnSteer:
		m.steers = append(m.steers, message)
		m.mu.Unlock()
		m.ack(instance, message.ID, ModEventHeld)
		m.events <- ModEvent{Kind: ModEventHeld, Text: message.Text}
	case m.working && message.Turn == fakeModTurnInterrupt:
		m.aborts = append(m.aborts, message)
		m.mu.Unlock()
		m.ack(instance, message.ID, ModEventHeld)
		m.events <- ModEvent{Kind: ModEventHeld, Text: message.Text}
	case m.working:
		m.held = append(m.held, message)
		m.mu.Unlock()
		m.ack(instance, message.ID, ModEventHeld)
		m.events <- ModEvent{Kind: ModEventHeld, Text: message.Text}
	default:
		m.working = true
		m.mu.Unlock()
		m.submit(instance, false, fakeModMessage{ID: message.ID, Text: message.Text, UserWords: message.UserWords})
	}
}

func (m *fakeMod) submit(instance string, interrupted bool, messages ...fakeModMessage) {
	asUser := messages[0].UserWords
	texts := make([]string, 0, len(messages))
	for _, message := range messages {
		texts = append(texts, message.Text)
	}
	if asUser {
		m.agent.term.print(fakeModPromptMarker)
	} else {
		m.agent.term.print(fakeModMessageMarker)
	}
	m.agent.placeThroughMod(strings.Join(texts, "\n\n"), asUser)
	for _, message := range messages {
		outcome := fakeModOutcomeSubmit
		if interrupted && message.Turn == fakeModTurnInterrupt {
			outcome = fakeModOutcomeAbort
		}
		m.ackHeld(instance, message, outcome)
	}
}

func (m *fakeMod) toolBoundary() []string {
	m.mu.Lock()
	steers, instance := m.steers, m.instance
	m.steers = nil
	m.mu.Unlock()
	appended := make([]string, 0, len(steers))
	for _, message := range steers {
		m.ackHeld(instance, message, fakeModOutcomeAppend)
		appended = append(appended, message.Text)
	}
	return appended
}

func (m *fakeMod) takeHeldLocked() []fakeModMessage {
	held := slices.Concat(m.aborts, m.steers, m.held)
	m.aborts, m.steers, m.held = nil, nil, nil
	return held
}

func (m *fakeMod) ackHeld(instance string, message fakeModMessage, outcome string) {
	m.post("/claude/ack", map[string]any{"terminal": m.terminal, "instance": instance, "id": message.ID, "outcome": outcome, "ring": message.Ring})
}

func (m *fakeMod) readdressHeldRings(address string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, message := range m.held {
		if message.Ring == nil {
			continue
		}
		var ring map[string]any
		if err := json.Unmarshal(message.Ring, &ring); err != nil {
			return err
		}
		ring["address"] = address
		data, err := json.Marshal(ring)
		if err != nil {
			return err
		}
		m.held[i].Ring = data
	}
	return nil
}

func (m *fakeMod) sendGivenBack(instance string) {
	m.returning.Lock()
	defer m.returning.Unlock()
	m.mu.Lock()
	back := slices.Clone(m.back)
	m.mu.Unlock()
	if len(back) == 0 {
		return
	}
	data, err := json.Marshal(map[string]any{"terminal": m.terminal, "instance": instance, "returns": back})
	if err != nil {
		return
	}
	response, err := m.client.Post("http://attn/claude/return", "application/json", bytes.NewReader(data))
	if err != nil {
		return
	}
	defer response.Body.Close()
	var answer struct {
		Settled []string `json:"settled"`
	}
	if json.NewDecoder(response.Body).Decode(&answer) != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.back = slices.DeleteFunc(m.back, func(message fakeModMessage) bool { return slices.Contains(answer.Settled, message.ID) })
}

func (m *fakeMod) post(path string, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		return
	}
	response, err := m.client.Post("http://attn"+path, "application/json", bytes.NewReader(data))
	if err == nil {
		response.Body.Close()
	}
}

func (m *fakeMod) ack(instance, id, outcome string) {
	m.post("/claude/ack", map[string]string{"terminal": m.terminal, "instance": instance, "id": id, "outcome": outcome})
}

func (m *fakeMod) turnStarted() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.working, m.stopped = true, false
}

func (m *fakeMod) turnCompleted(interrupted bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.working = false
	instance := m.instance
	held := m.takeHeldLocked()
	asUser := slices.ContainsFunc(held, func(message fakeModMessage) bool { return message.UserWords })
	if len(held) > 0 && held[0].Turn == fakeModTurnInterrupt {
		asUser = held[0].UserWords
	}
	var prompt, rest []fakeModMessage
	for _, message := range held {
		if message.UserWords == asUser {
			prompt = append(prompt, message)
		} else {
			rest = append(rest, message)
		}
	}
	m.held = rest
	refused := false
	var submitHold *fakeModHold
	if len(prompt) > 0 {
		m.working = true
		if hold := m.submitHold; hold != nil && !hold.taken {
			hold.taken, submitHold = true, hold
		}
	} else {
		m.turnsEnded++
		refused, m.refused, m.newerTurn = m.refused, false, false
	}
	m.mu.Unlock()
	if refused {
		m.reportBetweenTurns(instance)
	}
	if len(prompt) > 0 {
		go func() {
			if submitHold != nil {
				<-submitHold.release
			}
			m.submit(instance, interrupted, prompt...)
		}()
	}
}

func (m *fakeMod) handle(a *agent, method string, params json.RawMessage) (any, error) {
	switch method {
	case methodModNext:
		return <-m.events, nil
	case methodModTool:
		return m.toolBoundary(), nil
	case methodModAbortLands:
		a.turn.Lock()
		defer a.turn.Unlock()
		m.mu.Lock()
		aborting := len(m.aborts) > 0
		m.mu.Unlock()
		if !aborting {
			return nil, errors.New("the mod holds no interrupt whose abort could land")
		}
		m.turnCompleted(true)
		return struct{}{}, nil
	case methodModEsc:
		a.turn.Lock()
		defer a.turn.Unlock()
		returned := m.esc()
		halting, ok := a.conv.(halter)
		if !ok {
			return nil, fmt.Errorf("%T does not script a halt", a.conv)
		}
		return returned, halting.halt()
	case methodModReload:
		m.reload()
		return struct{}{}, nil
	case methodModStop:
		m.stopPolling()
		return struct{}{}, nil
	case methodModPause:
		m.mu.Lock()
		defer m.mu.Unlock()
		m.paused = make(chan struct{})
		return struct{}{}, nil
	case methodModResume:
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.paused == nil {
			return nil, errors.New("the mod is not paused")
		}
		close(m.paused)
		m.paused = nil
		return struct{}{}, nil
	case methodModGiveBack:
		m.mu.Lock()
		instance := m.instance
		m.mu.Unlock()
		m.sendGivenBack(instance)
		return struct{}{}, nil
	case methodModReaddressHeld:
		var address string
		if err := json.Unmarshal(params, &address); err != nil {
			return nil, err
		}
		return struct{}{}, m.readdressHeldRings(address)
	case methodModCompactOutcome:
		var next ModCompactReport
		if err := json.Unmarshal(params, &next); err != nil {
			return nil, err
		}
		m.mu.Lock()
		m.nextCompact = next
		m.mu.Unlock()
		return struct{}{}, nil
	case methodModCompactHold:
		m.mu.Lock()
		m.compactHold = &fakeModHold{arrived: make(chan struct{}), release: make(chan struct{})}
		m.mu.Unlock()
		return struct{}{}, nil
	case methodModCompactArrived, methodModCompactRelease:
		m.mu.Lock()
		hold := m.compactHold
		m.mu.Unlock()
		if hold == nil {
			return nil, fmt.Errorf("no compact is held to answer %s", method)
		}
		if method == methodModCompactArrived {
			<-hold.arrived
		} else {
			close(hold.release)
		}
		return struct{}{}, nil
	case methodModCompactReport:
		return <-m.reports, nil
	case methodModSubmitHold:
		m.mu.Lock()
		m.submitHold = &fakeModHold{release: make(chan struct{})}
		m.mu.Unlock()
		return struct{}{}, nil
	case methodModSubmitRelease:
		m.mu.Lock()
		hold := m.submitHold
		m.submitHold = nil
		m.mu.Unlock()
		if hold != nil {
			close(hold.release)
		}
		return struct{}{}, nil
	case methodModAckHold:
		m.mu.Lock()
		m.ackHold = &fakeModHold{arrived: make(chan struct{}), release: make(chan struct{})}
		m.mu.Unlock()
		return struct{}{}, nil
	case methodModAckArrived, methodModAckRelease:
		m.mu.Lock()
		hold := m.ackHold
		m.mu.Unlock()
		if hold == nil {
			return nil, fmt.Errorf("no message is held to answer %s", method)
		}
		if method == methodModAckArrived {
			<-hold.arrived
		} else {
			close(hold.release)
		}
		return struct{}{}, nil
	}
	return nil, fmt.Errorf("unknown mod method %q", method)
}

func (m *fakeMod) compact(instance, id string) {
	m.mu.Lock()
	running, turnsEndedWhenAsked := m.working || m.newerTurn, m.turnsEnded
	m.mu.Unlock()
	if running {
		m.refuseCompact(instance, id, fakeModTurnRunning, turnsEndedWhenAsked)
		return
	}
	m.agent.turn.Lock()
	m.mu.Lock()
	running = m.working || m.newerTurn
	var next ModCompactReport
	var hold *fakeModHold
	if !running {
		next, m.nextCompact = m.nextCompact, ModCompactReport{}
		if m.compactHold != nil && !m.compactHold.taken {
			hold = m.compactHold
			hold.taken = true
		}
	}
	m.mu.Unlock()
	m.agent.turn.Unlock()
	if running {
		m.refuseCompact(instance, id, fakeModTurnRunning, turnsEndedWhenAsked)
		return
	}
	outcome := next.Outcome
	if outcome == "" {
		outcome = ModCompactCompacted
	}
	if outcome != ModCompactBusy {
		if err := m.agent.control.call(context.Background(), methodCompacted, compactedParams{Conversation: m.claude.conversation}, nil); err != nil {
			m.reportCompact(instance, id, ModCompactFailed, err.Error())
			return
		}
	}
	compacting := outcome == ModCompactCompacted
	if compacting {
		if err := m.compactHooks("PreCompact"); err != nil {
			m.reportCompact(instance, id, ModCompactFailed, err.Error())
			return
		}
	}
	if hold != nil {
		close(hold.arrived)
		<-hold.release
	}
	if compacting {
		if err := errors.Join(m.compactHooks("SessionStart"), m.compactHooks("PostCompact")); err != nil {
			m.reportCompact(instance, id, ModCompactFailed, err.Error())
			return
		}
	}
	if outcome == ModCompactBusy {
		m.mu.Lock()
		m.newerTurn = true
		m.mu.Unlock()
		m.refuseCompact(instance, id, next.Reason, turnsEndedWhenAsked)
		return
	}
	m.reportCompact(instance, id, outcome, next.Reason)
}

func (m *fakeMod) refuseCompact(instance, id, reason string, turnsEndedWhenAsked int) {
	m.reportCompact(instance, id, ModCompactBusy, reason)
	m.mu.Lock()
	turnEnded := m.turnsEnded != turnsEndedWhenAsked
	m.refused = !turnEnded
	m.mu.Unlock()
	if turnEnded {
		m.reportBetweenTurns(instance)
	}
}

func (m *fakeMod) reportBetweenTurns(instance string) {
	m.post("/claude/compacted", map[string]string{"terminal": m.terminal, "instance": instance, "outcome": modCompactReady})
}

func (m *fakeMod) compactHooks(event string) error {
	m.agent.turn.Lock()
	defer m.agent.turn.Unlock()
	c := m.claude
	if event == "SessionStart" {
		return c.hooks.run(event, "compact", c.hookInput(event, map[string]any{"source": "compact"}))
	}
	return c.hooks.run(event, "manual", c.hookInput(event, map[string]any{"trigger": "manual"}))
}

func (m *fakeMod) reportCompact(instance, id, outcome, reason string) {
	m.post("/claude/compacted", map[string]string{"terminal": m.terminal, "instance": instance, "id": id, "outcome": outcome, "reason": reason})
	m.reports <- ModCompactReport{Outcome: outcome, Reason: reason}
}

func (m *fakeMod) esc() []string {
	m.mu.Lock()
	held, instance := m.takeHeldLocked(), m.instance
	m.working, m.stopped = false, true
	m.back = append(m.back, held...)
	m.mu.Unlock()
	texts := make([]string, 0, len(held))
	for _, message := range held {
		texts = append(texts, message.Text)
	}
	m.sendGivenBack(instance)
	return texts
}

func (m *fakeMod) stopPolling() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	m.takeHeldLocked()
	m.back, m.working = nil, false
}

func (m *fakeMod) reload() {
	m.stopPolling()
	m.load()
}
