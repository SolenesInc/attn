package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

func snoozeUntil(d *Daemon, id string, until time.Time) {
	d.handleSnoozeTurn(&protocol.SnoozeTurnMessage{
		SessionID: id,
		Until:     until.Format(time.RFC3339Nano),
	})
}

func snoozedUntil(t *testing.T, d *Daemon, id string) string {
	t.Helper()
	session := d.sessionForBroadcast(d.store.Get(id))
	if session == nil {
		t.Fatalf("session %s not found", id)
	}
	return protocol.Deref(session.TurnSnoozedUntil)
}

func TestSnoozeSuppressesTurnsUntilItsDeadline(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	if !owed(t, d, "s1") {
		t.Fatal("setup: a waiting session owes no turn")
	}

	snoozeUntil(d, "s1", time.Now().Add(time.Hour))
	if owed(t, d, "s1") {
		t.Fatal("the session still owes a turn immediately after snoozing")
	}
	if snoozedUntil(t, d, "s1") == "" {
		t.Fatal("no deadline on the wire, so the row has nothing to park under")
	}

	for _, state := range []string{
		protocol.StateWorking,
		protocol.StateWaitingInput,
		protocol.StatePendingApproval,
		protocol.StateIdle,
	} {
		moveTo(d, "s1", state)
		if owed(t, d, "s1") {
			t.Fatalf("state %s opened a turn on a snoozed session", state)
		}
	}
}

func TestWakeOpensTheTurnAtTheWakeInstant(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	original := protocol.Deref(d.sessionForBroadcast(d.store.Get("s1")).TurnOpenedAt)

	snoozeUntil(d, "s1", time.Now().Add(time.Hour))
	moveTo(d, "s1", protocol.StateIdle)

	wakeAt := time.Now().Add(time.Hour)
	d.wakeSnooze("s1", wakeAt, "test")

	if !owed(t, d, "s1") {
		t.Fatal("the woken session owes no turn although it is sitting idle")
	}
	opened := protocol.Deref(d.sessionForBroadcast(d.store.Get("s1")).TurnOpenedAt)
	if opened == original {
		t.Error("the turn kept its pre-snooze age, so it wakes to the head of the queue")
	}
	parsed, err := time.Parse(time.RFC3339Nano, opened)
	if err != nil {
		t.Fatalf("turn_opened_at %q does not parse: %v", opened, err)
	}
	if !parsed.Equal(wakeAt.UTC()) {
		t.Errorf("turn opened at %s, want the wake instant %s", parsed, wakeAt.UTC())
	}
	if snoozedUntil(t, d, "s1") != "" {
		t.Error("the deadline survived the wake")
	}
}

func TestWakeOpensNoTurnWhileTheAgentIsWorking(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWorking)
	snoozeUntil(d, "s1", time.Now().Add(time.Hour))
	d.wakeSnooze("s1", time.Now(), "test")

	if owed(t, d, "s1") {
		t.Fatal("waking a working agent opened a turn")
	}
	moveTo(d, "s1", protocol.StateWaitingInput)
	if !owed(t, d, "s1") {
		t.Error("the session stayed suppressed after the snooze was cleared")
	}
}

func TestBreakThroughStatesEndTheSnooze(t *testing.T) {
	tests := []struct {
		name   string
		state  string
		reason sessionstate.Reason
		breaks bool
	}{
		{"stuck", protocol.StateUnknown, sessionstate.ReasonStuck, true},
		{"process exited", protocol.StateIdle, sessionstate.ReasonProcessExited, true},
		{"an ordinary finished run", protocol.StateIdle, sessionstate.ReasonClassifierVerdict, false},
		{"a question", protocol.StateWaitingInput, sessionstate.ReasonQuestionOpen, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTurnDaemon(t)
			addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

			moveTo(d, "s1", protocol.StateWorking)
			snoozeUntil(d, "s1", time.Now().Add(time.Hour))

			d.recordStateReason("s1", sessionstate.Resolution{
				State:  protocol.SessionState(tt.state),
				Reason: tt.reason,
			})
			moveTo(d, "s1", tt.state)

			if got := owed(t, d, "s1"); got != tt.breaks {
				t.Errorf("owed = %v, want %v", got, tt.breaks)
			}
			stillSnoozed := snoozedUntil(t, d, "s1") != ""
			if stillSnoozed == tt.breaks {
				t.Errorf("snooze live = %v after a state that breaks=%v; a break-through must consume the deferral",
					stillSnoozed, tt.breaks)
			}
		})
	}
}

func TestSnoozingAWorkingAgentSuppressesTheTurnItWouldOpen(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWorking)
	if owed(t, d, "s1") {
		t.Fatal("setup: a working session owes a turn")
	}
	snoozeUntil(d, "s1", time.Now().Add(time.Hour))

	moveTo(d, "s1", protocol.StateIdle)
	if owed(t, d, "s1") {
		t.Error("the finished run opened a turn although the agent was deferred")
	}
}

func TestSnoozeWakesAfterARestart(t *testing.T) {
	d := newTurnDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

		moveTo(d, "s1", protocol.StateIdle)
		now := time.Now()
		if !d.store.SnoozeTurn("s1", now.Add(time.Minute), now) {
			t.Fatal("setup: the snooze was not stored")
		}
		time.Sleep(time.Hour)
		if owed(t, d, "s1") {
			t.Fatal("setup: the session is not deferred")
		}

		woken := make(chan string, 1)
		d.snoozeWakeHook = func(sessionID string) { woken <- sessionID }
		d.rescheduleSnoozeWakes()

		synctest.Wait()
		select {
		case <-woken:
		default:
			t.Fatal("the lapsed snooze never woke after the reschedule")
		}
		if !owed(t, d, "s1") {
			t.Error("the woken session owes no turn although it is sitting idle")
		}
	})
}

func TestSnoozeTimerFiresOnItsDeadline(t *testing.T) {
	d := newTurnDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

		moveTo(d, "s1", protocol.StateWaitingInput)
		woken := make(chan string, 1)
		d.snoozeWakeHook = func(sessionID string) { woken <- sessionID }

		snoozeUntil(d, "s1", time.Now().Add(time.Hour))
		if owed(t, d, "s1") {
			t.Fatal("the session still owes a turn immediately after snoozing")
		}

		// The real deadline length: the armed AfterFunc fires when the bubble's
		// clock reaches it, not when a test-sized window happens to elapse.
		time.Sleep(time.Hour)
		synctest.Wait()
		select {
		case <-woken:
		default:
			t.Fatal("the snooze timer never fired")
		}
		if !owed(t, d, "s1") {
			t.Error("the session owes no turn after its snooze elapsed")
		}
	})
}

func TestResnoozingReplacesThePendingWake(t *testing.T) {
	d := newTurnDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
		moveTo(d, "s1", protocol.StateWaitingInput)

		snoozeUntil(d, "s1", time.Now().Add(time.Minute))
		snoozeUntil(d, "s1", time.Now().Add(time.Hour))

		time.Sleep(30 * time.Minute)
		synctest.Wait()
		if owed(t, d, "s1") {
			t.Error("the superseded timer woke a session that had been re-snoozed for an hour")
		}
		if snoozedUntil(t, d, "s1") == "" {
			t.Error("the superseded timer cleared the live deadline")
		}
	})
}

// The window the timer's identity check cannot cover: the wake proved it was current and
// released the lock before the second snooze reached the store, so the clear reads it.
func TestAResnoozeInsideAFiringWakeKeepsTheLaterDeadline(t *testing.T) {
	d := newTurnDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
		moveTo(d, "s1", protocol.StateWaitingInput)

		later := time.Now().Add(time.Hour)
		var once bool
		d.snoozeWakeGapHook = func(sessionID string) {
			if once {
				return // the replacement's own wake, an hour from now, must not recurse
			}
			once = true
			snoozeUntil(d, sessionID, later)
		}
		woken := make(chan string, 1)
		d.snoozeWakeHook = func(sessionID string) { woken <- sessionID }

		snoozeUntil(d, "s1", time.Now().Add(time.Minute))
		time.Sleep(time.Minute)
		synctest.Wait()
		select {
		case <-woken:
		default:
			t.Fatal("the first snooze's timer never fired")
		}

		if owed(t, d, "s1") {
			t.Error("the expired timer cashed a promise the user had already replaced")
		}
		if got := snoozedUntil(t, d, "s1"); got != later.UTC().Format(time.RFC3339Nano) {
			t.Errorf("live deadline = %q, want the re-snooze's %q", got, later.UTC().Format(time.RFC3339Nano))
		}
		d.snoozeMu.Lock()
		pending, ok := d.snoozeTimers["s1"]
		d.snoozeMu.Unlock()
		if !ok {
			t.Fatal("the expired timer took the replacement's wake with it; the agent would never return")
		}
		if !pending.firesAt.Equal(later) {
			t.Errorf("pending wake fires at %s, want the re-snooze's %s", pending.firesAt, later)
		}
	})
}

func TestSnoozeCancelsAPendingAutoSettle(t *testing.T) {
	d := newTurnDaemon(t)
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "5")
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")

	moveTo(d, "s1", protocol.StateWaitingInput)
	creditUserInputForNextWorking(t, d, "s1")
	moveTo(d, "s1", protocol.StateWorking)

	d.autoSettleMu.Lock()
	pending := len(d.autoSettleTimers)
	d.autoSettleMu.Unlock()
	if pending == 0 {
		t.Fatal("setup: no auto-settle armed")
	}

	snoozeUntil(d, "s1", time.Now().Add(time.Hour))

	d.autoSettleMu.Lock()
	pending = len(d.autoSettleTimers)
	d.autoSettleMu.Unlock()
	if pending != 0 {
		t.Error("the auto-settle timer survived the snooze")
	}
}

func TestALapsedDeadlineIsNotBroadcast(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "s1", protocol.StateWorking)

	d.store.SnoozeTurn("s1", time.Now().Add(-time.Minute), time.Now())
	if got := snoozedUntil(t, d, "s1"); got != "" {
		t.Errorf("turn_snoozed_until = %q for a deadline already past, want empty", got)
	}
}

func TestSnoozeRejectsAnUnparseableDeadline(t *testing.T) {
	d := newTurnDaemon(t)
	addTurnSession(t, d, "s1", protocol.SessionAgentCodex, "ws1")
	moveTo(d, "s1", protocol.StateWaitingInput)

	d.handleSnoozeTurn(&protocol.SnoozeTurnMessage{SessionID: "s1", Until: "next tuesday"})

	if !owed(t, d, "s1") {
		t.Error("a malformed deadline settled the turn anyway")
	}
	if snoozedUntil(t, d, "s1") != "" {
		t.Error("a malformed deadline was stored")
	}
}
