package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

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

func TestSessionStateCharacterization_ALateVerdictDoesNotOverwriteAnApproval(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		sessionID := "stale-classifier"
		addCharacterizationSession(t, d, sessionID, protocol.SessionAgentCodex, protocol.SessionStateWorking)
		classifier := newBlockingClassifier(protocol.StateIdle)
		d.classifier = classifier

		transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
		content := `{"type":"assistant","message":{"role":"assistant","content":"Finished."}}` + "\n"
		if err := os.WriteFile(transcriptPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write transcript: %v", err)
		}
		capture := captureBroadcasts(d)

		classified := make(chan struct{})
		go func() {
			d.classifySessionState(sessionID, transcriptPath)
			close(classified)
		}()

		synctest.Wait()
		select {
		case <-classifier.started:
		default:
			close(classifier.release)
			t.Fatal("classifier did not start")
		}

		d.handleState(&syncConn{}, &protocol.StateMessage{ID: sessionID, State: protocol.StatePendingApproval})
		d.resolveDue(time.Now())
		fresh := d.store.Get(sessionID)
		if fresh.State != protocol.SessionStatePendingApproval {
			t.Fatalf("state=%q before the verdict lands; the rest proves nothing", fresh.State)
		}
		stateEventsBeforeRelease := characterizationEventCount(capture.snapshot(), protocol.EventSessionStateChanged, sessionID)
		close(classifier.release)
		requireDone(t, classified, "classifier did not finish")

		d.resolveDue(time.Now())

		after := d.store.Get(sessionID)
		if after == nil || after.State != protocol.SessionStatePendingApproval {
			t.Fatalf("session=%+v, the late verdict overwrote pending_approval", after)
		}
		if after.StateUpdatedAt != fresh.StateUpdatedAt || after.LastSeen != fresh.LastSeen {
			t.Fatalf("stale classifier changed timestamps: fresh=%+v after=%+v", fresh, after)
		}
		if got := characterizationEventCount(capture.snapshot(), protocol.EventSessionStateChanged, sessionID); got != stateEventsBeforeRelease {
			t.Fatalf("stale classifier emitted state event: before=%d after=%d", stateEventsBeforeRelease, got)
		}
	})
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
