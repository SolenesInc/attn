package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestANewChatsSubmitSurvivesThePreviousClassifiersLateIdle(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Vars = append(s.Vars, "ATTN_HEADLESS_TASKS=on")
	observed := s.PauseAt(pausepoint.ConversationObserved)
	s.Start()
	app := s.App()
	first := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	agent := s.Launched(first)
	app.TypeLine(first, "find the flaky checkout test")
	agent.Prompted()
	s.HeadlessTask().Fail("provider unavailable")
	agent.Reply("Found it. <!-- attn:state=idle -->")
	verdict := s.HeadlessTask()
	app.TypeLine(first, "/new")
	agent.Prompted()
	app.TypeLine(first, "fix cart discounts")
	observed.Await()
	mark := len(app.Log())
	verdict.Answer(`{"verdict":"DONE"}`)
	testworld.AwaitSessionSince(app, mark, first, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	observed.Release()
	agent.Prompted()
	next := awaitSuccessor(app, first)
	title := s.HeadlessTask()
	if !strings.Contains(title.Prompt, "fix cart discounts") {
		t.Fatalf("title asks %q, want the new chat's prompt", title.Prompt)
	}
	title.Answer("Fix cart discounts")
	awaitLabel(app, string(next.ID), "Fix cart discounts")
}
