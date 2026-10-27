package daemon_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAutomationAcceptsTheCodexDirectoryTrustPromptWithEnter(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}
	applyAutomation(t, cli, manualAutomation(w, "Check the code."))
	awaitAutomationChanged(app, 1)
	w.RequireEnterAtNextBoot("Do you trust the contents of this directory?")
	result := testworld.Request(app, protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "trust"}, protocol.EventAutomationRunResult, automationAnswer[protocol.AutomationRunResultMessage]("trust"))
	if !result.Success {
		t.Fatalf("automation launch = %+v", result)
	}
	runs := automationRuns(t, cli, 1)
	if len(runs) != 1 || runs[0].State != "delivered" {
		t.Fatalf("automation runs = %+v, want one delivered run", runs)
	}
	agent := w.Launched(string(protocol.Deref(runs[0].SessionID)))
	if prompt := agent.Prompted(); !strings.Contains(prompt, "Check the code.") {
		t.Fatalf("automation prompt = %q", prompt)
	}
}

func TestRepeatingAnAnnotationDeliveryIDPastesOnceAndANewIDPastesAgain(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		agent := w.bubbleClaude(t, app, "shop")
		agent.term.OnSubmit(nil)
		submit := func(id string) {
			t.Helper()
			result := testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, SessionID: protocol.SessionID(agent.id), RequestID: id, Text: "Use blue for the banner"}, protocol.EventSessionAnnotationsSubmitResult, func(result protocol.SessionAnnotationsSubmitResultMessage) bool { return result.RequestID == id })
			if !result.Success || result.Status != "delivered" {
				t.Fatalf("submit = %+v", result)
			}
		}
		submit("first")
		submit("first")
		if got := agent.term.Submitted(); !slices.Equal(got, []string{"Use blue for the banner"}) {
			t.Fatalf("repeated delivery submitted %q", got)
		}
		submit("second")
		if got := agent.term.Submitted(); !slices.Equal(got, []string{"Use blue for the banner", "Use blue for the banner"}) {
			t.Fatalf("new delivery submitted %q", got)
		}
	})
}

func TestAFailedTitleRemainsOnItsConversationAfterANewChat(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Codex)
	app := w.App()
	first := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	agent := w.Launched(first)
	app.TypeLine(first, "find the flaky checkout test")
	agent.Prompted()
	w.HeadlessTask().Fail("provider unavailable")
	failed := awaitTitleTask(app, first, func(task protocol.Task) bool { return task.State == "failed" })
	mark := len(app.Log())
	agent.Reply("Found it. <!-- attn:state=idle -->")
	answerTurnVerdict(t, w, "DONE")
	testworld.AwaitSessionSince(app, mark, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	app.TypeLine(first, "/new")
	agent.Prompted()
	app.TypeLine(first, "fix cart discounts")
	agent.Prompted()
	next := awaitSuccessor(app, first)
	w.HeadlessTask().Answer("Fix cart discounts")
	awaitLabel(app, string(next.ID), "Fix cart discounts")
	tasks := listTasks(app)
	for _, task := range tasks {
		if task.ID == failed.ID {
			if task.Subject != first || task.State != "failed" {
				t.Fatalf("previous conversation's failed title = %+v", task)
			}
			return
		}
	}
	t.Fatalf("previous conversation's failed title %s disappeared from %+v", failed.ID, tasks)
}
