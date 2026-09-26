package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
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

func TestNudgeCountdownHandsRecentKeystrokeRetryToMailbox(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	t.Cleanup(d.stopAgentMailboxDoorbells)
	var action string
	d.nudgeFireHook = func(_, a string) { action = a }
	agentID, inputs := armForTest(t, d)

	d.noteUserInput(agentID, "", []byte("k"))
	fireNudgeNow(t, d, agentID)

	if action != "queued" {
		t.Fatalf("fire action = %q, want queued (mailbox owns the retry)", action)
	}
	if wasNudged(inputs(agentID)) {
		t.Fatal("doorbell spliced onto a session the user was actively typing into")
	}
	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("legacy nudge countdown retained a retry after durable enqueue")
	}
	d.agentMailboxMu.Lock()
	mailbox := d.agentMailboxDoorbells[agentID]
	d.agentMailboxMu.Unlock()
	if mailbox == nil || !mailbox.unread || mailbox.retry == nil {
		t.Fatal("mailbox did not retain the unread item and its quiet-window retry")
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
		{"mouse report tagged by the app", protocol.Ptr("pointer"), false},
		{"terminal query reply forwarded by the app", protocol.Ptr("response"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessionID := "sess-" + tc.name
			d.handlePtyInput(nil, &protocol.PtyInputMessage{
				ID:     sessionID,
				Data:   "x",
				Source: tc.source,
			})
			if got := d.userInputQuietRemaining(sessionID, time.Hour) > 0; got != tc.want {
				t.Fatalf("userInputQuietRemaining after handlePtyInput(source=%v) > 0 = %v, want %v",
					protocol.Deref(tc.source), got, tc.want)
			}
		})
	}
}

func TestLegacyNudgeHeldOffByTypingWakesAfterQuiet(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	t.Cleanup(d.stopAgentMailboxDoorbells)
	agentID, inputs := armForTest(t, d)
	quiesceTranscriptWatchers(t, d)
	synctest.Test(t, func(t *testing.T) {
		defer d.stopAgentMailboxDoorbells()
		if err := d.writeSessionPTY(agentID, []byte("half written"), "user"); err != nil {
			t.Fatalf("user input: %v", err)
		}
		d.handleTriggerNudge(&protocol.TriggerNudgeMessage{
			Cmd:       protocol.CmdTriggerNudge,
			SessionID: agentID,
		})
		if wasNudged(inputs(agentID)) {
			t.Fatalf("typed into a composer the user just used: %q", inputs(agentID))
		}

		time.Sleep(sessionInputQuietWindow)
		synctest.Wait()
		if !wasNudged(inputs(agentID)) {
			t.Fatalf("mailbox did not wake once the composer went quiet: %q", inputs(agentID))
		}
	})
}
