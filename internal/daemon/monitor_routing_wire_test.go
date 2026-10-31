package daemon_test

import (
	"fmt"
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
	boot := w.HoldNextBoot()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "trust"})
	w.AwaitHeldBoot()
	runs := automationRuns(t, cli, 1)
	boot()
	if len(runs) != 1 || runs[0].State != "pending" {
		t.Fatalf("automation runs while the agent boots = %+v, want one pending run", runs)
	}
	run := awaitRunState(t, w, app, runs[0].ID, func(r protocol.AutomationRunSummary) bool { return r.State != "pending" })
	if run.State != "delivered" {
		t.Fatalf("automation run = %+v, want delivered", run)
	}
	if result := awaitAutomationRunResult(app, "trust"); !result.Success {
		t.Fatalf("automation launch = %+v", result)
	}
	agent := w.Launched(string(protocol.Deref(run.SessionID)))
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

func TestAFullDeliveryHistoryRefusesNewInputAndStillAnswersRetries(t *testing.T) {
	const capacity = 65_536
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		agent := w.bubbleClaude(t, app, "shop")
		agent.term.OnSubmit(nil)
		submit := func(id string) protocol.SessionAnnotationsSubmitResultMessage {
			t.Helper()
			mark := app.EventCount()
			app.Send(protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, SessionID: protocol.SessionID(agent.id), RequestID: id, Text: "Use blue for the banner"})
			return testworld.AwaitSince(app, mark, protocol.EventSessionAnnotationsSubmitResult, func(result protocol.SessionAnnotationsSubmitResultMessage) bool { return result.RequestID == id })
		}
		for n := range capacity {
			if result := submit(fmt.Sprint(n)); !result.Success {
				t.Fatalf("delivery %d refused before the limit: %+v", n, result)
			}
		}
		result := submit("over-budget")
		if result.Success || result.Status != "error" {
			t.Fatalf("new delivery at capacity = %+v, want a visible refusal", result)
		}
		for _, detail := range []string{agent.id, fmt.Sprintf("max_placed_deliveries=%d", capacity), fmt.Sprintf("asked for %d", capacity+1)} {
			if !strings.Contains(protocol.Deref(result.Error), detail) {
				t.Errorf("refusal %q omits %q", protocol.Deref(result.Error), detail)
			}
		}
		for _, id := range []string{"0", fmt.Sprint(capacity - 1)} {
			if result := submit(id); !result.Success || result.Status != "delivered" {
				t.Fatalf("retry at capacity = %+v, want its original receipt", result)
			}
		}
		if got := len(agent.term.Submitted()); got != capacity {
			t.Fatalf("submitted %d prompts, want %d distinct accepted deliveries", got, capacity)
		}
		t.Logf("%d distinct IDs placed; new ID refused with %q; oldest and newest retries placed no extra input", capacity, protocol.Deref(result.Error))
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
