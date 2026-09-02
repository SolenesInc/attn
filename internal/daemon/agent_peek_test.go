package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

func callAgentPeek(t *testing.T, d *Daemon, target string) protocol.Response {
	t.Helper()
	return callHandler(t, func(conn net.Conn) {
		d.handleAgentPeek(conn, &protocol.AgentPeekMessage{Cmd: protocol.CmdAgentPeek, TargetSessionID: target})
	})
}

func TestHandleAgentPeekReturnsStateTodosWorkspaceAndLastMessage(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	transcriptDir := filepath.Join(codexHome, "sessions", "2026", "08", "10")
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := strings.Join([]string{
		`{"timestamp":"2026-08-10T10:00:00Z","type":"session_meta","payload":{"id":"native-peek"}}`,
		`{"timestamp":"2026-08-10T10:00:01Z","type":"event_msg","payload":{"type":"agent_message","message":"first answer"}}`,
		`{"timestamp":"2026-08-10T10:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"latest answer"}}`,
	}, "\n") + "\n"
	path := filepath.Join(transcriptDir, "rollout-native-peek.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	workspaceID := addCharacterizationSession(t, d, "peek-target", protocol.SessionAgentCodex, protocol.SessionStateWorking)
	d.store.UpdateTodos("peek-target", []string{"[✓] read the plan", "[→] build peek"})
	if changed, err := d.store.TransitionSessionConversation("peek-target", "native-peek", path); err != nil || !changed {
		t.Fatalf("seed binding: changed=%v err=%v", changed, err)
	}

	resp := callAgentPeek(t, d, "peek-target")
	if !resp.Ok || resp.AgentPeekResult == nil {
		t.Fatalf("response = %+v", resp)
	}
	result := resp.AgentPeekResult
	if result.SessionID != "peek-target" || result.State != string(protocol.SessionStateWorking) {
		t.Fatalf("result identity/state = %+v", result)
	}
	if len(result.Todos) != 2 || result.Todos[1] != "[→] build peek" {
		t.Fatalf("todos = %v", result.Todos)
	}
	if result.WorkspaceID != workspaceID {
		t.Fatalf("workspace id = %q, want %q", result.WorkspaceID, workspaceID)
	}
	if protocol.Deref(result.LastAssistantMessage) != "latest answer" {
		t.Fatalf("last assistant message = %q", protocol.Deref(result.LastAssistantMessage))
	}
	if result.Screen != nil {
		t.Fatalf("screen = %+v, want absent when the backend has no snapshot", result.Screen)
	}
}

func TestHandleAgentPeekResolvesPrefixesAndNamesFailures(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addCharacterizationSession(t, d, "aaa-first", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "aab-second", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	resp := callAgentPeek(t, d, "aaa")
	if !resp.Ok || resp.AgentPeekResult == nil || resp.AgentPeekResult.SessionID != "aaa-first" {
		t.Fatalf("unique prefix response = %+v", resp)
	}

	ambiguous := callAgentPeek(t, d, "aa")
	if ambiguous.Ok || protocol.Deref(ambiguous.Error) != "ambiguous_session" {
		t.Fatalf("ambiguous response = %+v", ambiguous)
	}

	missing := callAgentPeek(t, d, "zzz")
	if missing.Ok || protocol.Deref(missing.Error) != "session_not_found" {
		t.Fatalf("missing response = %+v", missing)
	}
}

type peekSnapshotBackend struct {
	*fakeSpawnBackend
	snapshot pty.ScreenSnapshotInfo
}

func (b *peekSnapshotBackend) ScreenSnapshot(context.Context, string) (pty.ScreenSnapshotInfo, error) {
	return b.snapshot, nil
}

func TestHandleAgentPeekServesTheRenderedScreen(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addCharacterizationSession(t, d, "peek-screen", protocol.SessionAgentClaude, protocol.SessionStateWorking)
	d.ptyBackend = &peekSnapshotBackend{
		fakeSpawnBackend: &fakeSpawnBackend{},
		snapshot: pty.ScreenSnapshotInfo{
			Screen: &pty.ViewportSnapshot{Text: "$ make test\nok\n", HasText: true, Cols: 80, Rows: 24},
		},
	}

	resp := callAgentPeek(t, d, "peek-screen")
	if !resp.Ok || resp.AgentPeekResult == nil || resp.AgentPeekResult.Screen == nil {
		t.Fatalf("response = %+v", resp)
	}
	screen := resp.AgentPeekResult.Screen
	if screen.Text != "$ make test\nok\n" || screen.Cols != 80 || screen.Rows != 24 {
		t.Fatalf("screen = %+v", screen)
	}
}

func TestHandleAgentPeekServesTheScreenKeptWhenTheProcessExited(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{screen: "Error: Model \"gpt-5.6-sol\" is ambiguous across providers\n"}
	workspaceID, sessionID, cwd := setupDelegationSource(t, d, backend)

	if !d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 1}) {
		t.Fatal("process exit was suppressed")
	}
	backend.mu.Lock()
	backend.screenUnavailable = true
	backend.mu.Unlock()

	resp := callAgentPeek(t, d, sessionID)
	if !resp.Ok || resp.AgentPeekResult == nil {
		t.Fatalf("response = %+v", resp)
	}
	result := resp.AgentPeekResult
	if result.Exit == nil || result.Exit.Code != 1 || result.Exit.Signal != nil || result.Exit.At == "" {
		t.Fatalf("exit = %+v, want code 1 with a timestamp", result.Exit)
	}
	if result.Screen == nil || !strings.Contains(result.Screen.Text, "is ambiguous across providers") {
		t.Fatalf("screen = %+v, want the viewport kept at exit", result.Screen)
	}

	backend.mu.Lock()
	backend.screenUnavailable = false
	backend.spawnErr = errors.New("pty spawn refused")
	backend.mu.Unlock()
	client := newWorkspaceProtocolTestClient()
	respawn := &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: sessionID, Cwd: cwd, WorkspaceID: workspaceID,
		Agent: protocol.AgentShellValue, Cols: 80, Rows: 24, Label: protocol.Ptr("Source"),
	}
	d.handleSpawnSession(client, respawn)
	expectSpawnResult(t, client, sessionID, false)
	resp = callAgentPeek(t, d, sessionID)
	if resp.AgentPeekResult == nil || resp.AgentPeekResult.Exit == nil || resp.AgentPeekResult.Screen == nil {
		t.Fatalf("peek after a failed respawn = %+v, want the exit and its screen still kept", resp.AgentPeekResult)
	}

	backend.mu.Lock()
	backend.spawnErr = nil
	backend.mu.Unlock()
	d.handleSpawnSession(client, respawn)
	expectSpawnResult(t, client, sessionID, true)
	resp = callAgentPeek(t, d, sessionID)
	if resp.AgentPeekResult == nil || resp.AgentPeekResult.Exit != nil {
		t.Fatalf("peek after respawn = %+v, want the exit forgotten", resp.AgentPeekResult)
	}
}

func TestClampExitScreenTextKeepsTheTailAndSaysSo(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	text := strings.Repeat(line, exitScreenMaxBytes/100+50)
	clamped := clampExitScreenText(text)
	if len(clamped) > exitScreenMaxBytes+200 {
		t.Fatalf("clamped to %d bytes, want about %d", len(clamped), exitScreenMaxBytes)
	}
	head, _, _ := strings.Cut(clamped, "\n")
	if !strings.HasPrefix(head, "[exit screen truncated: ") || !strings.Contains(head, "attn keeps the last 262144]") {
		t.Fatalf("truncation notice = %q", head)
	}
	if !strings.HasSuffix(clamped, line) {
		t.Fatal("clamped text lost its tail")
	}
	if clampExitScreenText("short\n") != "short\n" {
		t.Fatal("a short screen must pass through untouched")
	}
}
