package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEveryTerminalComesBackWhenItsSharedHostDies(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex), testworld.OnPTYBackend(testworld.SharedHostPTY))
	s.Start()
	app := s.App()
	setSetting(t, app, "pty_shared_host_enabled", "false")
	onWorker := s.Spawn(app, fakeagent.Claude, s.Path("worker"))
	workerRun := lostRuntimeAgentAtRest(s, app, onWorker)
	s.EnableSharedHost(app)
	shop := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	api := s.Spawn(app, fakeagent.Codex, s.Path("api"))
	agents := map[string]*fakeagent.Run{shop: lostRuntimeAgentAtRest(s, app, shop), api: lostRuntimeAgentAtRest(s, app, api)}
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shell"))
	shopTerminal := protocol.TerminalID(app.Terminal(shop))
	shellTerminal := protocol.TerminalID(app.Terminal(shell))
	app.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: shopTerminal, Cols: 101, Rows: 37})
	testworld.Await(app, protocol.EventPtyResized, func(m protocol.PtyResizedMessage) bool { return m.ID == shopTerminal && m.Cols == 101 })
	mark := len(app.Log())

	s.KillRuntimeOf(string(shopTerminal))

	for session, first := range agents {
		revived := s.Launched(session)
		if !revived.Resumed || revived.ConversationID != first.ConversationID {
			t.Errorf("session %s came back as %q, want it resuming conversation %s", session, revived.Argv, first.ConversationID)
		}
		app.TypeLine(session, "still there?")
		if prompt := revived.Prompted(); prompt != "still there?" {
			t.Errorf("session %s was prompted %q, want what was typed", session, prompt)
		}
		revived.Reply("Here. <!-- attn:state=idle -->")
		testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	}
	testworld.AwaitSince(app, mark, protocol.EventRuntimeRespawned, func(m protocol.RuntimeRespawnedMessage) bool { return m.ID == shellTerminal })
	attached := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: shopTerminal},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == shopTerminal })
	if protocol.Deref(attached.Cols) != 101 || protocol.Deref(attached.Rows) != 37 {
		t.Errorf("session %s came back at %dx%d, want the 101x37 its pane asked for", shop, protocol.Deref(attached.Cols), protocol.Deref(attached.Rows))
	}
	app.TypeLine(onWorker, "worker still there?")
	if prompt := workerRun.Prompted(); prompt != "worker still there?" {
		t.Errorf("the agent on a worker was prompted %q, want it untouched by the host's death", prompt)
	}
	for _, event := range app.Received() {
		if event.Event == protocol.EventSessionExited {
			t.Errorf("the app was told terminal %s exited, want it brought back", protocol.Deref(event.ID))
		}
	}
}

func lostRuntimeAgentAtRest(s *testworld.Stack, app *testworld.Peer, session string) *fakeagent.Run {
	s.T.Helper()
	run := s.Launched(session)
	app.TypeLine(session, "add a discount field")
	run.Prompted()
	run.Reply("Done. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	return run
}
