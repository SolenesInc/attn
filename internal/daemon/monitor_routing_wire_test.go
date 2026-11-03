package daemon_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

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
		submitted := agent.term.Submitted()
		if len(submitted) != capacity {
			t.Fatalf("submitted %d prompts, want %d distinct accepted deliveries", len(submitted), capacity)
		}
		if submitted[0] != "Use blue for the banner" || submitted[capacity-1] != "Use blue for the banner" {
			t.Fatalf("delivered prompts %q … %q, want the annotation text", submitted[0], submitted[capacity-1])
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
