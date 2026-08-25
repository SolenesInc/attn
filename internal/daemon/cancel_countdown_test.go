package daemon

import (
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestCancelCountdown_StopsBothCountdownsOnOneSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	t.Cleanup(d.stopAutoSettleTimers)
	// Windows long enough that the real timers never race the hand-fires below.
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "3600")
	d.store.SetSetting(SettingAutoSettleCountdownSeconds, "3600")

	agentID, _ := armForTest(t, d)
	if currentNudgeTimer(d, agentID) == nil {
		t.Fatal("precondition: no nudge countdown armed")
	}
	if !d.store.OpenTurnIfClosed(agentID, time.Now()) {
		t.Fatal("OpenTurnIfClosed() = false; the fixture owes no turn")
	}
	creditUserInputForNextWorking(t, d, agentID)
	if !d.applyState(sessionStateChange{sessionID: agentID, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, agentID)
	if _, ok := autoSettlePending(d, agentID); !ok {
		t.Fatal("precondition: no auto-settle countdown armed")
	}

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: agentID})

	if _, ok := autoSettlePending(d, agentID); ok {
		t.Fatal("auto-settle countdown survived the cancel")
	}
	if currentNudgeTimer(d, agentID) != nil {
		t.Fatal("nudge countdown survived the cancel")
	}
}

func TestCancelCountdown_NoCountdownIsHarmless(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.nudgeWindowOverride = time.Hour
	t.Cleanup(d.stopNudgeCountdowns)
	_, agentID, inputs := delegateForNotify(t, d, "codex")
	d.store.UpdateState(agentID, protocol.StateIdle)

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: agentID})

	if wasNudged(inputs(agentID)) {
		t.Fatal("a cancel doorbelled the session")
	}
	if session := d.store.Get(agentID); session == nil || string(session.State) != protocol.StateIdle {
		t.Fatal("a cancel changed session state")
	}
}

func TestCancelCountdown_NudgeStaysCancelledAcrossSelectionChange(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		chiefID, agentID, _ := delegateForNotify(t, d, "codex")
		ticketID := boundTicketID(t, d, agentID)
		d.store.UpdateState(agentID, protocol.StateIdle)
		commentOnTicket(t, d, ticketID, "take a look at the failing test")
		if currentNudgeTimer(d, agentID) == nil {
			t.Fatal("precondition: no nudge countdown armed")
		}

		d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: agentID})

		d.setSelectedSession(agentID)
		d.setSelectedSession(chiefID)
		// synctest.Wait returns once the resume goroutine has finished and every other bubble
		// goroutine is parked, so "no timer" is about the settled daemon, not a 50ms window.
		synctest.Wait()

		if currentNudgeTimer(d, agentID) != nil {
			t.Fatal("the cancelled nudge re-armed on a selection change")
		}
		clone := d.sessionForBroadcast(d.store.Get(agentID))
		if !protocol.Deref(clone.TicketUnread) {
			t.Fatal("cancelling the countdown also cleared the unread indicator")
		}
	})
}

func TestCancelCountdown_NewerTicketActivityReArmsTheNudge(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		_, agentID, _ := delegateForNotify(t, d, "codex")
		ticketID := boundTicketID(t, d, agentID)
		d.store.UpdateState(agentID, protocol.StateIdle)
		commentOnTicket(t, d, ticketID, "take a look at the failing test")
		if currentNudgeTimer(d, agentID) == nil {
			t.Fatal("precondition: no nudge countdown armed")
		}

		d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: agentID})
		if currentNudgeTimer(d, agentID) != nil {
			t.Fatal("precondition: cancel did not stop the countdown")
		}

		commentOnTicket(t, d, ticketID, "actually, this is now blocking the release")

		settledNudgeDeadline(t, d, agentID)
	})
}

func TestCancelCountdown_KeepsTheTurnOwed(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: id})

	if !turnIsOwed(d, id) {
		t.Fatal("the cancel settled the turn it was supposed to keep")
	}
}
