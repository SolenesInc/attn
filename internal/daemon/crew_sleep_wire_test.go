package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func sleepCrew(t *testing.T, w *world, member string) *protocol.CrewSleepResult {
	t.Helper()
	slept, err := w.Client().CrewSleep(member)
	if err != nil {
		t.Fatalf("sleep %s: %v", member, err)
	}
	return slept
}

func withdrawnRestart(t *testing.T, w *world, member, requestID string) *protocol.CrewRestart {
	t.Helper()
	restart := crewRosterMember(t, w.Client(), member).Restart
	if restart == nil || restart.RequestID != requestID || restart.State != protocol.CrewRestartStateFailed || !strings.Contains(protocol.Deref(restart.Error), "sleep instead") {
		t.Fatalf("%s's restart = %+v, want %s withdrawn because the user chose sleep", member, restart, requestID)
	}
	return restart
}

func TestAskingAMemberToSleepWithdrawsItsPendingRestartAndTheDayEndsAsleep(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID))
	restartCrew(t, cli, "trellis", "then-sleep")

	if slept := sleepCrew(t, w, "trellis"); slept.AlreadyAsleep || protocol.Deref(slept.SessionID) != day.SessionID {
		t.Fatalf("sleep = %+v, want the request sent to the live day", slept)
	}
	withdrawn := withdrawnRestart(t, w, "trellis", "then-sleep")
	sessions := crewSessionCount(t, cli)
	_, err := cli.CrewRestart("trellis", "restart-after-sleep")
	crewErrorContains(t, err, "still closing after a sleep request")
	withdrawnRestart(t, w, "trellis", "then-sleep")

	handed := crewHandoff(t, cli, string(day.SessionID), "Going to sleep as asked.", false, protocol.CrewDayCloseNap)
	if protocol.Deref(handed.Outcome) != protocol.CrewDayCloseSleep || handed.SessionID != nil {
		t.Fatalf("a nap filed after the sleep request = %+v, want the day ended with nobody behind it", handed)
	}
	if after := withdrawnRestart(t, w, "trellis", "then-sleep"); protocol.Deref(after.Error) != protocol.Deref(withdrawn.Error) {
		t.Errorf("the handoff rewrote the withdrawn restart to %+v", after)
	}
	if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
		t.Fatalf("trellis is still bound to %s after the day ended", *binding)
	}
	if got := crewSessionCount(t, cli); got != sessions-1 {
		t.Fatalf("sessions = %d, want the day gone and nothing woken in its place", got)
	}

	fresh := restartCrew(t, cli, "trellis", "restart-after-sleep")
	successor := protocol.Deref(fresh.Restart.SuccessorSessionID)
	if fresh.Restart.State != protocol.CrewRestartStateCompleted || successor == "" || successor == day.SessionID {
		t.Fatalf("a restart once trellis is asleep = %+v, want it woken fresh", fresh.Restart)
	}
	w.Launched(string(successor))
}

func TestADayThatExitsAfterItsRestartWasWithdrawnStaysAsleepUntilRestarted(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	day := wakeCrew(t, cli, "alder", "")
	run := w.Launched(string(day.SessionID))
	restartCrew(t, cli, "alder", "then-sleep")
	sleepCrew(t, w, "alder")

	exitCrewDay(w, run)
	if binding := crewRosterMember(t, cli, "alder").BindingSession; binding != nil {
		t.Fatalf("the withdrawn restart woke alder into %s when its day exited", *binding)
	}
	withdrawnRestart(t, w, "alder", "then-sleep")

	restarted := restartCrew(t, cli, "alder", "after-the-exit")
	successor := protocol.Deref(restarted.Restart.SuccessorSessionID)
	if restarted.Restart.State != protocol.CrewRestartStateCompleted || successor == "" || successor == day.SessionID {
		t.Fatalf("restarting alder after its day exited = %+v, want a fresh day", restarted.Restart)
	}
	w.Launched(string(successor))
}

func TestAskingAnAsleepMemberToSleepSendsNothingAndWakesNobody(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli := w.Client()
		before := crewSessionCount(t, cli)
		if slept := sleepCrew(t, w, "trellis"); !slept.AlreadyAsleep {
			t.Fatalf("sleep=%+v", slept)
		}
		w.advance(time.Hour)
		if got := crewSessionCount(t, cli); got != before {
			t.Fatalf("sleep woke a session: before=%d after=%d", before, got)
		}
		day := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		day.reply("Ready. <!-- attn:state=idle -->")
		if items := readInbox(t, cli, day.id, 0).Items; len(items) != 0 {
			t.Fatalf("sleep queued mail=%+v", items)
		}
	})
}
