package daemon_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func restartCrew(t *testing.T, cli *client.Client, member, requestID string) *protocol.CrewRestartResult {
	t.Helper()
	result, err := cli.CrewRestart(member, requestID)
	if err != nil {
		t.Fatalf("restart %s as %s: %v", member, requestID, err)
	}
	return result
}

func appRestartCrew(app *testworld.Peer, msg protocol.CrewRestartMessage) protocol.CrewRestartResultMessage {
	app.T.Helper()
	msg.Cmd = protocol.CmdCrewRestart
	return testworld.Request(app, msg, protocol.EventCrewRestartResult, func(r protocol.CrewRestartResultMessage) bool { return r.RequestID == msg.RequestID })
}

func readCrewInbox(t *testing.T, cli *client.Client, session string) []protocol.AgentInboxItem {
	t.Helper()
	batch, err := cli.AgentInboxBatch(session, 20)
	if err != nil {
		t.Fatal(err)
	}
	return batch.Items
}

func TestACrewRestartAsksTheDayToHandOffAndCompletesOnItsSuccessor(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(day.SessionID)
	letters := crewLetters(t, w, "trellis")

	queued := restartCrew(t, cli, "trellis", "restart-1")
	if queued.Restart.State != protocol.CrewRestartStateQueued || queued.Restart.RequestID != "restart-1" || queued.Restart.SessionID != day.SessionID {
		t.Fatalf("restart = %+v, want restart-1 queued on the day", queued.Restart)
	}
	if duplicate := restartCrew(t, cli, "trellis", "restart-2"); duplicate.Restart.RequestID != "restart-1" {
		t.Fatalf("a second restart of the same day = %+v, want it coalesced onto restart-1", duplicate.Restart)
	}
	if after := crewLetters(t, w, "trellis"); !slices.Equal(after, letters) {
		t.Fatalf("asking for a restart filed letters %q; the day writes its own", after)
	}
	inbox := readCrewInbox(t, cli, day.SessionID)
	if len(inbox) != 1 || inbox[0].Content != prompts.RenderText("crew", "restart-requested", prompts.Values{}) {
		t.Fatalf("the day's inbox = %+v, want one restart request", inbox)
	}
	if state := crewRosterMember(t, cli, "trellis").Restart.State; state != protocol.CrewRestartStateRequested {
		t.Fatalf("after the day read its inbox the restart is %q, want requested", state)
	}

	handed := crewHandoff(t, cli, day.SessionID, "I wrote this letter myself.", false, "")
	successor := protocol.Deref(handed.SessionID)
	w.Launched(successor)
	completed := crewRosterMember(t, cli, "trellis")
	if completed.Restart.State != protocol.CrewRestartStateCompleted || protocol.Deref(completed.Restart.SuccessorSessionID) != successor ||
		protocol.Deref(completed.BindingSession) != successor {
		t.Fatalf("after the handoff trellis = %+v with restart %+v, want it completed on %s", completed, completed.Restart, successor)
	}

	sessions := crewSessionCount(t, cli)
	replayed := restartCrew(t, cli, "trellis", "restart-1")
	if replayed.Restart.State != protocol.CrewRestartStateCompleted || protocol.Deref(replayed.Member.BindingSession) != successor {
		t.Fatalf("replaying the completed restart = %+v, want it answered as completed", replayed)
	}
	if next := restartCrew(t, cli, "trellis", "restart-3"); next.Restart.RequestID != "restart-3" || next.Restart.SessionID != successor {
		t.Fatalf("a restart of the new day = %+v, want restart-3 on %s", next.Restart, successor)
	}
	_, err := cli.CrewRestart("trellis", "restart-2")
	crewErrorContains(t, err, "already accepted for an earlier day")
	if got := crewSessionCount(t, cli); got != sessions {
		t.Fatalf("replays changed the sessions from %d to %d", sessions, got)
	}
}

func TestACrewRestartActsOnlyOnTheDayTheUserSaw(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()

	asleep := protocol.CrewRestartMessage{Member: "alder", RequestID: "wake-alder", ExpectedSessionID: protocol.Ptr(""),
		ExpectedRevision: protocol.Ptr(crewRosterMember(t, cli, "alder").Revision)}
	woke := appRestartCrew(app, asleep)
	if !woke.Success || woke.Restart == nil || woke.Restart.State != protocol.CrewRestartStateCompleted || woke.Member == nil ||
		protocol.Deref(woke.Member.BindingSession) != protocol.Deref(woke.Restart.SuccessorSessionID) {
		t.Fatalf("restarting asleep alder = %+v, want it woken directly", woke)
	}
	firstDay := protocol.Deref(woke.Member.BindingSession)
	w.Launched(firstDay)

	for _, malformed := range []struct {
		msg  protocol.CrewRestartMessage
		want string
	}{
		{protocol.CrewRestartMessage{Member: "alder"}, "request id"},
		{protocol.CrewRestartMessage{Member: "alder", RequestID: "missing-day"}, "expected session id"},
		{protocol.CrewRestartMessage{Member: "keel", RequestID: "missing-revision", ExpectedSessionID: protocol.Ptr("")}, "expected revision"},
	} {
		if refused := appRestartCrew(app, malformed.msg); refused.Success || !strings.Contains(protocol.Deref(refused.Error), malformed.want) {
			t.Errorf("restart %+v = %+v, want it refused naming %q", malformed.msg, refused, malformed.want)
		}
	}

	actedOn := crewRosterMember(t, cli, "alder")
	restartCrew(t, cli, "alder", "newer-request")
	secondDay := protocol.Deref(crewHandoff(t, cli, firstDay, "Move to the next day.", false, "").SessionID)
	w.Launched(secondDay)
	sessions := crewSessionCount(t, cli)
	delayed := appRestartCrew(app, protocol.CrewRestartMessage{Member: "alder", RequestID: "delayed-request",
		ExpectedSessionID: protocol.Ptr(firstDay), ExpectedRevision: protocol.Ptr(actedOn.Revision)})
	if delayed.Success || !delayed.Conflict || !strings.Contains(protocol.Deref(delayed.Error), "day changed") {
		t.Fatalf("a delayed restart of the first day = %+v, want a conflict naming the changed day", delayed)
	}

	if err := cli.Unregister(secondDay); err != nil {
		t.Fatal(err)
	}
	replayedAsleep := appRestartCrew(app, asleep)
	if replayedAsleep.Success || !replayedAsleep.Conflict || replayedAsleep.Member == nil || replayedAsleep.Member.Revision == *asleep.ExpectedRevision {
		t.Fatalf("replaying the first asleep restart after another day = %+v, want a conflict carrying the newer member", replayedAsleep)
	}
	if got := crewSessionCount(t, cli); got != sessions-1 {
		t.Fatalf("stale restarts changed the sessions: %d, want %d", got, sessions-1)
	}

	read := crewRosterMember(t, cli, "keel")
	setCrew(t, cli, "keel", protocol.CrewSetMessage{Effort: protocol.Ptr("high")})
	raced := protocol.CrewRestartMessage{Member: "keel", RequestID: "raced", ExpectedSessionID: protocol.Ptr(""), ExpectedRevision: protocol.Ptr(read.Revision)}
	if conflict := appRestartCrew(app, raced); conflict.Success || !conflict.Conflict || protocol.Deref(conflict.Member.Effort) != "high" {
		t.Fatalf("a restart racing a settings edit = %+v, want a conflict carrying the edit", conflict)
	}
	if binding := crewRosterMember(t, cli, "keel").BindingSession; binding != nil {
		t.Fatalf("a conflicted restart woke keel in %s", *binding)
	}
	raced.ExpectedRevision = protocol.Ptr(crewRosterMember(t, cli, "keel").Revision)
	retried := appRestartCrew(app, raced)
	if !retried.Success || retried.Restart == nil || retried.Restart.State != protocol.CrewRestartStateCompleted {
		t.Fatalf("retrying the conflicted request against the current revision = %+v, want it to wake keel", retried)
	}
	w.Launched(protocol.Deref(retried.Restart.SuccessorSessionID))
}

func TestADayThatEndsDuringARestartIsSucceeded(t *testing.T) {
	for _, row := range []struct {
		name  string
		end   func(w *world, day *fakeagent.Run) *testworld.Peer
		after protocol.CrewRestartState
	}{
		{name: "the agent exits while the restart is queued", end: exitCrewDay, after: protocol.CrewRestartStateQueued},
		{name: "the agent exits after reading the request", end: exitCrewDay, after: protocol.CrewRestartStateRequested},
		{name: "the daemon restarts under the queued restart", after: protocol.CrewRestartStateQueued, end: func(w *world, _ *fakeagent.Run) *testworld.Peer {
			w.restart()
			return w.App()
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			w := newCrewWorld(t, fakeagent.Claude)
			cli := w.Client()
			day := wakeCrew(t, cli, "alder", "")
			run := w.Launched(day.SessionID)
			restartCrew(t, cli, "alder", "ended-day")
			if row.after == protocol.CrewRestartStateRequested {
				readCrewInbox(t, cli, day.SessionID)
			}

			app := row.end(w, run)
			alder := completedCrewRestart(app, "alder", "ended-day")
			successor := protocol.Deref(alder.BindingSession)
			if successor == "" || successor == day.SessionID || protocol.Deref(alder.Restart.SuccessorSessionID) != successor {
				t.Fatalf("after the day ended alder = %+v with restart %+v, want a fresh day completing ended-day", alder, alder.Restart)
			}
			w.Launched(successor)
		})
	}
}

func exitCrewDay(w *world, day *fakeagent.Run) *testworld.Peer {
	app := w.App()
	day.Exit(0)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == day.SessionID })
	return app
}

func completedCrewRestart(app *testworld.Peer, member, requestID string) protocol.CrewMember {
	app.T.Helper()
	completed := func(members []protocol.CrewMember) (protocol.CrewMember, bool) {
		index := slices.IndexFunc(members, func(m protocol.CrewMember) bool {
			return m.ID == member && m.Restart != nil && m.Restart.RequestID == requestID && m.Restart.State == protocol.CrewRestartStateCompleted
		})
		if index < 0 {
			return protocol.CrewMember{}, false
		}
		return members[index], true
	}
	if found, ok := completed(app.Initial.Crew); ok {
		return found
	}
	update := testworld.Await(app, protocol.EventCrewUpdated, func(e protocol.CrewUpdatedMessage) bool {
		_, ok := completed(e.Members)
		return ok
	})
	found, _ := completed(update.Members)
	return found
}

func TestAMemberWhoseDayVanishedRestartsIntoAFreshDay(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	day := wakeCrew(t, cli, "alder", "")
	run := w.Launched(day.SessionID)
	run.Prompted()
	run.Reply("Looking around. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, day.SessionID, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	w.restart()
	app = w.App()
	cli = w.Client()
	shown := crewRosterMember(t, cli, "alder")
	if shown.BindingSession != nil {
		t.Fatalf("the roster shows alder awake in the day that died with the daemon: %s", *shown.BindingSession)
	}
	restarted := appRestartCrew(app, protocol.CrewRestartMessage{Member: "alder", RequestID: "vanished", ExpectedSessionID: protocol.Ptr(""), ExpectedRevision: protocol.Ptr(shown.Revision)})
	fresh := protocol.Deref(restarted.Restart.SuccessorSessionID)
	if !restarted.Success || restarted.Restart.State != protocol.CrewRestartStateCompleted || fresh == "" || fresh == day.SessionID ||
		protocol.Deref(restarted.Member.BindingSession) != fresh {
		t.Fatalf("restarting alder = %+v, want a fresh day", restarted)
	}
	w.Launched(fresh)
}

func TestDeletingACrewDaysWorktreeResumesItsRestart(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	repo := newRepo(t, "shop")
	dayInWorktree := func(member, branch string) (string, string) {
		t.Helper()
		worktree := createWorktree(t, app, repo, branch)
		setCrew(t, cli, member, protocol.CrewSetMessage{Cwd: protocol.Ptr(worktree)})
		day := wakeCrew(t, cli, member, "")
		w.Launched(day.SessionID)
		restartCrew(t, cli, member, member+"-restart")
		return worktree, day.SessionID
	}

	worktree, day := dayInWorktree("alder", "alder-day")
	setCrew(t, cli, "alder", protocol.CrewSetMessage{Cwd: protocol.Ptr("")})
	if err := cli.DeleteWorktree(worktree, true); err != nil {
		t.Fatal(err)
	}
	alder := crewRosterMember(t, cli, "alder")
	successor := protocol.Deref(alder.BindingSession)
	if alder.Restart.State != protocol.CrewRestartStateCompleted || successor == "" || successor == day || protocol.Deref(alder.Restart.SuccessorSessionID) != successor {
		t.Fatalf("after its worktree was deleted alder = %+v with restart %+v, want a successor completing the restart", alder, alder.Restart)
	}
	w.Launched(successor)
	if list, err := cli.List(""); err != nil || slices.ContainsFunc(list.Sessions, func(s protocol.Session) bool { return s.ID == day }) {
		t.Fatalf("the day in the deleted worktree is still listed (%v)", err)
	}

	worktree, _ = dayInWorktree("keel", "keel-day")
	if err := cli.DeleteWorktree(worktree, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("the worktree is still there: %v", err)
	}
	keel := crewRosterMember(t, cli, "keel")
	if keel.Restart.State != protocol.CrewRestartStateFailed || !strings.Contains(protocol.Deref(keel.Restart.Error), worktree) || keel.BindingSession != nil {
		t.Fatalf("after the worktree keel launches in was deleted keel = %+v with restart %+v, want a failed restart naming it", keel, keel.Restart)
	}
	sessions := crewSessionCount(t, cli)
	if replay := restartCrew(t, cli, "keel", "keel-restart"); replay.Restart.State != protocol.CrewRestartStateFailed {
		t.Fatalf("replaying the failed restart = %+v, want it still failed", replay.Restart)
	}
	if got := crewSessionCount(t, cli); got != sessions {
		t.Fatalf("replaying the failed restart changed the sessions from %d to %d", sessions, got)
	}
}
