package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestATurnOpensAtAWaitSurvivesTheWorkAndOnlySettleClosesIt(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, watcher, cli := w.App(), w.App(), w.Client()
		if err := cli.Register("s1", "s1", w.Path("s1")); err != nil {
			t.Fatalf("register: %v", err)
		}
		w.advance(0)
		report := func(state protocol.SessionState, send func() error) protocol.Session {
			t.Helper()
			if err := send(); err != nil {
				t.Fatalf("report %s: %v", state, err)
			}
			w.advance(0)
			shown := sessionStateLastShown(t, app)
			if shown.State != state {
				t.Fatalf("the app shows %s, want %s", shown.State, state)
			}
			return shown
		}
		working := func() error { return cli.UpdateState("s1", protocol.StateWorking) }
		waiting := func() error { return cli.UpdateState("s1", protocol.StateWaitingInput) }
		settle := func() error {
			app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: "s1"})
			return nil
		}

		if launching := sessionStateLastShown(t, app); protocol.Deref(launching.TurnOwed) {
			t.Fatal("a launching session owes a turn")
		}
		if protocol.Deref(report(protocol.SessionStateWorking, working).TurnOwed) {
			t.Fatal("a session at work owes a turn")
		}
		if err := cli.SendStop("s1", "", client.StopFacts{PendingSessionCrons: 1}); err != nil {
			t.Fatalf("stop: %v", err)
		}
		finished := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
		if !protocol.Deref(finished.TurnOwed) || !turnOpenedAt(t, finished).Equal(stateSince(t, finished)) {
			t.Fatalf("a finished run shows owed=%v opened at %q, want the turn opened as it finished at %s",
				protocol.Deref(finished.TurnOwed), protocol.Deref(finished.TurnOpenedAt), finished.StateSince)
		}

		for _, next := range []struct {
			state protocol.SessionState
			send  func() error
		}{{protocol.SessionStateWorking, working}, {protocol.SessionStateWaitingInput, waiting}} {
			w.advance(time.Second)
			if shown := report(next.state, next.send); !protocol.Deref(shown.TurnOwed) || shown.TurnOpenedAt == nil || *shown.TurnOpenedAt != *finished.TurnOpenedAt {
				t.Fatalf("going %s left the turn owed=%v opened at %q, want it still owed since %q",
					next.state, protocol.Deref(shown.TurnOwed), protocol.Deref(shown.TurnOpenedAt), *finished.TurnOpenedAt)
			}
		}

		if shown := report(protocol.SessionStateWaitingInput, settle); shown.TurnOwed != nil {
			t.Fatal("settle left the turn owed")
		}
		testworld.AwaitStateAfter(watcher, finished, func(s protocol.Session) bool {
			return s.State == protocol.SessionStateWaitingInput && s.TurnOwed == nil
		})
		if protocol.Deref(report(protocol.SessionStateWorking, working).TurnOwed) {
			t.Fatal("going back to work reopened the settled turn")
		}

		w.advance(time.Second)
		if !protocol.Deref(report(protocol.SessionStateWaitingInput, waiting).TurnOwed) {
			t.Fatal("the next wait opened no turn")
		}
		report(protocol.SessionStateWorking, working)
		if protocol.Deref(report(protocol.SessionStateWorking, settle).TurnOwed) {
			t.Fatal("settling while the agent works left the turn owed")
		}
		w.advance(time.Second)
		asking := report(protocol.SessionStatePendingApproval, func() error {
			return cli.RecordNotification("s1", "permission_prompt", "Allow edit?")
		})
		if !protocol.Deref(asking.TurnOwed) {
			t.Fatal("an approval request after a settle opened no turn")
		}
	})
}

func TestSessionsTheQueueSkipsNeverOweATurn(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	queued := w.Spawn(app, fakeagent.Claude, w.Path("api"))
	w.Launched(queued)
	testworld.AwaitSession(app, queued, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateIdle && protocol.Deref(s.TurnOwed)
	})

	shell := w.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), w.Path("scripts"))
	chief := w.Spawn(app, fakeagent.Claude, w.Path("office"))
	w.Launched(chief)
	chiefResult := testworld.Request(app, protocol.SetChiefOfStaffMessage{Cmd: protocol.CmdSetChiefOfStaff, SessionID: chief, ChiefOfStaff: true},
		protocol.EventChiefOfStaffResult, func(r protocol.ChiefOfStaffResultMessage) bool { return r.SessionID == chief })
	if chiefResult.Error != nil {
		t.Fatalf("make %s the chief of staff: %s", chief, *chiefResult.Error)
	}
	w.Launched(chief)
	madeChief := len(sessionUpdatesOf(app, chief))
	pinned := w.Spawn(app, fakeagent.Claude, w.Path("pinned"))
	w.Launched(pinned)
	muted := w.Spawn(app, fakeagent.Claude, w.Path("muted"))
	w.Launched(muted)
	app.Send(protocol.PinWorkspaceMessage{Cmd: protocol.CmdPinWorkspace, WorkspaceID: "workspace-pinned", Pinned: true})
	app.Send(protocol.MuteWorkspaceMessage{Cmd: protocol.CmdMuteWorkspace, WorkspaceID: "workspace-muted"})
	testworld.Await(app, protocol.EventWorkspaceStateChanged, func(e protocol.WorkspaceStateChangedMessage) bool {
		return e.Workspace.ID == "workspace-pinned" && e.Workspace.Pinned
	})
	testworld.Await(app, protocol.EventWorkspaceStateChanged, func(e protocol.WorkspaceStateChangedMessage) bool {
		return e.Workspace.ID == "workspace-muted" && e.Workspace.Muted
	})
	for _, id := range []string{pinned, muted, chief} {
		testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	}
	testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	for id, name := range map[string]string{shell: "shell", chief: "chief of staff", pinned: "session in a pinned workspace", muted: "session in a muted workspace"} {
		got := queriedSession(t, cli, id)
		if got.State != protocol.SessionStateIdle || got.TurnOwed != nil {
			t.Errorf("the %s is %s with turn owed %v, want idle at its prompt owing nothing", name, got.State, protocol.Deref(got.TurnOwed))
		}
		if id == chief && !protocol.Deref(got.ChiefOfStaff) {
			t.Errorf("the chief of staff is not shown as the chief")
		}
	}
	app.TypeLine(shell, "exit")
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == shell })
	for id, since := range map[string]int{shell: 0, chief: madeChief} {
		for _, s := range sessionUpdatesOf(app, id)[since:] {
			if protocol.Deref(s.TurnOwed) {
				t.Fatalf("the app was shown %s owing a turn while %s", id, s.State)
			}
		}
	}
}
