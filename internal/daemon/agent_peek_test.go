package daemon

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func callAgentPeek(t *testing.T, d *Daemon, target string) protocol.Response {
	t.Helper()
	return callHandler(t, func(conn net.Conn) {
		d.handleAgentPeek(conn, &protocol.AgentPeekMessage{Cmd: protocol.CmdAgentPeek, TargetSessionID: target})
	})
}

func TestHandleAgentPeekReturnsStateProfileAndLastMessage(t *testing.T) {
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
	addCharacterizationSession(t, d, "peek-target", protocol.SessionAgentCodex, protocol.SessionStateWorking)
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
	if got := protocol.Deref(result.ProfileName); got != "Default" {
		t.Fatalf("profile name = %q, want Default", got)
	}
	if protocol.Deref(result.LastAssistantMessage) != "latest answer" {
		t.Fatalf("last assistant message = %q", protocol.Deref(result.LastAssistantMessage))
	}
	if result.Screen != nil {
		t.Fatalf("screen = %+v, want absent when the backend has no snapshot", result.Screen)
	}
}

func addCharacterizationSession(
	t *testing.T,
	d *Daemon,
	id string,
	agent protocol.SessionAgent,
	state protocol.SessionState,
) {
	t.Helper()
	directory := t.TempDir()
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          agent,
		Directory:      directory,
		ProfileID:      defaultProfileID(t, d.store),
		State:          state,
		StateSince:     characterizationOldTimestamp,
		StateUpdatedAt: characterizationOldTimestamp,
		LastSeen:       characterizationOldTimestamp,
	})
}

const characterizationOldTimestamp = "2000-01-01T00:00:00Z"
