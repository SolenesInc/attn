package fakeclaudeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
)

const (
	APIKey              = "sk-ant-api03-attn-stack-test-000000000000"
	ToolPrompt          = "touch the marker file"
	QuestionWaitingPath = "/question-waiting"
	CompactPrompt       = "create a detailed summary of the conversation so far"
	CompactSummary      = "The checkout fix landed and the session closed itself."

	cachedPrefixTokens = 40000
)

type Model struct {
	*httptest.Server
	mu            sync.Mutex
	prompts       []string
	opening       []string
	requestTimes  []time.Time
	compactCount  int
	changed       chan struct{}
	bash          []bashStep
	compact       chan struct{}
	breakCompacts bool
	contextTokens int
}

type bashStep struct{ after, command string }

type tool struct {
	Name string `json:"name"`
}

func New() *Model {
	return &Model{changed: make(chan struct{})}
}

func Start(t testing.TB) *Model {
	t.Helper()
	m := New()
	m.Server = httptest.NewServer(m)
	t.Cleanup(m.Close)
	return m
}

func (m *Model) RunBashAfter(prompt, command string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bash = append(m.bash, bashStep{after: prompt, command: command})
}

func (m *Model) HoldCompacts() (release func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hold := make(chan struct{})
	m.compact = hold
	return sync.OnceFunc(func() {
		close(hold)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.compact == hold {
			m.compact = nil
		}
	})
}

func (m *Model) BreakCompacts(broken bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.breakCompacts = broken
}

func (m *Model) SetContextTokens(tokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contextTokens = tokens
}

func (m *Model) AwaitPrompt(t testing.TB, contains string) {
	t.Helper()
	m.AwaitPromptSince(t, 0, contains)
}

func (m *Model) Mark() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.prompts)
}

func (m *Model) Prompts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.prompts...)
}

func (m *Model) LastRequestAt() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requestTimes) == 0 {
		return time.Time{}
	}
	return m.requestTimes[len(m.requestTimes)-1]
}

func (m *Model) CompactCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.compactCount
}

func (m *Model) AwaitPromptSince(t testing.TB, mark int, contains string) (opening string) {
	t.Helper()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	for {
		m.mu.Lock()
		seen, openings, changed := append([]string(nil), m.prompts...), append([]string(nil), m.opening...), m.changed
		m.mu.Unlock()
		for i := mark; i < len(seen); i++ {
			if strings.Contains(seen[i], contains) {
				return openings[i]
			}
		}
		select {
		case <-changed:
		case <-guard.C:
			t.Fatalf("the model saw no prompt with %q within %s; prompts: %q", contains, fakeagent.HangGuard, seen[min(mark, len(seen)):])
		}
	}
}

func (m *Model) record(prompt, opening string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prompts = append(m.prompts, prompt)
	m.opening = append(m.opening, opening)
	close(m.changed)
	m.changed = make(chan struct{})
}

func (m *Model) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == QuestionWaitingPath:
		m.record(QuestionWaitingPath, "")
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v1/messages/count_tokens"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":1200}`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v1/messages"):
		m.answer(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (m *Model) usage() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.contextTokens == 0 {
		return map[string]any{"input_tokens": 1200, "output_tokens": 2, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
	}
	written := m.contextTokens - cachedPrefixTokens
	return map[string]any{
		"input_tokens": 2, "output_tokens": 2,
		"cache_creation_input_tokens": written,
		"cache_read_input_tokens":     cachedPrefixTokens,
		"cache_creation":              map[string]int{"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": written},
	}
}

func (m *Model) answer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Tools    []tool `json:"tools"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	m.requestTimes = append(m.requestTimes, time.Now())
	m.mu.Unlock()
	callsTool, compacts := false, false
	bash := slices.Contains(body.Tools, tool{Name: "Bash"})
	command := ""
	for i := len(body.Messages) - 1; i >= 0; i-- {
		if body.Messages[i].Role != "user" {
			continue
		}
		prompt := ContentText(body.Messages[i].Content)
		callsTool = strings.Contains(prompt, ToolPrompt)
		compacts = strings.Contains(prompt, CompactPrompt)
		m.mu.Lock()
		if bash && len(m.bash) > 0 && strings.Contains(prompt, m.bash[0].after) {
			command, m.bash = m.bash[0].command, m.bash[1:]
		}
		m.mu.Unlock()
		m.record(prompt, ContentText(body.Messages[0].Content))
		break
	}
	usage := m.usage()
	say := "Noted."
	if compacts {
		m.mu.Lock()
		hold, broken := m.compact, m.breakCompacts
		m.compactCount++
		if !broken {
			m.contextTokens = 0
		}
		m.mu.Unlock()
		if hold != nil {
			<-hold
		}
		if !broken {
			say = "<analysis>The user asked for the checkout fix.</analysis>\n<summary>" + CompactSummary + "</summary>"
		}
	}
	if command != "" && body.Stream {
		writeBashToolUse(w, body.Model, command, usage)
		return
	}
	if !body.Stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_mock", "type": "message", "role": "assistant", "model": body.Model,
			"content":     []map[string]string{{"type": "text", "text": say}},
			"stop_reason": "end_turn", "stop_sequence": nil, "usage": usage,
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	send := func(event string, payload map[string]any) {
		payload["type"] = event
		encoded, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, encoded)
	}
	send("message_start", map[string]any{"message": map[string]any{
		"id": "msg_mock", "type": "message", "role": "assistant", "model": body.Model, "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": usage,
	}})
	if callsTool {
		send("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "tool_use", "id": "toolu_mock", "name": "Bash", "input": map[string]any{}}})
		send("content_block_delta", map[string]any{"index": 0, "delta": map[string]string{"type": "input_json_delta", "partial_json": `{"command":"touch marker","description":"Touching a file"}`}})
		send("content_block_stop", map[string]any{"index": 0})
		send("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 2}})
		send("message_stop", map[string]any{})
		return
	}
	send("content_block_start", map[string]any{"index": 0, "content_block": map[string]string{"type": "text", "text": ""}})
	send("content_block_delta", map[string]any{"index": 0, "delta": map[string]string{"type": "text_delta", "text": say}})
	send("content_block_stop", map[string]any{"index": 0})
	send("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 2}})
	send("message_stop", map[string]any{})
}

func writeBashToolUse(w http.ResponseWriter, model, command string, usage map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	send := func(event string, payload map[string]any) {
		payload["type"] = event
		encoded, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, encoded)
	}
	input, _ := json.Marshal(map[string]string{"command": command, "description": "Run attn"})
	send("message_start", map[string]any{"message": map[string]any{
		"id": "msg_mock_bash", "type": "message", "role": "assistant", "model": model, "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": usage,
	}})
	send("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "tool_use", "id": "toolu_" + uuid.NewString()[:8], "name": "Bash", "input": map[string]any{}}})
	send("content_block_delta", map[string]any{"index": 0, "delta": map[string]string{"type": "input_json_delta", "partial_json": string(input)}})
	send("content_block_stop", map[string]any{"index": 0})
	send("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 20}})
	send("message_stop", map[string]any{})
}

func ContentText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	_ = json.Unmarshal(content, &blocks)
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "tool_result":
			parts = append(parts, ContentText(block.Content))
		}
	}
	return strings.Join(parts, "\n")
}
