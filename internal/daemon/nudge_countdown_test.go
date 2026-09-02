package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ticketnotify"
)

func currentNudgeTimer(d *Daemon, sessionID string) *time.Timer {
	d.nudgeMu.Lock()
	defer d.nudgeMu.Unlock()
	if c, ok := d.nudgeCountdowns[sessionID]; ok {
		return c.timer
	}
	return nil
}

func currentNudgeDeadline(d *Daemon, sessionID string) time.Time {
	d.nudgeMu.Lock()
	defer d.nudgeMu.Unlock()
	if c, ok := d.nudgeCountdowns[sessionID]; ok {
		return c.firesAt
	}
	return time.Time{}
}

func settledNudgeDeadline(t *testing.T, d *Daemon, sessionID string) time.Time {
	t.Helper()
	synctest.Wait()
	deadline := currentNudgeDeadline(d, sessionID)
	if deadline.IsZero() {
		t.Fatalf("no nudge deadline armed for %s once the daemon settled", sessionID)
	}
	return deadline
}

// Tests that use it set an hour-long window override so the real timer never races it.
func fireNudgeNow(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	timer := currentNudgeTimer(d, sessionID)
	if timer == nil {
		t.Fatalf("no nudge countdown armed for session %s", sessionID)
	}
	timer.Stop()
	d.nudgeCountdownFire(sessionID, timer)
}

func armForTest(t *testing.T, d *Daemon) (agentID string, inputs func(string) []string) {
	t.Helper()
	_, agentID, inputs = delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)
	commentOnTicket(t, d, ticketID, "take a look at the failing test")
	return agentID, inputs
}

func TestNudgeCountdownFiresWhenInactive(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		agentID, inputs := armForTest(t, d)

		time.Sleep(defaultNudgeCountdownWindow - time.Second)
		synctest.Wait()
		if wasNudged(inputs(agentID)) {
			t.Fatal("the doorbell rang before the countdown window elapsed")
		}

		time.Sleep(time.Second)
		synctest.Wait()
		if !wasNudged(inputs(agentID)) {
			t.Fatalf("session %s was never doorbelled", agentID)
		}
	})
}

func TestNudgeCountdownPausedWhileActive(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agentID, inputs := delegateForNotify(t, d, "codex")
	ticketID := boundTicketID(t, d, agentID)
	d.store.UpdateState(agentID, protocol.StateIdle)
	d.setSelectedSession(agentID)

	commentOnTicket(t, d, ticketID, "take a look")

	if timer := currentNudgeTimer(d, agentID); timer != nil {
		t.Fatal("active session armed a countdown instead of pausing")
	}
	if wasNudged(inputs(agentID)) {
		t.Fatal("active session was doorbelled (splice risk)")
	}
	clone := d.sessionForBroadcast(d.store.Get(agentID))
	if !protocol.Deref(clone.TicketUnread) {
		t.Fatal("active session lost its unread indicator")
	}
	if clone.NudgeFiresAt != nil {
		t.Fatal("paused (active) session should not broadcast a countdown deadline")
	}
}

func TestNudgeCountdownResumesOnSwitchAway(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		d.nudgeWindowOverride = time.Hour
		stopDaemonBackground(t, d)
		chiefID, agentID, _ := delegateForNotify(t, d, "codex")
		ticketID := boundTicketID(t, d, agentID)
		d.store.UpdateState(agentID, protocol.StateIdle)
		d.setSelectedSession(agentID)
		commentOnTicket(t, d, ticketID, "take a look")
		if currentNudgeTimer(d, agentID) != nil {
			t.Fatal("countdown ran while the session was active")
		}

		d.setSelectedSession(chiefID)

		settledNudgeDeadline(t, d, agentID)
	})
}

func TestBufferedNudgePreservesDeadlineAcrossSelectionPause(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		d.nudgeWindowOverride = time.Second
		d.ticketBundleWindowOverride = time.Hour
		stopDaemonBackground(t, d)
		chiefID, agentID, _ := delegateForNotify(t, d, "codex")
		ticketID := boundTicketID(t, d, agentID)
		if _, err := ticketnotify.ConsumeAll(d.store, d.ticketObserversForSession(chiefID), time.Now()); err != nil {
			t.Fatal(err)
		}
		attentionAt := time.Now().Add(-10 * time.Minute)
		if err := d.store.SetTicketDeliveryAttention(d.ticketAttentionKey(chiefID), attentionAt); err != nil {
			t.Fatal(err)
		}
		d.setSelectedSession(chiefID)
		commentOnTicket(t, d, ticketID, "buffer this")
		if deadline := currentNudgeDeadline(d, chiefID); !deadline.IsZero() {
			t.Fatalf("selected chief armed deadline %s", deadline)
		}

		d.setSelectedSession(agentID)
		deadline := settledNudgeDeadline(t, d, chiefID)
		want := attentionAt.Add(time.Hour)
		// Store timestamps use RFC3339 second precision, so the durable round-trip may trim sub-seconds.
		if delta := deadline.Sub(want); delta < -time.Second || delta > time.Second {
			t.Fatalf("resumed deadline = %s, want %s", deadline, want)
		}
	})
}

func TestRebuildTicketDeliverySchedulesRearmsUnread(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		d.nudgeWindowOverride = time.Hour
		stopDaemonBackground(t, d)
		_, agentID, _ := delegateForNotify(t, d, "codex")
		ticketID := boundTicketID(t, d, agentID)
		d.store.UpdateState(agentID, protocol.StateIdle)
		commentOnTicket(t, d, ticketID, "survive restart")
		d.cancelNudgeCountdown(agentID, "simulate daemon restart")
		if currentNudgeTimer(d, agentID) != nil {
			t.Fatal("countdown still armed before rebuild")
		}

		d.rebuildTicketDeliverySchedules()
		settledNudgeDeadline(t, d, agentID)
	})
}

func TestNudgeCountdownPausesOnSwitchTo(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, _ := armForTest(t, d)
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("inactive session did not arm a countdown")
	}

	d.setSelectedSession(agentID)

	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("countdown kept running after the session became active")
	}
}

func TestNudgeCountdownSurvivesEligibleStateChange(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, _ := armForTest(t, d)
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("precondition: no countdown armed")
	}

	d.applyState(sessionStateChange{
		sessionID: agentID,
		state:     protocol.StateWorking,
		cause:     resolverObservation{},
	})

	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("countdown was canceled when the agent became active")
	}
}

func TestNudgeCountdownCancelsOnPendingApproval(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, _ := armForTest(t, d)

	d.applyState(sessionStateChange{
		sessionID: agentID,
		state:     protocol.StatePendingApproval,
		cause:     resolverObservation{},
	})

	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("countdown survived a pending approval prompt")
	}
}

func TestSessionInputWriteDoesNotInterleaveWithPendingApproval(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := "doorbell-state-fence"
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             sessionID,
		Label:          "doorbell-state-fence",
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	inputStarted := make(chan struct{})
	releaseInput := make(chan struct{})
	inputs := make(chan string, 2)
	var firstWrite sync.Once
	d.ptyBackend = &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		inputs <- string(data)
		firstWrite.Do(func() {
			close(inputStarted)
			<-releaseInput
		})
	}}

	inputDone := make(chan error, 1)
	delivery := maintenanceSessionInput("input-test", "countdown-splice", sessionID, ticketNudgePrompt, sessionInputAtTurnBoundary)
	go func() { inputDone <- d.sessionInputs().try(context.Background(), delivery).err }()
	<-inputStarted

	stateDone := make(chan struct{})
	go func() {
		d.applyState(sessionStateChange{
			sessionID: sessionID,
			state:     protocol.StatePendingApproval,
			cause:     resolverObservation{},
		})
		close(stateDone)
	}()
	select {
	case <-stateDone:
		t.Fatal("pending approval committed while a doorbell input was in flight")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseInput)
	if err := <-inputDone; err != nil {
		t.Fatalf("session input error = %v", err)
	}
	<-stateDone
	wantPaste := sessionInputPasteStart + ticketNudgePrompt + sessionInputPasteEnd
	if got := <-inputs; got != wantPaste {
		t.Fatalf("doorbell paste = %q, want %q", got, wantPaste)
	}
	if got := <-inputs; got != "\r" {
		t.Fatalf("session-input submit = %q, want a lone Enter", got)
	}
}

func TestNudgeDeliveryStatePolicy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
		want  bool
	}{
		{name: "active green", state: protocol.StateWorking, want: true},
		{name: "new initial", state: protocol.StateLaunching, want: true},
		{name: "unknown", state: protocol.StateUnknown, want: true},
		{name: "waiting for input", state: protocol.StateWaitingInput, want: true},
		{name: "flashing approval", state: protocol.StatePendingApproval, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionInputPhaseAllows(sessionInputAtTurnBoundary, protocol.SessionState(tc.state)); got != tc.want {
				t.Fatalf("sessionInputPhaseAllows(%q) = %v, want %v", tc.state, got, tc.want)
			}
		})
	}
}

func TestNudgeCountdownClearedWhenInboxDrained(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, inputs := armForTest(t, d)
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("precondition: no countdown armed")
	}

	callTicketInbox(t, d, agentID)

	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("countdown survived the inbox drain")
	}
	clone := d.sessionForBroadcast(d.store.Get(agentID))
	if protocol.Deref(clone.TicketUnread) {
		t.Fatal("indicator stuck on after the inbox drained to zero")
	}
	if wasNudged(inputs(agentID)) {
		t.Fatal("a drained session was still doorbelled")
	}
}

func TestTriggerNudgeFiresImmediately(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, inputs := armForTest(t, d)

	d.handleTriggerNudge(&protocol.TriggerNudgeMessage{
		Cmd:       protocol.CmdTriggerNudge,
		SessionID: agentID,
	})

	if !wasNudged(inputs(agentID)) {
		t.Fatal("trigger_nudge did not doorbell immediately")
	}
	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("trigger_nudge left a countdown running")
	}
	if _, found, err := d.store.TicketDeliveryAttention(d.ticketAttentionKey(agentID)); err != nil || !found {
		t.Fatalf("trigger_nudge did not record attention: found=%v err=%v", found, err)
	}
}

func TestTriggerNudgeDeliversWhenUnknown(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, inputs := armForTest(t, d)
	d.store.UpdateState(agentID, protocol.StateUnknown)

	d.handleTriggerNudge(&protocol.TriggerNudgeMessage{
		Cmd:       protocol.CmdTriggerNudge,
		SessionID: agentID,
	})

	if !wasNudged(inputs(agentID)) {
		t.Fatal("trigger_nudge did not doorbell an at-rest unknown session")
	}
}

func TestTriggerNudgeDeliversWhileWorking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, inputs := armForTest(t, d)
	d.store.UpdateState(agentID, protocol.StateWorking)

	d.handleTriggerNudge(&protocol.TriggerNudgeMessage{
		Cmd:       protocol.CmdTriggerNudge,
		SessionID: agentID,
	})

	if !wasNudged(inputs(agentID)) {
		t.Fatal("trigger_nudge did not deliver on demand into a working session")
	}
}

func TestTriggerNudgeSkipsPendingApproval(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	agentID, inputs := armForTest(t, d)
	d.store.UpdateState(agentID, protocol.StatePendingApproval)

	d.handleTriggerNudge(&protocol.TriggerNudgeMessage{
		Cmd:       protocol.CmdTriggerNudge,
		SessionID: agentID,
	})

	if wasNudged(inputs(agentID)) {
		t.Fatal("trigger_nudge typed a doorbell into an approval prompt")
	}
}

func TestNudgeCountdownReArmsAfterRecentKeystroke(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	var action string
	d.nudgeFireHook = func(_, a string) { action = a }
	agentID, inputs := armForTest(t, d)

	d.noteUserInput(agentID, "", []byte("k"))
	fireNudgeNow(t, d, agentID)

	if action != "rearm" {
		t.Fatalf("fire action = %q, want rearm (keystroke guard)", action)
	}
	if wasNudged(inputs(agentID)) {
		t.Fatal("doorbell spliced onto a session the user was actively typing into")
	}
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("guard dropped the nudge instead of re-arming")
	}
}

func TestNudgeKeystrokeGuardIgnoresAutomation(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	var action string
	d.nudgeFireHook = func(_, a string) { action = a }
	agentID, inputs := armForTest(t, d)

	d.noteUserInput(agentID, "automation", []byte("k"))
	d.noteUserInput(agentID, "attach_replay", []byte("k"))
	fireNudgeNow(t, d, agentID)

	if action != "doorbell" {
		t.Fatalf("fire action = %q, want doorbell (automation must not trip the guard)", action)
	}
	if !wasNudged(inputs(agentID)) {
		t.Fatal("automation input wrongly suppressed the doorbell")
	}
}

func TestHandlePtyInputRecordsKeystrokeForGuard(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	cases := []struct {
		name   string
		source *string
		want   bool
	}{
		{"untagged genuine keystroke", nil, true},
		{"explicit empty source", protocol.Ptr(""), true},
		{"automation write", protocol.Ptr("automation"), false},
		{"attach replay write", protocol.Ptr("attach_replay"), false},
		{"padded automation is trimmed then ignored", protocol.Ptr("  automation  "), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessionID := "sess-" + tc.name
			d.handlePtyInput(nil, &protocol.PtyInputMessage{
				ID:     sessionID,
				Data:   "x",
				Source: tc.source,
			})
			if got := d.recentUserInput(sessionID, time.Hour); got != tc.want {
				t.Fatalf("recentUserInput after handlePtyInput(source=%v) = %v, want %v",
					protocol.Deref(tc.source), got, tc.want)
			}
		})
	}
}

func TestStopNudgeCountdownsClearsTimers(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	agentID, _ := armForTest(t, d)
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("precondition: no countdown armed")
	}

	d.stopNudgeCountdowns()

	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("stopNudgeCountdowns left a countdown armed")
	}
}
