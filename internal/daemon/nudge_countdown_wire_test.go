package daemon_test

import (
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	nudgeCountdownWindow = 30 * time.Second
	nudgeBundleWindow    = 10 * time.Minute
)

func TestTheNudgeCountdownRunsOnlyWhileTheSessionIsUnseenAndCanTakeIt(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "author", "worker", "other")
		createTicket(t, cli, "worker", "fix the build", "fix-build")
		selectForNudge := func(id string) {
			app.Send(protocol.SessionSelectedMessage{Cmd: protocol.CmdSessionSelected, ID: id})
			w.advance(0)
		}
		awaitNudge := func(match func(s protocol.Session) bool) protocol.Session {
			t.Helper()
			return testworld.AwaitSession(app, "worker", match)
		}
		firesAt := func(s protocol.Session) time.Time {
			at, _ := time.Parse(time.RFC3339Nano, protocol.Deref(s.NudgeFiresAt))
			return at
		}

		selectForNudge("worker")
		commentOnTicket(t, cli, "author", "fix-build", "take a look")
		if seen := awaitNudge(func(s protocol.Session) bool { return protocol.Deref(s.TicketUnread) }); protocol.Deref(seen.NudgeFiresAt) != "" {
			t.Errorf("the selected session armed a countdown to %s", protocol.Deref(seen.NudgeFiresAt))
		}

		selectForNudge("other")
		want := time.Now().Add(nudgeCountdownWindow)
		awaitNudge(func(s protocol.Session) bool { return firesAt(s).Equal(want) })
		selectForNudge("worker")
		awaitNudge(func(s protocol.Session) bool { return protocol.Deref(s.NudgeFiresAt) == "" })
		selectForNudge("other")
		awaitNudge(func(s protocol.Session) bool { return !firesAt(s).IsZero() })

		if err := cli.UpdateState("worker", protocol.StateWorking); err != nil {
			t.Fatalf("worker reports working: %v", err)
		}
		if working := awaitNudge(func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking }); firesAt(working).IsZero() {
			t.Error("the countdown was cancelled when the agent started working")
		}
		if err := cli.UpdateState("worker", protocol.StateWaitingInput); err != nil {
			t.Fatalf("worker reports waiting_input: %v", err)
		}
		awaitNudge(func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

		if _, err := cli.TicketInbox("worker"); err != nil {
			t.Fatalf("worker reads its ticket inbox: %v", err)
		}
		read := time.Now()
		awaitNudge(func(s protocol.Session) bool {
			return !protocol.Deref(s.TicketUnread) && protocol.Deref(s.NudgeFiresAt) == ""
		})

		selectForNudge("worker")
		w.advance(time.Minute)
		commentOnTicket(t, cli, "author", "fix-build", "one more thing")
		w.advance(0)
		if held := queriedSession(t, cli, "worker"); !protocol.Deref(held.TicketUnread) || protocol.Deref(held.NudgeFiresAt) != "" {
			t.Errorf("the selected worker has unread %v and a countdown to %q, want unread activity and no countdown",
				protocol.Deref(held.TicketUnread), protocol.Deref(held.NudgeFiresAt))
		}
		selectForNudge("other")
		bundled := read.Add(nudgeBundleWindow)
		awaitNudge(func(s protocol.Session) bool { return firesAt(s).Equal(bundled) })

		if err := cli.RecordNotification("worker", "permission_prompt", "Allow edit?"); err != nil {
			t.Fatalf("worker asks for approval: %v", err)
		}
		approval := awaitNudge(func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
		if protocol.Deref(approval.NudgeFiresAt) != "" {
			t.Errorf("the countdown to %s survived the approval prompt", protocol.Deref(approval.NudgeFiresAt))
		}
	})
}

func TestAnUnreadTicketNudgeIsReArmedAfterARestart(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	author := w.Spawn(app, fakeagent.Claude, w.Path("author"))
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	run := w.Launched(session)
	app.TypeLine(session, "fix the build")
	run.Prompted()
	run.Reply("Fixed. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	createTicket(t, cli, session, "fix the build", "fix-build")
	commentOnTicket(t, cli, author, "fix-build", "take a look")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.NudgeFiresAt) != "" })

	w.restart()
	sessions := w.App().Initial.Sessions
	i := slices.IndexFunc(sessions, func(s protocol.Session) bool { return s.ID == session })
	if i < 0 {
		t.Fatal("the session did not come back after a restart")
	}
	if back := sessions[i]; !protocol.Deref(back.TicketUnread) || protocol.Deref(back.NudgeFiresAt) == "" {
		t.Errorf("after a restart the session has unread %v and a countdown to %q, want its unread activity counting down again",
			protocol.Deref(back.TicketUnread), protocol.Deref(back.NudgeFiresAt))
	}
}
