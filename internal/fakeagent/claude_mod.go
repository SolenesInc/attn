package fakeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

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
)

type ModEvent struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type fakeModMessage struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type fakeMod struct {
	dir      string
	socket   string
	terminal string
	client   *http.Client
	agent    *agent
	events   chan ModEvent

	mu       sync.Mutex
	instance string
	cancel   context.CancelFunc
	working  bool
	stopped  bool
	held     []fakeModMessage
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
		client: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}},
	}
}

func (c *claude) startMod(a *agent) {
	if c.mod != nil {
		c.mod.agent = a
		c.mod.load()
	}
}

func (m *fakeMod) load() {
	if m == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.instance, m.cancel = uuid.NewString(), cancel
	instance := m.instance
	m.mu.Unlock()
	go m.pollLoop(ctx, instance)
}

func (m *fakeMod) pollLoop(ctx context.Context, instance string) {
	query := url.Values{"terminal": {m.terminal}, "instance": {instance}}.Encode()
	for ctx.Err() == nil {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://attn/claude/next?"+query, nil)
		if err != nil {
			return
		}
		response, err := m.client.Do(request)
		if err != nil {
			return
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
			if json.Unmarshal(body, &message) == nil && ctx.Err() == nil {
				m.place(instance, message)
			}
		}
	}
}

func (m *fakeMod) place(instance string, message fakeModMessage) {
	m.mu.Lock()
	switch {
	case m.stopped:
		m.mu.Unlock()
		m.ack(instance, message.ID, ModEventRefusedStop)
		m.events <- ModEvent{Kind: ModEventRefusedStop, Text: message.Text}
	case m.working:
		m.held = append(m.held, message)
		m.mu.Unlock()
		m.ack(instance, message.ID, ModEventHeld)
		m.events <- ModEvent{Kind: ModEventHeld, Text: message.Text}
	default:
		m.working = true
		m.mu.Unlock()
		m.submit(instance, message.Text, message.ID)
	}
}

func (m *fakeMod) submit(instance, text string, ids ...string) {
	m.agent.term.print(fakeModPromptMarker)
	m.agent.place(text, true)
	for _, id := range ids {
		m.ack(instance, id, fakeModOutcomeSubmit)
	}
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
	if len(held) > 0 {
		m.working = true
	}
	m.mu.Unlock()
	if len(held) == 0 {
		return
	}
	texts, ids := make([]string, 0, len(held)), make([]string, 0, len(held))
	for _, message := range held {
		texts, ids = append(texts, message.Text), append(ids, message.ID)
	}
	go m.submit(instance, strings.Join(texts, "\n\n"), ids...)
}

func (m *fakeMod) handle(a *agent, method string) (any, error) {
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
	}
	return nil, fmt.Errorf("unknown mod method %q", method)
}

func (m *fakeMod) toolBoundary() []string {
	m.mu.Lock()
	held, instance := m.held, m.instance
	m.held = nil
	m.mu.Unlock()
	appended := make([]string, 0, len(held))
	for _, message := range held {
		m.ack(instance, message.ID, fakeModOutcomeAppend)
		appended = append(appended, message.Text)
	}
	return appended
}

func (m *fakeMod) esc() []string {
	m.mu.Lock()
	held, instance := m.held, m.instance
	m.held, m.working, m.stopped = nil, false, true
	m.mu.Unlock()
	ids, texts := make([]string, 0, len(held)), make([]string, 0, len(held))
	for _, message := range held {
		ids, texts = append(ids, message.ID), append(texts, message.Text)
	}
	m.post("/claude/return", map[string]any{"terminal": m.terminal, "instance": instance, "ids": ids})
	return texts
}

func (m *fakeMod) stopPolling() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	m.held, m.working = nil, false
}

func (m *fakeMod) reload() {
	m.stopPolling()
	m.load()
}
