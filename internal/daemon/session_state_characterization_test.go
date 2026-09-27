package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

const characterizationOldTimestamp = "2000-01-01T00:00:00Z"

func addCharacterizationSession(
	t *testing.T,
	d *Daemon,
	id string,
	agent protocol.SessionAgent,
	state protocol.SessionState,
) string {
	t.Helper()
	directory := t.TempDir()
	workspaceID := "workspace-" + id
	addTestWorkspace(d, workspaceID, directory)
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          agent,
		Directory:      directory,
		State:          state,
		StateSince:     characterizationOldTimestamp,
		StateUpdatedAt: characterizationOldTimestamp,
		LastSeen:       characterizationOldTimestamp,
	})
	d.associateSessionWithWorkspace(id, workspaceID)
	return workspaceID
}

func characterizationEventCount(events []protocol.WebSocketEvent, eventName, sessionID string) int {
	count := 0
	for _, event := range events {
		if event.Event != eventName {
			continue
		}
		if sessionID != "" && (event.Session == nil || event.Session.ID != sessionID) {
			continue
		}
		count++
	}
	return count
}

func TestSessionStateCharacterization_PluginCASGatesEffects(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
	sessionID := "plugin-state"
	addCharacterizationSession(t, d, sessionID, "snipe", protocol.SessionStateLaunching)
	if !d.store.BeginAgentDriverRun(sessionID, "snipe-plugin", "run-current") {
		t.Fatal("failed to begin plugin run")
	}
	capture := captureBroadcasts(d)

	if !d.applyPluginReportedState(pluginReportStateParams{
		SessionID: sessionID,
		RunID:     "run-current",
		Seq:       2,
		State:     protocol.StateWorking,
	}) {
		t.Fatal("fresh plugin report was rejected")
	}
	accepted := d.store.Get(sessionID)
	if accepted == nil || accepted.State != protocol.SessionStateWorking {
		t.Fatalf("session=%+v, want working", accepted)
	}
	if accepted.LastSeen == characterizationOldTimestamp {
		t.Fatal("accepted plugin report did not Touch the session")
	}
	stateEventsAfterAccepted := characterizationEventCount(capture.snapshot(), protocol.EventSessionStateChanged, sessionID)

	if d.applyPluginReportedState(pluginReportStateParams{
		SessionID: sessionID,
		RunID:     "run-current",
		Seq:       1,
		State:     protocol.StateIdle,
	}) {
		t.Fatal("stale plugin report was accepted")
	}
	afterStale := d.store.Get(sessionID)
	if afterStale == nil || afterStale.State != protocol.SessionStateWorking || afterStale.StateUpdatedAt != accepted.StateUpdatedAt || afterStale.LastSeen != accepted.LastSeen {
		t.Fatalf("stale plugin report changed session: accepted=%+v after=%+v", accepted, afterStale)
	}
	if got := characterizationEventCount(capture.snapshot(), protocol.EventSessionStateChanged, sessionID); got != stateEventsAfterAccepted {
		t.Fatalf("stale plugin report emitted state event: before=%d after=%d", stateEventsAfterAccepted, got)
	}
}
