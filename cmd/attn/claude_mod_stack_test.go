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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

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

func TestRefusingAPermissionQuestionLeavesRealClaudeFreeToTakeMessages(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"with No", "2"},
		{"with Esc", "\x1b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			settings := fmt.Sprintf(`{"permissions":{"ask":["Bash"]},"hooks":{"Notification":[{"matcher":"permission_prompt","hooks":[{"type":"command","command":"curl -s %s%s"}]}]}}`, model.URL, questionWaitingPath)
			if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(settings), 0o600); err != nil {
				t.Fatal(err)
			}
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

			app.TypeLine(recipient, mockModelToolPrompt)
			testworld.AwaitSession(app, recipient, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
			const held = "held through the question"
			app.Send(protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "held", SessionID: protocol.SessionID(recipient), Text: held})
			if taken := testworld.Await(app, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool {
				return r.RequestID == "held"
			}); taken.Status != "delivered" {
				t.Fatalf("the annotation was answered %+v, want it taken during the question", taken)
			}
			model.awaitPrompt(t, questionWaitingPath)
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(app.Terminal(recipient)), Data: tc.key})
			model.awaitPrompt(t, held)

			const later = "the discount is applied after tax"
			if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", recipient, later}, Session: reviewer}); sent.Code != 0 {
				t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
			}
			model.awaitPrompt(t, "attn agent inbox")
		})
	}
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

const (
	mockModelToolPrompt     = "touch the marker file"
	questionWaitingPath     = "/question-waiting"
	mockModelCompactPrompt  = "create a detailed summary of the conversation so far"
	mockModelCompactSummary = "The checkout fix landed and the session closed itself."
)

type mockModel struct {
	*httptest.Server
	mu      sync.Mutex
	prompts []string
	opening []string
	changed chan struct{}
	bash    []mockBashStep
	compact chan struct{}
}

type mockBashStep struct{ after, command string }

type mockTool struct {
	Name string `json:"name"`
}

func (m *mockModel) runBashAfter(prompt, command string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bash = append(m.bash, mockBashStep{after: prompt, command: command})
}

func startMockModel(t *testing.T) *mockModel {
	t.Helper()
	m := &mockModel{changed: make(chan struct{})}
	m.Server = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.Close)
	return m
}

func (m *mockModel) holdCompacts() (release func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.compact = make(chan struct{})
	return sync.OnceFunc(func() { close(m.compact) })
}

func (m *mockModel) awaitPrompt(t *testing.T, contains string) {
	t.Helper()
	m.awaitPromptSince(t, 0, contains)
}

func (m *mockModel) mark() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.prompts)
}

func (m *mockModel) awaitPromptSince(t *testing.T, mark int, contains string) (opening string) {
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

func (m *mockModel) record(prompt, opening string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prompts = append(m.prompts, prompt)
	m.opening = append(m.opening, opening)
	close(m.changed)
	m.changed = make(chan struct{})
}

func (m *mockModel) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == questionWaitingPath:
		m.record(questionWaitingPath, "")
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
		Model    string     `json:"model"`
		Stream   bool       `json:"stream"`
		Tools    []mockTool `json:"tools"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	callsTool, compacts := false, false
	bash := slices.Contains(body.Tools, mockTool{Name: "Bash"})
	command := ""
	for i := len(body.Messages) - 1; i >= 0; i-- {
		if body.Messages[i].Role != "user" {
			continue
		}
		prompt := contentText(body.Messages[i].Content)
		callsTool = strings.Contains(prompt, mockModelToolPrompt)
		compacts = strings.Contains(prompt, mockModelCompactPrompt)
		m.mu.Lock()
		if bash && len(m.bash) > 0 && strings.Contains(prompt, m.bash[0].after) {
			command, m.bash = m.bash[0].command, m.bash[1:]
		}
		m.mu.Unlock()
		m.record(prompt, contentText(body.Messages[0].Content))
		break
	}
	say := "Noted."
	if compacts {
		m.mu.Lock()
		hold := m.compact
		m.mu.Unlock()
		if hold != nil {
			<-hold
		}
		say = "<analysis>The user asked for the checkout fix.</analysis>\n<summary>" + mockModelCompactSummary + "</summary>"
	}
	usage := map[string]int{"input_tokens": 1200, "output_tokens": 2, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
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

func writeBashToolUse(w http.ResponseWriter, model, command string, usage map[string]int) {
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

func contentText(content json.RawMessage) string {
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
			parts = append(parts, contentText(block.Content))
		}
	}
	return strings.Join(parts, "\n")
}

func TestMailFromHomeReachesRealClaudeOnAnOutpostThroughTheModAndIsTypedOnceTheSettingIsOff(t *testing.T) {
	t.Parallel()
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	model := startMockModel(t)
	home, outpost := testworld.NewStack(t), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	work := outpost.Path("shop")
	configDir := filepath.Join(outpost.Dir, "toolhome", ".claude")
	writeClaudeConfig(t, claude, configDir, work)
	outpost.Vars = append(outpost.Vars,
		"ATTN_CLAUDE_EXECUTABLE="+claude,
		"CLAUDE_CONFIG_DIR="+configDir,
		"ANTHROPIC_BASE_URL="+model.URL,
		"ANTHROPIC_API_KEY="+mockModelAPIKey,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_AUTOUPDATER=1",
	)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	requireStdout(t, home.Attn("settings", "set", "claude_mod_enabled", "true"), "claude_mod_enabled = true")
	copied := outpost.App()
	pair.Connect()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	testworld.Await(copied, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["claude_mod_enabled"] == "true" })
	const reviewer = "rev-1111-2222"
	register(t, home, reviewer, "reviewer")

	app := pair.App
	recipient := home.Spawn(app, fakeagent.Claude, work, func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("recipient screen:\n%s", home.Attn("agent", "peek", recipient).Stdout)
		}
	})
	outpost.AwaitTimer(daemon.ClaudeModPollHoldTimer)

	const ring = "attn agent inbox"
	originOfRing := func(rings int, mail string) (string, string) {
		t.Helper()
		before := model.mark()
		appMark := len(app.Log())
		if sent := home.Run(testworld.Invocation{Args: []string{"agent", "msg", recipient, mail}, Session: reviewer}); sent.Code != 0 {
			t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
		}
		model.awaitPromptSince(t, before, ring)
		worked := false
		testworld.AwaitSessionSince(app, appMark, recipient, func(s protocol.Session) bool {
			worked = worked || s.State == protocol.SessionStateWorking
			return worked && s.State != protocol.SessionStateWorking
		})
		rows := claudeUserRows(t, configDir)
		var matching []claudeUserRow
		for _, row := range rows {
			if strings.Contains(row.Text, ring) {
				matching = append(matching, row)
			}
		}
		if len(matching) != rings {
			t.Fatalf("Claude's transcript has %d user rows with %q, want %d; rows: %+v", len(matching), ring, rings, rows)
		}
		last := matching[len(matching)-1]
		return last.Origin.Kind, last.Origin.Name
	}

	if kind, name := originOfRing(1, "the discount is applied after tax"); kind != "plugin" || name != "attn" {
		t.Fatalf("Claude recorded the ring with origin %s/%s, want the attn plugin's; it was typed, not handed to the mod", kind, name)
	}
	t.Log("with the setting on, Claude on the outpost recorded the ring with origin plugin/attn")

	if read := home.Run(testworld.Invocation{Args: []string{"agent", "inbox"}, Session: recipient}); read.Code != 0 || !strings.Contains(read.Stdout, "the discount is applied after tax") {
		t.Fatalf("attn agent inbox exited %d with %q%s", read.Code, read.Stdout, read.Stderr)
	}
	requireStdout(t, home.Attn("settings", "set", "claude_mod_enabled", "false"), "claude_mod_enabled = false")
	testworld.Await(copied, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["claude_mod_enabled"] == "false" })
	if kind, name := originOfRing(2, "the total is rounded last"); kind == "plugin" {
		t.Fatalf("Claude recorded the ring with origin %s/%s with the setting off, want it typed", kind, name)
	}
	t.Log("with the setting off, the same session was typed into")
}

func TestAQuickCaptureRealClaudesModHoldsThroughADaemonRestartComesBackUnreadAtEsc(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"permissions":{"allow":["Bash(cat:*)"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Vars = append(s.Vars,
		"ATTN_CLAUDE_EXECUTABLE="+claude,
		"CLAUDE_CONFIG_DIR="+configDir,
		"ANTHROPIC_BASE_URL="+model.URL,
		"ANTHROPIC_API_KEY="+mockModelAPIKey,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_AUTOUPDATER=1",
	)
	s.Start()
	requireStdout(t, s.Attn("settings", "set", "claude_mod_enabled", "true"), "claude_mod_enabled = true")
	app := s.TrustedApp()
	recipient := s.Spawn(app, fakeagent.Claude, work)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("recipient screen:\n%s", s.Attn("agent", "peek", recipient).Stdout)
		}
	})
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)

	const longTask = "run the long task"
	fifo := makeFifo(t, filepath.Join(work, "tool.fifo"))
	model.runBashAfter(longTask, "cat "+fifo)
	app.TypeLine(recipient, longTask)
	opened := make(chan *os.File, 1)
	go func() {
		toolRunning, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err == nil {
			opened <- toolRunning
		}
	}()
	select {
	case toolRunning := <-opened:
		defer toolRunning.Close()
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("Claude did not start the tool within %s", fakeagent.HangGuard)
	}

	capture := uuid.NewString()
	const words = "check the rounding"
	if _, err := app.QuickCapture(protocol.QuickCaptureSendMessage{
		Cmd: protocol.CmdQuickCaptureSend, CaptureID: capture, Content: words, AttachmentIds: []string{},
		Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID(recipient))},
	}); err != nil {
		t.Fatal(err)
	}
	testworld.Await(app, protocol.EventQuickCaptureRead, func(m protocol.QuickCaptureReadMessage) bool { return m.CaptureID == capture })

	s.Stop()
	s.Start()
	app = s.TrustedApp()
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)

	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(app.Terminal(recipient)), Data: "\x1b"})
	testworld.Await(app, protocol.EventQuickCaptureUnread, func(m protocol.QuickCaptureUnreadMessage) bool { return m.CaptureID == capture })

	mark := model.mark()
	app.TypeLine(recipient, "carry on")
	model.awaitPromptSince(t, mark, words)
}
