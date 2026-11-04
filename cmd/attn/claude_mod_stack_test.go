package main_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const mockModelAPIKey = "sk-ant-api03-attn-stack-test-000000000000"

func TestAgentMessageReachesRealClaudeThroughTheMod(t *testing.T) {
	t.Parallel()
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	model := startMockModel(t)
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	work := s.Path("shop")
	configDir := filepath.Join(s.Dir, "toolhome", ".claude")
	writeClaudeConfig(t, claude, configDir, work)
	s.Vars = append(s.Vars,
		"ATTN_CLAUDE_EXECUTABLE="+claude,
		"CLAUDE_CONFIG_DIR="+configDir,
		"ANTHROPIC_BASE_URL="+model.URL,
		"ANTHROPIC_API_KEY="+mockModelAPIKey,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_AUTOUPDATER=1",
	)
	s.Start()
	s.Attn("settings", "set", "claude_mod_enabled", "true")
	const reviewer = "rev-1111-2222"
	register(t, s, reviewer, "reviewer")

	app := s.App()
	recipient := s.Spawn(app, fakeagent.Claude, work)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("recipient screen:\n%s", s.Attn("agent", "peek", recipient).Stdout)
		}
	})
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)

	mark := len(app.Log())
	if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", recipient, "the discount is applied after tax"}, Session: reviewer}); sent.Code != 0 {
		t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
	}
	const ring = "attn agent inbox"
	model.awaitPrompt(t, ring)
	working := testworld.AwaitSessionSince(app, mark, recipient, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })

	rows := claudeUserRows(t, configDir)
	for _, row := range rows {
		if strings.Contains(row.Text, ring) {
			if row.Origin.Kind != "plugin" || row.Origin.Name != "attn" {
				t.Fatalf("Claude recorded the ring with origin %+v, want the attn plugin's; it was typed, not handed to the mod", row.Origin)
			}
			return
		}
	}
	t.Fatalf("Claude's transcript has no user row with %q; rows: %+v", ring, rows)
}

type claudeUserRow struct {
	Text   string
	Origin struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
}

func claudeUserRows(t *testing.T, configDir string) []claudeUserRow {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(configDir, "projects", "*", "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("find Claude's transcript under %s: %v (found %d)", configDir, err, len(files))
	}
	var rows []claudeUserRow
	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := bufio.NewScanner(f)
		lines.Buffer(nil, 16<<20)
		for lines.Scan() {
			var entry struct {
				Type    string          `json:"type"`
				Origin  json.RawMessage `json:"origin"`
				Message struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(lines.Bytes(), &entry) != nil || entry.Type != "user" {
				continue
			}
			var row claudeUserRow
			row.Text = contentText(entry.Message.Content)
			if len(entry.Origin) > 0 {
				_ = json.Unmarshal(entry.Origin, &row.Origin)
			}
			rows = append(rows, row)
		}
		f.Close()
	}
	return rows
}

func writeClaudeConfig(t *testing.T, claude, configDir, work string) {
	t.Helper()
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(claude, "--version").Output()
	if err != nil {
		t.Fatalf("claude --version: %v", err)
	}
	version, _, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	trusted := map[string]any{"hasTrustDialogAccepted": true, "hasCompletedProjectOnboarding": true}
	state := map[string]any{
		"hasCompletedOnboarding":                        true,
		"lastOnboardingVersion":                         version,
		"lastReleaseNotesSeen":                          version,
		"hasSeenAutoDefaultNotice":                      true,
		"autoModeClassifierBillingNoticeAcknowledgedAt": time.Now().UnixMilli(),
		"theme":                 "dark",
		"customApiKeyResponses": map[string]any{"approved": []string{mockModelAPIKey[len(mockModelAPIKey)-20:]}, "rejected": []string{}},
		"projects":              map[string]any{work: trusted, resolved: trusted},
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, ".claude.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

type mockModel struct {
	*httptest.Server
	mu      sync.Mutex
	prompts []string
	changed chan struct{}
}

func startMockModel(t *testing.T) *mockModel {
	t.Helper()
	m := &mockModel{changed: make(chan struct{})}
	m.Server = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.Close)
	return m
}

func (m *mockModel) awaitPrompt(t *testing.T, contains string) {
	t.Helper()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	for {
		m.mu.Lock()
		seen, changed := append([]string(nil), m.prompts...), m.changed
		m.mu.Unlock()
		for _, prompt := range seen {
			if strings.Contains(prompt, contains) {
				return
			}
		}
		select {
		case <-changed:
		case <-guard.C:
			t.Fatalf("the model saw no prompt with %q within %s; prompts: %q", contains, fakeagent.HangGuard, seen)
		}
	}
}

func (m *mockModel) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v1/messages/count_tokens"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":1200}`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v1/messages"):
		m.answer(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (m *mockModel) answer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for i := len(body.Messages) - 1; i >= 0; i-- {
		if body.Messages[i].Role != "user" {
			continue
		}
		m.mu.Lock()
		m.prompts = append(m.prompts, contentText(body.Messages[i].Content))
		close(m.changed)
		m.changed = make(chan struct{})
		m.mu.Unlock()
		break
	}
	const say = "Noted."
	usage := map[string]int{"input_tokens": 1200, "output_tokens": 2, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
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
	send("content_block_start", map[string]any{"index": 0, "content_block": map[string]string{"type": "text", "text": ""}})
	send("content_block_delta", map[string]any{"index": 0, "delta": map[string]string{"type": "text_delta", "text": say}})
	send("content_block_stop", map[string]any{"index": 0})
	send("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 2}})
	send("message_stop", map[string]any{})
}

func contentText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(content, &blocks)
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}
