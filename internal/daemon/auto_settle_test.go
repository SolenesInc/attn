package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

func newAutoSettleDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "auto-settle.sock"))
	return d, seedAutoSettleSession(t, d, t.TempDir())
}

func seedAutoSettleSession(t *testing.T, d *Daemon, dir string) string {
	t.Helper()
	id := "session"
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          protocol.SessionAgentCodex,
		Directory:      dir,
		State:          protocol.SessionStateWaitingInput,
		StateSince:     characterizationOldTimestamp,
		StateUpdatedAt: characterizationOldTimestamp,
		LastSeen:       characterizationOldTimestamp,
	})
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "3600")
	d.store.SetSetting(SettingAutoSettleCountdownSeconds, "3600")
	if !d.store.OpenTurnIfClosed(id, time.Now()) {
		t.Fatal("OpenTurnIfClosed() = false; the fixture owes no turn")
	}
	creditUserInput(t, d, id)
	return id
}

func creditUserInput(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	lane := d.sessionInputs().lane(sessionID)
	lane.mu.Lock()
	lane.userGeneration++
	lane.userSubmit = true
	lane.mu.Unlock()
	d.observePromptTaken(sessionID, "user steer", time.Now())
}

func creditUserInputForNextWorking(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	lane := d.sessionInputs().lane(sessionID)
	lane.mu.Lock()
	lane.userGeneration++
	lane.userSubmit = true
	lane.mu.Unlock()
	d.sessionInputs().observePromptTaken(sessionID, "user steer", time.Now())
}

func autoSettlePending(d *Daemon, sessionID string) (*autoSettleTimer, bool) {
	d.autoSettleMu.Lock()
	defer d.autoSettleMu.Unlock()
	entry, ok := d.autoSettleTimers[sessionID]
	return entry, ok
}

func fireAutoSettleNow(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	entry, ok := autoSettlePending(d, sessionID)
	if !ok {
		t.Fatalf("no auto-settle pending for %s", sessionID)
	}
	entry.timer.Stop()
	d.autoSettleFire(sessionID, entry.timer)
}

func turnIsOwed(d *Daemon, sessionID string) bool {
	return d.turnOwed(sessionID)
}

func TestAutoSettle_ClassificationSuspendsAndThenReevaluates(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	d.recordClassifierStarted(id, time.Now())

	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("countdown survived classification start")
	}
	if !turnIsOwed(d, id) {
		t.Fatal("classification start settled the turn")
	}

	d.concludeClassification(id, nil)
	entry, ok := autoSettlePending(d, id)
	if !ok || entry.phase != autoSettleArming {
		t.Fatalf("after classification: pending=%v entry=%+v, want a fresh arm", ok, entry)
	}
}

func TestAutoSettle_FireTimeRecheckRefusesHeldWorkingDuringClassification(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	d.recordEvidence(id, time.Now(), func(e *sessionstate.Evidence) {
		e.ClassifyingSince = time.Now()
	})

	entry, ok := autoSettlePending(d, id)
	if !ok {
		t.Fatal("no countdown to exercise the fire-time classification gate")
	}
	entry.timer.Stop()
	d.autoSettleFire(id, entry.timer)

	if !turnIsOwed(d, id) {
		t.Fatal("turn settled while the working state was held for classification")
	}
	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("timer survived a classifying fire-time refusal")
	}
}
