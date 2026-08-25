package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

func newRegistryFromClient(client github.GitHubClient) *github.ClientRegistry {
	registry := github.NewClientRegistry()
	if client == nil {
		return registry
	}
	if ghClient, ok := client.(*github.Client); ok {
		registry.Register(ghClient.Host(), ghClient)
	}
	return registry
}

type Classifier interface {
	Classify(text string, timeout time.Duration) (string, error)
}

type FakeClassifier struct {
	mu           sync.Mutex
	defaultState string
	responses    map[string]string
	calls        []ClassifyCall
}

type ClassifyCall struct {
	Text    string
	Timeout time.Duration
	Time    time.Time
}

func NewFakeClassifier(defaultState string) *FakeClassifier {
	return &FakeClassifier{
		defaultState: defaultState,
		responses:    make(map[string]string),
	}
}

func (f *FakeClassifier) SetResponse(substring, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[substring] = state
}

func (f *FakeClassifier) Classify(text string, timeout time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, ClassifyCall{
		Text:    text,
		Timeout: timeout,
		Time:    time.Now(),
	})

	for substring, state := range f.responses {
		if contains(text, substring) {
			return state, nil
		}
	}

	return f.defaultState, nil
}

func (f *FakeClassifier) Calls() []ClassifyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]ClassifyCall, len(f.calls))
	copy(result, f.calls)
	return result
}

func (f *FakeClassifier) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func contains(text, substr string) bool {
	return len(substr) > 0 && len(text) >= len(substr) && (text == substr || findSubstring(text, substr))
}

func findSubstring(text, substr string) bool {
	for i := 0; i <= len(text)-len(substr); i++ {
		if text[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

type BroadcastRecorder struct {
	mu     sync.Mutex
	events []*protocol.WebSocketEvent
}

func NewBroadcastRecorder() *BroadcastRecorder {
	return &BroadcastRecorder{}
}

func (r *BroadcastRecorder) Record(event *protocol.WebSocketEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *BroadcastRecorder) Events() []*protocol.WebSocketEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]*protocol.WebSocketEvent, len(r.events))
	copy(result, r.events)
	return result
}

func (r *BroadcastRecorder) EventsOfType(eventType string) []*protocol.WebSocketEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*protocol.WebSocketEvent
	for _, e := range r.events {
		if e.Event == eventType {
			result = append(result, e)
		}
	}
	return result
}

func (r *BroadcastRecorder) WaitForEvent(eventType string, timeout time.Duration) *protocol.WebSocketEvent {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events := r.EventsOfType(eventType)
		if len(events) > 0 {
			return events[len(events)-1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (r *BroadcastRecorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

func (r *BroadcastRecorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

type WireTrace struct {
	mu       sync.Mutex
	payloads [][]byte
}

func (t *WireTrace) record(payload []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.payloads = append(t.payloads, append([]byte(nil), payload...))
}

func (t *WireTrace) Payloads() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([][]byte, len(t.payloads))
	for i, p := range t.payloads {
		out[i] = append([]byte(nil), p...)
	}
	return out
}

func (t *WireTrace) EventNames() []string {
	payloads := t.Payloads()
	names := make([]string, 0, len(payloads))
	for _, p := range payloads {
		var envelope struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(p, &envelope); err != nil || envelope.Event == "" {
			names = append(names, "?")
			continue
		}
		names = append(names, envelope.Event)
	}
	return names
}

func (t *WireTrace) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.payloads = nil
}

func (t *WireTrace) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.payloads)
}

type TestHarness struct {
	Daemon     *Daemon
	Classifier *FakeClassifier
	Recorder   *BroadcastRecorder
	Wire       *WireTrace
	Store      *store.Store
	SockPath   string
}

type TestHarnessBuilder struct {
	socketPath      string
	defaultState    string
	ghClient        github.GitHubClient
	recordBroadcast bool
}

func NewTestHarnessBuilder(socketPath string) *TestHarnessBuilder {
	return &TestHarnessBuilder{
		socketPath:      socketPath,
		defaultState:    protocol.StateWaitingInput,
		recordBroadcast: true,
	}
}

func (b *TestHarnessBuilder) WithDefaultClassifierState(state string) *TestHarnessBuilder {
	b.defaultState = state
	return b
}

func (b *TestHarnessBuilder) WithGitHubClient(client github.GitHubClient) *TestHarnessBuilder {
	b.ghClient = client
	return b
}

func (b *TestHarnessBuilder) WithoutBroadcastRecording() *TestHarnessBuilder {
	b.recordBroadcast = false
	return b
}

func (b *TestHarnessBuilder) Build() *TestHarness {
	classifier := NewFakeClassifier(b.defaultState)
	recorder := NewBroadcastRecorder()
	wire := &WireTrace{}
	sessionStore := store.New()

	pidPath := b.socketPath + ".pid"
	dataRoot := filepath.Dir(b.socketPath)
	hub := newWSHub()
	manager := pty.NewManager(nil)

	if b.recordBroadcast {
		hub.broadcastListener = func(event *protocol.WebSocketEvent) {
			recorder.Record(event)
		}
	}
	hub.wireTap = wire.record

	d := &Daemon{
		socketPath:          b.socketPath,
		pidPath:             pidPath,
		dataRoot:            dataRoot,
		store:               sessionStore,
		wsHub:               hub,
		done:                make(chan struct{}),
		logger:              nil,
		ghRegistry:          newRegistryFromClient(b.ghClient),
		classifier:          classifier,
		ptyBackend:          ptybackend.NewEmbedded(manager),
		transcriptWatch:     make(map[string]*transcriptWatcher),
		pendingInitialWS:    make(map[*wsClient]struct{}),
		startedCh:           make(chan struct{}),
		classifiedTurn:      make(map[string]string),
		classifyingTurn:     make(map[string]string),
		pendingConversation: make(map[string]agentConversationObservation),
		plugins:             newPluginRegistry(),
	}

	return &TestHarness{
		Daemon:     d,
		Classifier: classifier,
		Recorder:   recorder,
		Wire:       wire,
		Store:      sessionStore,
		SockPath:   b.socketPath,
	}
}

func (h *TestHarness) Start() {
	go h.Daemon.Start()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", h.SockPath, 10*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *TestHarness) Stop() {
	h.Daemon.Stop()
}
