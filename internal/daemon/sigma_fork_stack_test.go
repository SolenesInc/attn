package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASigmaForkFollowedJustBeforeACrashLinksOnlyItsOwnPullRequests(t *testing.T) {
	stack := testworld.NewStack(t, testworld.WithAgents(fakeagent.Sigma))
	stack.Start()
	app := stack.App()
	setSetting(t, app, "sigma_enabled", "true")
	first := stack.Spawn(app, fakeagent.Sigma, stack.Path("shop"))
	tui := stack.Launched(first)
	original := tui.ConversationID
	server := stack.AppServer(fakeagent.Sigma)
	terminal := app.Terminal(first)
	promptUntilWorking(app, first, tui, server, "review checkout tests")
	server.ToolCall(original, fakeagent.ServerToolCall{Name: "bash", Input: map[string]any{"command": "gh pr create --fill"}, Output: shopPull(7) + "\n"})
	server.Reply(original, "Checkout ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, first, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && slices.Contains(pullNumbers(s), 7)
	})
	mark := app.EventCount()
	app.TypeLine(first, "review delivery tests")
	tui.Prompted()
	server.Prompted(original)
	testworld.AwaitSessionSince(app, mark, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	mark = app.EventCount()
	server.Reply(original, "Delivery ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSessionSince(app, mark, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	stack.Stop()

	stack.StartCrashingAt("succession-committed")
	app = stack.App()
	typeInto(app, terminal, "/fork\r")
	tui.Prompted()
	forks := server.Forks(original)
	if len(forks) != 1 {
		t.Fatalf("the /fork made %v, want one fork", forks)
	}
	branch := forks[0]
	typeInto(app, terminal, "review shipping tests instead\r")
	tui.Prompted()
	server.Prompted(branch)
	stack.AwaitCrash()

	stack.Start()
	app = stack.App()
	successor, found := successorIn(app.Initial.Sessions, first)
	if !found {
		successor = awaitSuccessor(app, first)
	}
	mark = app.EventCount()
	server.ToolCall(branch, fakeagent.ServerToolCall{Name: "bash", Input: map[string]any{"command": "gh pr create --fill"}, Output: shopPull(8) + "\n"})
	server.Reply(branch, "Shipping ready. <!-- attn:state=waiting_input -->")
	forked := testworld.AwaitSessionSince(app, mark, string(successor.ID), func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && slices.Contains(pullNumbers(s), 8)
	})
	if got := pullNumbers(forked); !slices.Equal(got, []int{8}) {
		t.Errorf("the fork's session links pull requests %v, want only the one opened after the fork (8)", got)
	}
}
