package daemon

import (
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func callAgentPeek(t *testing.T, d *Daemon, target string) protocol.Response {
	t.Helper()
	return callHandler(t, func(conn net.Conn) {
		d.handleAgentPeek(conn, &protocol.AgentPeekMessage{Cmd: protocol.CmdAgentPeek, TargetSessionID: target})
	})
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
