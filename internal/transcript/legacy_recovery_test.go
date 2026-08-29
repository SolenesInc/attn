package transcript

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyJSAndShellASTRequireLiteralTicketStatusCall(t *testing.T) {
	source := `const ignored = "tools.exec_command({cmd: 'attn ticket status done'})";
const result = await tools.exec_command({cmd: "$ATTN_WRAPPER_PATH ticket status completed --comment 'finished safely'"});`
	commands := legacyJSStringProperties(source, "exec_command", "cmd")
	if len(commands) != 1 || commands[0] != "$ATTN_WRAPPER_PATH ticket status completed --comment 'finished safely'" {
		t.Fatalf("commands = %#v", commands)
	}
	calls := legacyStatusCalls("functions.exec", source)
	if len(calls) != 1 || calls[0].state != "done" || !calls[0].simple || calls[0].explicit {
		t.Fatalf("calls = %#v", calls)
	}

	rejected := []string{
		`await tools.exec_command({cmd: command})`,
		`await tools.exec_command({cmd: ` + "`attn ticket status ${state}`" + `})`,
		`attn ticket status done | tee /tmp/out`,
		`for id in a b; do attn ticket status --ticket "$id" done; done`,
	}
	for _, input := range rejected {
		calls := legacyStatusCalls("functions.exec", input)
		if len(calls) > 0 && calls[0].simple && !calls[0].explicit {
			t.Fatalf("%q produced a conversation binding: %#v", input, calls)
		}
	}
}

func TestInspectCodexLegacyRecoveryTranscript(t *testing.T) {
	dataRoot := t.TempDir()
	contextPath := filepath.Join(dataRoot, "workspace-contexts", "abcd", "context.md")
	source := LegacyRecoverySource{Provider: "codex", NativeSessionID: "native-1", Path: filepath.Join(t.TempDir(), "rollout.jsonl")}
	lines := []string{
		`{"timestamp":"2026-08-01T10:00:00Z","type":"session_meta","payload":{"id":"native-1","cwd":"/work"}}`,
		mustJSONLine(t, map[string]any{"timestamp": "2026-08-01T10:00:01Z", "type": "response_item", "payload": map[string]any{
			"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "attn checked out this workspace's shared context for this session at \"" + contextPath + "\"."}},
		}}),
		`{"timestamp":"2026-08-01T10:00:02Z","type":"event_msg","payload":{"type":"user_message","message":"recover the thing"}}`,
		`{"timestamp":"2026-08-01T10:00:03Z","type":"event_msg","payload":{"type":"agent_reasoning","text":"secret thought"}}`,
		`{"timestamp":"2026-08-01T10:00:04Z","type":"event_msg","payload":{"type":"agent_message","message":"done now"}}`,
		mustJSONLine(t, map[string]any{"timestamp": "2026-08-01T10:00:05Z", "type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call", "name": "functions.exec", "call_id": "call-1", "input": `const r = await tools.exec_command({cmd: "$ATTN_WRAPPER_PATH ticket status done"});`,
		}}),
		mustJSONLine(t, map[string]any{"timestamp": "2026-08-01T10:00:06Z", "type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call_output", "call_id": "call-1", "output": "ticket old-work → done",
		}}),
	}
	writeLegacyJSONL(t, source.Path, lines)

	inspection, err := InspectLegacyRecoveryTranscript(source, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Production || inspection.CWD != "/work" {
		t.Fatalf("production=%v cwd=%q", inspection.Production, inspection.CWD)
	}
	if len(inspection.Receipts) != 1 || !inspection.Receipts[0].Bound || inspection.Receipts[0].TicketID != "old-work" || inspection.Receipts[0].State != "done" {
		t.Fatalf("receipts = %#v", inspection.Receipts)
	}
	if inspection.FirstHuman != "recover the thing" || !strings.Contains(inspection.Conversation, "done now") {
		t.Fatalf("conversation = %q", inspection.Conversation)
	}
	for _, excluded := range []string{"checked out this workspace", "secret thought", "ticket old-work"} {
		if strings.Contains(inspection.Conversation, excluded) {
			t.Fatalf("conversation retained %q: %s", excluded, inspection.Conversation)
		}
	}

	wrongRoot, err := InspectLegacyRecoveryTranscript(source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if wrongRoot.Production || len(wrongRoot.Receipts) != 1 || wrongRoot.Conversation != "" {
		t.Fatalf("wrong profile accepted: %#v", wrongRoot)
	}
}

func TestCodexLegacyRecoveryFollowsYieldedExecCell(t *testing.T) {
	source := LegacyRecoverySource{Provider: "codex", NativeSessionID: "native-2"}
	records := legacyRecords(t,
		mustJSONLine(t, map[string]any{"timestamp": "2026-08-01T10:00:00Z", "type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call", "name": "functions.exec", "call_id": "exec-1", "input": `await tools.exec_command({cmd: "attn ticket status failed"})`,
		}}),
		mustJSONLine(t, map[string]any{"timestamp": "2026-08-01T10:00:01Z", "type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call_output", "call_id": "exec-1", "output": "Script running with cell ID cell-7",
		}}),
		mustJSONLine(t, map[string]any{"timestamp": "2026-08-01T10:00:02Z", "type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call", "name": "functions.wait", "call_id": "wait-1", "input": `await tools.wait({cell_id: "cell-7"})`,
		}}),
		mustJSONLine(t, map[string]any{"timestamp": "2026-08-01T10:00:03Z", "type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call_output", "call_id": "wait-1", "output": "ticket yielded-work → failed",
		}}),
	)
	receipts := proveLegacyTicketReceipts(source, records)
	if len(receipts) != 1 || !receipts[0].Bound || receipts[0].TicketID != "yielded-work" {
		t.Fatalf("receipts = %#v", receipts)
	}
}

func TestInspectClaudeLegacyRecoveryUsesCheckoutJoin(t *testing.T) {
	dataRoot := t.TempDir()
	native := "claude-native"
	sum := sha256.Sum256([]byte(native))
	checkoutDir := filepath.Join(dataRoot, "workspace-contexts", hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(checkoutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	context := []byte("production context\n")
	if err := os.WriteFile(filepath.Join(checkoutDir, "context.md"), context, 0o600); err != nil {
		t.Fatal(err)
	}
	contextHash := sha256.Sum256(context)
	metadata := legacyCheckoutMetadata{
		WorkspaceID: "workspace-1", SessionID: native, CanonicalHash: hex.EncodeToString(contextHash[:]), CheckedOutAt: "2026-08-01T10:00:00Z",
	}
	encoded, _ := json.Marshal(metadata)
	if err := os.WriteFile(filepath.Join(checkoutDir, "checkout.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	contextPath := filepath.Join(checkoutDir, "context.md")
	source := LegacyRecoverySource{Provider: "claude", NativeSessionID: native, Path: filepath.Join(t.TempDir(), native+".jsonl")}
	lines := []string{
		mustJSONLine(t, map[string]any{"type": "assistant", "timestamp": "2026-08-01T10:00:01Z", "sessionId": native, "cwd": "/work", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "read-1", "name": "Read", "input": map[string]any{"file_path": contextPath}},
		}}}),
		mustJSONLine(t, map[string]any{"type": "user", "timestamp": "2026-08-01T10:00:02Z", "sessionId": native, "cwd": "/work", "origin": map[string]any{"kind": "human"}, "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "finish it"},
		}}}),
		mustJSONLine(t, map[string]any{"type": "assistant", "timestamp": "2026-08-01T10:00:03Z", "sessionId": native, "cwd": "/work", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "finished"},
			map[string]any{"type": "tool_use", "id": "bash-1", "name": "Bash", "input": map[string]any{"command": "attn ticket status crashed"}},
		}}}),
		mustJSONLine(t, map[string]any{"type": "user", "timestamp": "2026-08-01T10:00:04Z", "sessionId": native, "cwd": "/work", "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "bash-1", "content": "ticket claude-work → crashed", "is_error": false},
		}}}),
	}
	writeLegacyJSONL(t, source.Path, lines)

	inspection, err := InspectLegacyRecoveryTranscript(source, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Production || len(inspection.Receipts) != 1 || !inspection.Receipts[0].Bound {
		t.Fatalf("inspection = %#v", inspection)
	}
	if strings.Contains(inspection.Conversation, "ticket claude-work") || !strings.Contains(inspection.Conversation, "finish it") {
		t.Fatalf("conversation = %s", inspection.Conversation)
	}
}

func TestInspectCopilotLegacyRecoveryUsesMeasuredNativeEnvelope(t *testing.T) {
	native := "e6f91b93-ca87-40cb-b552-876163e4ab2d"
	path := filepath.Join(t.TempDir(), native, "events.jsonl")
	source := LegacyRecoverySourceAt("copilot", path)
	lines := []string{
		mustJSONLine(t, map[string]any{"id": "event-1", "parentId": nil, "timestamp": "2026-08-29T11:53:23.574Z", "type": "session.start", "data": map[string]any{
			"sessionId": native, "version": 1, "producer": "copilot-agent", "copilotVersion": "1.0.80", "startTime": "2026-08-29T11:53:23.574Z",
			"context": map[string]any{"cwd": "/work/copilot", "gitRoot": "/work/copilot", "repository": "owner/repo", "hostType": "github"},
		}}),
		mustJSONLine(t, map[string]any{"id": "event-2", "parentId": "event-1", "timestamp": "2026-08-29T11:53:30.991Z", "type": "user.message", "data": map[string]any{
			"content": "Please recover the Copilot task", "interactionId": "interaction-1", "delivery": "idle",
		}}),
		mustJSONLine(t, map[string]any{"id": "event-3", "parentId": "event-2", "timestamp": "2026-08-29T11:53:34.252Z", "type": "assistant.message", "data": map[string]any{
			"content": "The Copilot work failed safely.", "interactionId": "interaction-1", "toolRequests": []any{},
		}}),
		mustJSONLine(t, map[string]any{"id": "event-4", "parentId": "event-3", "timestamp": "2026-08-29T11:54:31.908Z", "type": "tool.execution_start", "data": map[string]any{
			"toolCallId": "toolu_status", "toolName": "bash", "arguments": map[string]any{"command": "attn ticket status failed", "description": "Close the ticket"},
		}}),
		mustJSONLine(t, map[string]any{"id": "event-5", "parentId": "event-4", "timestamp": "2026-08-29T11:54:32.261Z", "type": "tool.execution_complete", "data": map[string]any{
			"toolCallId": "toolu_status", "success": true, "result": map[string]any{"content": "ticket copilot-work → failed", "detailedContent": "ticket copilot-work → failed"},
		}}),
	}
	writeLegacyJSONL(t, path, lines)

	inspection, err := InspectLegacyRecoveryTranscript(source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Production || inspection.CWD != "/work/copilot" || len(inspection.Receipts) != 1 ||
		!inspection.Receipts[0].Bound || inspection.Receipts[0].TicketID != "copilot-work" || inspection.Receipts[0].State != "failed" {
		t.Fatalf("inspection = %#v", inspection)
	}
	if inspection.FirstHuman != "Please recover the Copilot task" || !strings.Contains(inspection.Conversation, "The Copilot work failed safely.") ||
		strings.Contains(inspection.Conversation, "ticket copilot-work") {
		t.Fatalf("conversation = %s", inspection.Conversation)
	}

	wrongNative := source
	wrongNative.NativeSessionID = "different-native-id"
	wrong, err := InspectLegacyRecoveryTranscript(wrongNative, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if wrong.Production || len(wrong.Receipts) != 1 || wrong.Conversation != "" {
		t.Fatalf("wrong native identity accepted: %#v", wrong)
	}
}

func TestCopilotLegacyRecoveryRejectsUnmeasuredSessionStartShapes(t *testing.T) {
	native := "native-copilot"
	for name, records := range map[string][]string{
		"missing producer": {
			mustJSONLine(t, map[string]any{"type": "session.start", "data": map[string]any{"sessionId": native, "startTime": "2026-08-29T11:53:23Z", "context": map[string]any{"cwd": "/work"}}}),
		},
		"start after human": {
			mustJSONLine(t, map[string]any{"type": "user.message", "data": map[string]any{"content": "hello"}}),
			mustJSONLine(t, map[string]any{"type": "session.start", "data": map[string]any{"sessionId": native, "producer": "copilot-agent", "startTime": "2026-08-29T11:53:23Z", "context": map[string]any{"cwd": "/work"}}}),
		},
		"relative cwd": {
			mustJSONLine(t, map[string]any{"type": "session.start", "data": map[string]any{"sessionId": native, "producer": "copilot-agent", "startTime": "2026-08-29T11:53:23Z", "context": map[string]any{"cwd": "work"}}}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			production, _ := proveCopilotProduction(legacyRecords(t, records...), native)
			if production {
				t.Fatal("invalid Copilot envelope was accepted")
			}
		})
	}
}

func TestLegacyRecoveryRecordLimitIsExplicit(t *testing.T) {
	reader := bufioReader(bytes.Repeat([]byte("x"), LegacyRecoveryRecordLimit+1))
	_, err := readLegacyRecoveryRecord(reader)
	if !errors.Is(err, ErrLegacyRecoveryRecordTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func TestLegacyRecoveryTranscriptLimitIsCumulative(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeLegacyJSONL(t, path, []string{`{"type":"one"}`, `{"type":"two"}`})
	_, err := readLegacyRecoveryRecordsWithLimit(path, 20)
	if !errors.Is(err, ErrLegacyRecoveryTranscriptTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func TestInspectLegacyRecoveryRejectsOversizedTranscriptBeforeScanning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(LegacyRecoveryTranscriptLimit + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = InspectLegacyRecoveryTranscript(LegacyRecoverySource{Provider: "codex", Path: path}, t.TempDir())
	if !errors.Is(err, ErrLegacyRecoveryTranscriptTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func bufioReader(data []byte) *bufio.Reader {
	return bufio.NewReaderSize(bytes.NewReader(data), 64<<10)
}

func legacyRecords(t *testing.T, lines ...string) [][]byte {
	t.Helper()
	records := make([][]byte, len(lines))
	for i, line := range lines {
		records[i] = []byte(line)
	}
	return records
}

func writeLegacyJSONL(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustJSONLine(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
