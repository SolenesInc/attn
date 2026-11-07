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
	ModEventHeld         = "held"
	ModEventRefusedStop  = "stopped"
	ModEventReplaced     = "replaced"
	fakeModEventBacklog  = 64
	fakeModOutcomeAppend = "appended"
	fakeModOutcomeSubmit = "submitted"
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
	methodModCompactions    = "mod_compactions"
	methodModCompactReport  = "mod_compact_report"
	methodModPause          = "mod_pause"
	methodModResume         = "mod_resume"
	methodModGiveBack       = "mod_give_back"
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

type fakeModCompactHold struct {
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

	mu       sync.Mutex
	instance string
	cancel   context.CancelFunc
	working  bool
	stopped  bool
	held     []fakeModMessage
	back     []fakeModMessage

	nextCompact ModCompactReport
	compactHold *fakeModCompactHold
	compactions int
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
	switch {
	case m.stopped:
		m.mu.Unlock()
		m.post("/claude/ack", map[string]any{"terminal": m.terminal, "instance": instance, "id": message.ID, "outcome": ModEventRefusedStop, "composer_empty": m.agent.term.composerEmpty()})
		m.events <- ModEvent{Kind: ModEventRefusedStop, Text: message.Text}
	case m.working:
		m.held = append(m.held, message)
		m.mu.Unlock()
		m.ack(instance, message.ID, ModEventHeld)
		m.events <- ModEvent{Kind: ModEventHeld, Text: message.Text}
	default:
		m.working = true
		m.mu.Unlock()
		m.submit(instance, message.Text, fakeModMessage{ID: message.ID})
	}
}

func (m *fakeMod) submit(instance, text string, messages ...fakeModMessage) {
	m.agent.term.print(fakeModPromptMarker)
	m.agent.place(text, true)
	for _, message := range messages {
		m.ackHeld(instance, message, fakeModOutcomeSubmit)
	}
}

func (m *fakeMod) ackHeld(instance string, message fakeModMessage, outcome string) {
	m.post("/claude/ack", map[string]any{"terminal": m.terminal, "instance": instance, "id": message.ID, "outcome": outcome, "ring": message.Ring})
}

func (m *fakeMod) sendGivenBack(instance string) {
	m.mu.Lock()
	back := m.back
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

func (m *fakeMod) turnCompleted() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.working = false
	held, instance := m.held, m.instance
	m.held = nil
	refused := false
	if len(held) > 0 {
		m.working = true
	} else {
		m.turnsEnded++
		refused, m.refused, m.newerTurn = m.refused, false, false
	}
	m.mu.Unlock()
	if refused {
		m.reportBetweenTurns(instance)
	}
	if len(held) == 0 {
		return
	}
	texts := make([]string, 0, len(held))
	for _, message := range held {
		texts = append(texts, message.Text)
	}
	go m.submit(instance, strings.Join(texts, "\n\n"), held...)
}

func (m *fakeMod) handle(a *agent, method string, params json.RawMessage) (any, error) {
	switch method {
	case methodModNext:
		return <-m.events, nil
	case methodModTool:
		return m.toolBoundary(), nil
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
		m.compactHold = &fakeModCompactHold{arrived: make(chan struct{}), release: make(chan struct{})}
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
	case methodModCompactions:
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.compactions, nil
	case methodModCompactReport:
		return <-m.reports, nil
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
	var hold *fakeModCompactHold
	if !running {
		next, m.nextCompact = m.nextCompact, ModCompactReport{}
		if next.Outcome != ModCompactBusy {
			m.compactions++
		}
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

func (m *fakeMod) toolBoundary() []string {
	m.mu.Lock()
	held, instance := m.held, m.instance
	m.held = nil
	m.mu.Unlock()
	appended := make([]string, 0, len(held))
	for _, message := range held {
		m.ackHeld(instance, message, fakeModOutcomeAppend)
		appended = append(appended, message.Text)
	}
	return appended
}

func (m *fakeMod) esc() []string {
	m.mu.Lock()
	held, instance := m.held, m.instance
	m.held, m.working, m.stopped = nil, false, true
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
	m.held, m.back, m.working = nil, nil, false
}

func (m *fakeMod) reload() {
	m.stopPolling()
	m.load()
}
