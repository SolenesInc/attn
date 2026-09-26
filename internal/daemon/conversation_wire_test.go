package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func respawn(w *world, app *testworld.Peer, agent fakeagent.Harness, session, cwd string) *fakeagent.Run {
	w.T.Helper()
	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: session})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
	w.Spawn(app, agent, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = session
		m.ResumeSessionID = protocol.Ptr(session)
	})
	return w.Launched(session)
}

func TestARespawnResumesTheConversationClaudeStartedWithClear(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Claude, cwd)
	first := w.Launched(session)
	app.TypeLine(session, "add a discount field")
	first.Prompted()
	first.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	launched := first.ConversationID
	app.TypeLine(session, "/clear")
	if got := first.Prompted(); got != "/clear" {
		t.Fatalf("claude received %q", got)
	}
	cleared := first.ConversationID
	if cleared == launched {
		t.Fatalf("/clear kept claude in conversation %s", launched)
	}

	resumed := respawn(w, app, fakeagent.Claude, session, cwd)
	if !resumed.Resumed || resumed.ConversationID != cleared {
		t.Fatalf("respawn ran claude %q; want it to resume %s, the conversation /clear started", resumed.Argv, cleared)
	}
}

func TestASessionLaunchedToResumeAConversationKeepsResumingIt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	cwd := w.Path("api")
	earlier := w.Spawn(app, fakeagent.Codex, cwd)
	conversation := w.Launched(earlier).ConversationID
	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: earlier})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == earlier })

	session := w.Spawn(app, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr(conversation)
	})
	if launched := w.Launched(session); !launched.Resumed || launched.ConversationID != conversation {
		t.Fatalf("a launch resuming %s ran codex %q", conversation, launched.Argv)
	}
	if resumed := respawn(w, app, fakeagent.Codex, session, cwd); !resumed.Resumed || resumed.ConversationID != conversation {
		t.Fatalf("respawn ran codex %q, want it to resume %s", resumed.Argv, conversation)
	}
}
