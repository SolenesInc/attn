package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestOpeningAMembersOldDayWhileItIsAwakeElsewhereIsRefused(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	old := crewDayLeftRecoverable(t, w, "alder")
	app, cli := w.App(), w.Client()
	current := wakeCrew(t, cli, "alder", "")
	w.Launched(string(current.SessionID))

	revived := attachRevive(app, string(old), 80, 24)
	if revived.Success || !strings.Contains(protocol.Deref(revived.Error), string(current.SessionID)[:8]) {
		t.Fatalf("opening alder's old day %s answered %+v, want a refusal naming the live day %s", old, revived, current.SessionID)
	}
	_, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: old})
	crewErrorContains(t, err, "Alder is awake in session "+string(current.SessionID)[:8])

	if got := protocol.Deref(crewRosterMember(t, cli, "alder").BindingSession); got != current.SessionID {
		t.Fatalf("after the refusals alder is bound to %q, want %s", got, current.SessionID)
	}
	if running := crewRunningSessions(t, cli); len(running) != 1 || running[0] != current.SessionID {
		t.Fatalf("running sessions = %v, want only alder's live day %s", running, current.SessionID)
	}
}

func TestOpeningAMembersOldDayWhileItIsAsleepMakesThatDayTheMember(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	old := crewDayLeftRecoverable(t, w, "alder")
	app, cli := w.App(), w.Client()

	revived := attachRevive(app, string(old), 80, 24)
	if !revived.Success || !protocol.Deref(revived.Revived) {
		t.Fatalf("opening alder's old day answered %+v, want it revived", revived)
	}
	run := w.Launched(string(old))
	if instructions := crewLaunchFlag(run.Argv, "--append-system-prompt"); !strings.Contains(instructions, "You are **Alder**") {
		t.Errorf("the revived day launched without alder's priming:\n%s", instructions)
	}
	if got := protocol.Deref(crewRosterMember(t, cli, "alder").BindingSession); got != old {
		t.Fatalf("after the revive alder is bound to %q, want %s", got, old)
	}
	woken := wakeCrew(t, cli, "alder", "")
	if !woken.AlreadyAwake || woken.SessionID != old {
		t.Fatalf("wake after the revive = %+v, want alder already awake in %s", woken, old)
	}
}

func TestAWakeDuringARevivedDaysLaunchFindsThatDay(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	old := crewDayLeftRecoverable(t, w, "alder")
	app, cli := w.App(), w.Client()

	starting := w.PauseAt(pausepoint.TerminalStarting)
	revived := make(chan protocol.AttachResultMessage, 1)
	go func() { revived <- attachRevive(app, string(old), 80, 24) }()
	starting.Await()
	woke := make(chan *protocol.CrewWakeResult, 1)
	failed := make(chan error, 1)
	go func() {
		woken, err := cli.CrewWake("alder", "", "")
		if err != nil {
			failed <- err
			return
		}
		woke <- woken
	}()
	starting.Release()

	if result := <-revived; !result.Success {
		t.Fatalf("the revive answered %+v", result)
	}
	select {
	case err := <-failed:
		t.Fatalf("wake during the revive: %v", err)
	case woken := <-woke:
		if !woken.AlreadyAwake || woken.SessionID != old {
			t.Fatalf("wake during the revive = %+v, want alder already awake in %s", woken, old)
		}
	}
	w.Launched(string(old))
	if running := crewRunningSessions(t, cli); len(running) != 1 || running[0] != old {
		t.Fatalf("running sessions = %v, want only the revived day %s", running, old)
	}
}

func TestARetiredMembersOldDayStaysClosed(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	old := crewDayLeftRecoverable(t, w, "alder")
	app, cli := w.App(), w.Client()
	retireCrew(t, cli, "alder")

	revived := attachRevive(app, string(old), 80, 24)
	if revived.Success || !strings.Contains(protocol.Deref(revived.Error), "Alder is retired") {
		t.Fatalf("opening a retired member's day answered %+v, want the retirement refusal", revived)
	}
	if running := crewRunningSessions(t, cli); len(running) != 0 {
		t.Fatalf("running sessions = %v, want none", running)
	}
}

func TestMailForAMemberResumesItsInterruptedDayRatherThanAFreshOne(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	sender := spawnPanes(w, app, w.Path("sender"))[0].session
	day := wakeCrew(t, cli, "alder", "")
	run := w.Launched(string(day.SessionID))
	run.Prompted()
	run.Reply("Morning. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, string(day.SessionID), func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	app.TypeLine(string(day.SessionID), "reprice the catalog")
	run.Prompted()
	testworld.AwaitSession(app, string(day.SessionID), func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	sent := sendAgentMessage(t, cli, sender, "alder", "the catalog changed")
	if sent.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("mail mid-turn = %+v, want queued", sent)
	}

	w.restartHoldingRevives()
	cli = w.Client()
	resumed := w.Launched(string(day.SessionID))
	if !resumed.Resumed || resumed.ConversationID != run.ConversationID {
		t.Fatalf("alder's mail launched %q, want it resuming conversation %s", resumed.Argv, run.ConversationID)
	}
	if got := resumed.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Errorf("the resumed day was prompted with %q, want the inbox doorbell", got)
	}
	if got := inboxContents(readInbox(t, cli, string(day.SessionID), 0).Items); got != "the catalog changed" {
		t.Errorf("the resumed day reads %q, want the mail queued before the restart", got)
	}
	if bound := protocol.Deref(crewRosterMember(t, cli, "alder").BindingSession); bound != day.SessionID {
		t.Errorf("alder is bound to %q, want its interrupted day %s", bound, day.SessionID)
	}
}

func TestMailForAMemberWhoseLatestDayEndedStartsFreshAndLeavesAnOlderDayAlone(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	old := crewDayLeftRecoverable(t, w, "alder")
	app, cli := w.App(), w.Client()
	sender := spawnPanes(w, app, w.Path("sender"))[0].session
	current := wakeCrew(t, cli, "alder", "")
	run := w.Launched(string(current.SessionID))
	run.Prompted()
	run.Exit(0)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.SessionID == current.SessionID })

	sendAgentMessage(t, cli, sender, "alder", "the catalog changed")
	bound := crewRosterMember(t, cli, "alder").BindingSession
	if bound == nil || *bound == old || *bound == current.SessionID {
		t.Fatalf("after the mail alder is bound to %v, want a fresh day, not %s or %s", bound, old, current.SessionID)
	}
	if fresh := w.Launched(string(*bound)); fresh.Resumed {
		t.Errorf("the fresh day launched as %q, want no resumed conversation", fresh.Argv)
	}
	if state := queriedSession(t, cli, string(old)).State; state != protocol.SessionStateRecoverable {
		t.Errorf("alder's older day is %s, want it still recoverable", state)
	}
}

func crewDayLeftRecoverable(t *testing.T, w *world, member string) protocol.SessionID {
	t.Helper()
	app := w.App()
	day := wakeCrew(t, w.Client(), member, "")
	run := w.Launched(string(day.SessionID))
	run.Prompted()
	run.Reply("Working. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, string(day.SessionID), func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	w.restartHoldingRevives()
	for _, s := range w.App().Initial.Sessions {
		if s.ID == day.SessionID && s.State == protocol.SessionStateRecoverable {
			return day.SessionID
		}
	}
	t.Fatalf("after the restart %s's day %s is not recoverable", member, day.SessionID)
	return ""
}

func crewRunningSessions(t *testing.T, cli *client.Client) []protocol.SessionID {
	t.Helper()
	list, err := cli.List("")
	if err != nil {
		t.Fatal(err)
	}
	var running []protocol.SessionID
	for _, s := range list.Sessions {
		if s.State != protocol.SessionStateRecoverable {
			running = append(running, s.ID)
		}
	}
	return running
}

func TestTheEndOfASleepingDaysRunLeavesItsResumedRunAwake(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	day := wakeCrew(t, cli, "alder", "")
	run := w.Launched(string(day.SessionID))
	run.Prompted()
	run.Reply("A day worth keeping. <!-- attn:state=idle -->")

	ending := w.PauseAt(pausepoint.PtyExitOwnerResolved)
	crewHandoff(t, cli, string(day.SessionID), "Resume this conversation later.", false)
	ending.Await()

	starting := w.PauseAt(pausepoint.TerminalStarting)
	reopened := make(chan error, 1)
	go func() {
		_, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: day.SessionID})
		reopened <- err
	}()
	starting.Await()

	ended := w.PauseAt(pausepoint.PtyExitHandled)
	ending.Release()
	ended.Await()
	ended.Release()
	starting.Release()
	if err := <-reopened; err != nil {
		t.Fatalf("reopen while the earlier run was ending: %v", err)
	}
	w.Launched(string(day.SessionID))

	woken := wakeCrew(t, cli, "alder", "")
	if !woken.AlreadyAwake || woken.SessionID != day.SessionID {
		t.Fatalf("wake after the resume = %+v, want alder already awake in %s", woken, day.SessionID)
	}
	if running := crewRunningSessions(t, cli); len(running) != 1 || running[0] != day.SessionID {
		t.Fatalf("running sessions = %v, want only the resumed day %s", running, day.SessionID)
	}
}
