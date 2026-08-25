package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractLastAssistantMessage(t *testing.T) {
	content := `{"type":"user","message":{"content":"Hello"}}
{"type":"assistant","message":{"content":"Hi there! How can I help you today?"}}
{"type":"user","message":{"content":"Fix the bug"}}
{"type":"assistant","message":{"content":"I've fixed the bug. The issue was in the validation logic. All tests are now passing!"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "I've fixed the bug. The issue was in the validation logic. All tests are now passing!"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessage_Truncates(t *testing.T) {
	longMsg := ""
	for i := 0; i < 100; i++ {
		longMsg += "Hello world! "
	}

	content := `{"type":"assistant","message":{"content":"` + longMsg + `"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) > 50 {
		t.Errorf("result length %d exceeds limit 50", len(result))
	}
}

func TestExtractLastAssistantMessage_NoAssistant(t *testing.T) {
	content := `{"type":"user","message":{"content":"Hello"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestExtractLastAssistantMessage_ClaudeCodeFormat(t *testing.T) {
	content := `{"type":"user","message":{"role":"user","content":"test"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Let me think..."},{"type":"text","text":"How would you like to proceed with the video description feature? I need your input on next steps."}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "How would you like to proceed with the video description feature? I need your input on next steps."
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessage_ClaudeCodeFormat_MultipleTextBlocks(t *testing.T) {
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"First paragraph."},{"type":"text","text":"Second paragraph."}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "First paragraph.\nSecond paragraph."
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessage_ClaudeCodeFormat_OnlyThinking(t *testing.T) {
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Just thinking here..."}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "" {
		t.Errorf("expected empty string for thinking-only message, got %q", result)
	}
}

func TestExtractLastAssistantMessage_MessageRoleAsAssistant(t *testing.T) {
	content := `{"message":{"role":"assistant","content":[{"type":"text","text":"Hello from assistant"}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Hello from assistant"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessage_CodexEventMessage(t *testing.T) {
	content := `{"type":"event_msg","payload":{"type":"user_message","message":"Hi"}}
{"type":"event_msg","payload":{"type":"agent_message","message":"Codex reply one."}}
{"type":"event_msg","payload":{"type":"agent_message","message":"Codex reply two."}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Codex reply two."
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessage_CodexResponseItemOutputText(t *testing.T) {
	content := `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"First output."}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Second output."}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Second output."
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessage_CopilotEvents(t *testing.T) {
	content := `{"type":"session.start","data":{"sessionId":"abc"}}
{"type":"assistant.message","data":{"content":"Copilot reply one."}}
{"type":"assistant.message","data":{"content":"Copilot final reply with question?"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "events.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessage(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Copilot final reply with question?"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExtractCopilotToolLifecycle_Start(t *testing.T) {
	line := []byte(`{"type":"tool.execution_start","data":{"toolCallId":"tool-123","toolName":"bash"}}`)
	got, ok := ExtractCopilotToolLifecycle(line)
	if !ok {
		t.Fatal("expected lifecycle event")
	}
	if got.Kind != "start" {
		t.Fatalf("kind = %q, want %q", got.Kind, "start")
	}
	if got.ToolCallID != "tool-123" {
		t.Fatalf("toolCallId = %q, want %q", got.ToolCallID, "tool-123")
	}
	if got.ToolName != "bash" {
		t.Fatalf("toolName = %q, want %q", got.ToolName, "bash")
	}
}

func TestExtractCopilotToolLifecycle_Complete(t *testing.T) {
	line := []byte(`{"type":"tool.execution_complete","data":{"toolCallId":"tool-123","success":true}}`)
	got, ok := ExtractCopilotToolLifecycle(line)
	if !ok {
		t.Fatal("expected lifecycle event")
	}
	if got.Kind != "complete" {
		t.Fatalf("kind = %q, want %q", got.Kind, "complete")
	}
	if got.ToolCallID != "tool-123" {
		t.Fatalf("toolCallId = %q, want %q", got.ToolCallID, "tool-123")
	}
	if got.ToolName != "" {
		t.Fatalf("toolName = %q, want empty", got.ToolName)
	}
}

func TestExtractCopilotToolLifecycle_IgnoresNonLifecycle(t *testing.T) {
	cases := [][]byte{
		[]byte(`{"type":"assistant.message","data":{"content":"hello"}}`),
		[]byte(`{"type":"tool.execution_start","data":{"toolName":"bash"}}`),
		[]byte(`not-json`),
	}
	for _, line := range cases {
		if _, ok := ExtractCopilotToolLifecycle(line); ok {
			t.Fatalf("expected no lifecycle for line: %s", string(line))
		}
	}
}

func TestExtractLastAssistantMessageAfterLastUser_Claude_StaleAssistantIgnored(t *testing.T) {
	content := `{"type":"user","message":{"content":"hello"}}
{"type":"assistant","message":{"content":"Hi! How can I help?"}}
{"type":"user","message":{"content":"show me what this bot does"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessageAfterLastUser(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Fatalf("expected empty result, got %q", result)
	}
}

func TestExtractLastAssistantMessageAfterLastUser_Claude_AssistantAfterLatestUser(t *testing.T) {
	content := `{"type":"user","message":{"content":"hello"}}
{"type":"assistant","message":{"content":"Hi! How can I help?"}}
{"type":"user","message":{"content":"show me what this bot does"}}
{"type":"assistant","message":{"content":"This bot tracks sessions and attention."}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessageAfterLastUser(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "This bot tracks sessions and attention."
	if result != expected {
		t.Fatalf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessageAfterLastUser_Claude_NoUserStillReturnsLastAssistant(t *testing.T) {
	content := `{"type":"assistant","message":{"content":"Hello! What can I help you with today?"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessageAfterLastUser(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "Hello! What can I help you with today?"
	if result != expected {
		t.Fatalf("got %q, want %q", result, expected)
	}
}

func TestExtractLastAssistantMessageAfterLastUser_Copilot_StaleAssistantIgnored(t *testing.T) {
	content := `{"type":"user.message","data":{"content":"hello"}}
{"type":"assistant.message","data":{"content":"Hi there!"}}
{"type":"user.message","data":{"content":"run this command"}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "events.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	result, err := ExtractLastAssistantMessageAfterLastUser(path, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Fatalf("expected empty result, got %q", result)
	}
}

func TestExtractLastAssistantMessageAfterLastUserSince_Claude_StaleByTimestampIgnored(t *testing.T) {
	content := `{"type":"user","timestamp":"2026-02-11T23:57:15.222Z","message":{"role":"user","content":"hhello!"}}
{"type":"assistant","timestamp":"2026-02-11T23:57:18.631Z","message":{"role":"assistant","content":[{"type":"text","text":"Hey! How can I help you today?"}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	minTS := time.Date(2026, 2, 11, 23, 57, 30, 0, time.UTC)
	result, err := ExtractLastAssistantMessageAfterLastUserSince(path, 500, minTS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Fatalf("expected empty result for stale assistant, got %q", result)
	}
}

func TestExtractLastAssistantMessageAfterLastUserSince_Claude_FreshAssistantReturned(t *testing.T) {
	content := `{"type":"user","timestamp":"2026-02-11T23:57:35.472Z","message":{"role":"user","content":"i don tknow"}}
{"type":"assistant","timestamp":"2026-02-11T23:57:38.427Z","message":{"role":"assistant","content":[{"type":"text","text":"No worries! Whenever something comes up, I'm here."}]}}
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	minTS := time.Date(2026, 2, 11, 23, 57, 30, 0, time.UTC)
	result, err := ExtractLastAssistantMessageAfterLastUserSince(path, 500, minTS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "No worries! Whenever something comes up, I'm here."
	if result != expected {
		t.Fatalf("got %q, want %q", result, expected)
	}
}

// The abort lines below are verbatim captures from claude 2.1.220, codex 0.146.0 and
// copilot 1.0.77 interrupted mid-turn. No hook fires, so the exact shape is the contract.

type turnAbortCase struct {
	name         string
	line         string
	wantReason   string
	wantUserHalt bool
	wantAt       string
	wantOK       bool
}

func runTurnAbortCases(t *testing.T, parse func([]byte) (TurnAbort, bool), cases []turnAbortCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			abort, ok := parse([]byte(tc.line))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (abort %+v)", ok, tc.wantOK, abort)
			}
			if !ok {
				return
			}
			if abort.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", abort.Reason, tc.wantReason)
			}
			if abort.UserHalt != tc.wantUserHalt {
				t.Fatalf("user halt = %v, want %v", abort.UserHalt, tc.wantUserHalt)
			}
			got := ""
			if !abort.At.IsZero() {
				got = abort.At.UTC().Format(time.RFC3339Nano)
			}
			if got != tc.wantAt {
				t.Fatalf("at = %q, want %q", got, tc.wantAt)
			}
		})
	}
}

func TestClaudeTurnAborted(t *testing.T) {
	runTurnAbortCases(t, ClaudeTurnAborted, []turnAbortCase{
		{
			name:         "captured interrupt entry",
			line:         `{"parentUuid":"60baa6e9-1760-40c8-9f12-134f9051980d","type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]},"uuid":"2f177d9e-15fa-4617-bb8b-fb3d8150027e","timestamp":"2026-08-01T22:08:15.284Z","interruptedMessageId":"msg_011Cdcnp6M5nmsdZuTzGRmaF"}`,
			wantReason:   "[Request interrupted by user]",
			wantUserHalt: true,
			wantAt:       "2026-08-01T22:08:15.284Z",
			wantOK:       true,
		},
		{
			name:         "captured tool-use interrupt with no dedicated field",
			line:         `{"parentUuid":"9f5d4154-154b-40c5-9f6a-a4fe7de054f2","type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]},"uuid":"b2cf7717-d87d-4613-bf08-f73edb9e0e07","timestamp":"2026-08-01T12:30:36.311Z","userType":"external","entrypoint":"cli"}`,
			wantReason:   "[Request interrupted by user for tool use]",
			wantUserHalt: true,
			wantAt:       "2026-08-01T12:30:36.311Z",
			wantOK:       true,
		},
		{
			name:         "the dedicated field carries an unrecognized marker",
			line:         `{"type":"user","message":{"role":"user","content":"[Request stopped]"},"interruptedMessageId":"msg_01"}`,
			wantReason:   "[Request interrupted by user]",
			wantUserHalt: true,
			wantOK:       true,
		},
		{
			name: "the marker pasted as a prompt is not an abort",
			line: `{"type":"user","message":{"role":"user","content":"[Request interrupted by user]"},"promptSource":"typed","origin":{"kind":"human"},"permissionMode":"auto","timestamp":"2026-08-01T21:48:07.459Z"}`,
		},
		{
			name: "the marker submitted as a block is not an abort",
			line: `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]},"promptSource":"paste","origin":{"kind":"human"}}`,
		},
		{
			name: "the marker as a bare string is not an abort",
			line: `{"type":"user","message":{"role":"user","content":"[Request interrupted by user]"},"uuid":"6d41deb5","timestamp":"2026-08-01T22:06:44.538Z"}`,
		},
		{
			name: "a user quoting the marker in a prompt is not an abort",
			line: `{"type":"user","message":{"role":"user","content":"why does [Request interrupted by user] show up in my logs?"}}`,
		},
		{
			name: "a tool result quoting the marker is not an abort",
			line: `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01","type":"tool_result","content":"parser.go:12:[Request interrupted by user]"}]}}`,
		},
		{
			name: "an assistant message is not an abort",
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
		},
		{
			name: "a codex line is not a claude abort",
			line: `{"type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted"}}`,
		},
		{
			name: "malformed json is not an abort",
			line: `{"type":"user",`,
		},
	})
}

func TestCodexTurnAborted(t *testing.T) {
	runTurnAbortCases(t, CodexTurnAborted, []turnAbortCase{
		{
			name:         "captured turn_aborted event",
			line:         `{"timestamp":"2026-08-01T21:58:33.937Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"019fbf55-ef48-70f1-991c-989a330a104a","reason":"interrupted","started_at":1785621507,"completed_at":1785621513,"duration_ms":6019}}`,
			wantReason:   "interrupted",
			wantUserHalt: true,
			wantAt:       "2026-08-01T21:58:33.937Z",
			wantOK:       true,
		},
		{
			name:       "a replaced turn is not a user halt",
			line:       `{"type":"event_msg","payload":{"type":"turn_aborted","reason":"replaced"}}`,
			wantReason: "replaced",
			wantOK:     true,
		},
		{
			name:       "leaving review mode is not a user halt",
			line:       `{"type":"event_msg","payload":{"type":"turn_aborted","reason":"review_ended"}}`,
			wantReason: "review_ended",
			wantOK:     true,
		},
		{
			name:       "running out of budget is not a user halt",
			line:       `{"type":"event_msg","payload":{"type":"turn_aborted","reason":"budget_limited"}}`,
			wantReason: "budget_limited",
			wantOK:     true,
		},
		{
			name:       "a reasonless abort is not a user halt",
			line:       `{"type":"event_msg","payload":{"type":"turn_aborted"}}`,
			wantReason: "aborted",
			wantOK:     true,
		},
		{
			name: "a completed turn is not an abort",
			line: `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"019fbf55"}}`,
		},
		{
			name: "a response item is not an abort",
			line: `{"type":"response_item","payload":{"type":"message","role":"assistant"}}`,
		},
		{
			name: "a claude line is not a codex abort",
			line: `{"type":"user","message":{"role":"user","content":"[Request interrupted by user]"},"interruptedMessageId":"msg_01"}`,
		},
	})
}

func TestCopilotTurnAborted(t *testing.T) {
	runTurnAbortCases(t, CopilotTurnAborted, []turnAbortCase{
		{
			name:         "captured abort event",
			line:         `{"type":"abort","timestamp":"2026-08-02T08:52:00.344Z","data":{"reason":"user_initiated"}}`,
			wantReason:   "user_initiated",
			wantUserHalt: true,
			wantAt:       "2026-08-02T08:52:00.344Z",
			wantOK:       true,
		},
		{
			name:       "an abort copilot caused is still reported",
			line:       `{"type":"abort","data":{"reason":"tool_failure"}}`,
			wantReason: "tool_failure",
			wantOK:     true,
		},
		{
			name:       "a reasonless abort is not a user halt",
			line:       `{"type":"abort","data":{}}`,
			wantReason: "aborted",
			wantOK:     true,
		},
		{
			name: "a turn end is not an abort",
			line: `{"type":"assistant.turn_end","data":{"turnId":"0"}}`,
		},
		{
			name: "a claude line is not a copilot abort",
			line: `{"type":"user","message":{"role":"user","content":"[Request interrupted by user]"},"interruptedMessageId":"msg_01"}`,
		},
	})
}
