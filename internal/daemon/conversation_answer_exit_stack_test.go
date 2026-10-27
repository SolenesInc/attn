package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnExitBeforeASwitchAnswerEndsTheCommittedConversation(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	answering := s.PauseAt(pausepoint.ConversationSwitchAnswer)
	exiting := s.PauseAt(pausepoint.PtyExitHandled)
	s.Start()
	app := s.App()
	first := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	agent := s.Launched(first)
	app.TypeLine(first, "before the switch")
	agent.Prompted()
	original := agent.ConversationID
	agent.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, first, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	terminal := app.Terminal(first)
	app.TypeLine(first, "/clear")
	agent.Prompted()
	answering.Await()
	next := awaitSuccessor(app, first)
	conversation := agent.ConversationID
	if conversation == original {
		t.Fatal("clear did not start a new conversation")
	}
	agent.Exit(1)
	exiting.Await()
	exiting.Release()
	answering.Release()
	exit := testworld.Await(app, protocol.EventSessionExited, func(x protocol.SessionExitedMessage) bool {
		return string(x.ID) == terminal
	})
	if exit.SessionID != next.ID || exit.ExitCode != 1 {
		t.Fatalf("terminal exit = %+v, want session %s with code 1", exit, next.ID)
	}
	current := queriedSession(t, s.Client(), string(next.ID))
	if current.TerminalExit == nil || current.TerminalExit.Code != 1 {
		t.Fatalf("committed session lost its exit: %+v", current)
	}
	if reason := protocol.Deref(current.StateReason); reason != "process_exited" {
		t.Fatalf("committed session state=%s reason=%q, want its process exit", current.State, reason)
	}
	closed := showSession(t, s.Client(), first)
	if protocol.Deref(closed.ClosedAt) == "" {
		t.Fatal("the predecessor reopened when the committed conversation exited")
	}
	reloadRespawned(t, app, string(next.ID))
	resumed := s.Launched(string(next.ID))
	if resumed.ConversationID != conversation {
		t.Fatalf("reload resumed %s, want committed conversation %s", resumed.ConversationID, conversation)
	}
}
