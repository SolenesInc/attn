package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAReconciliationJudgesTheEndedConversationAndNotesItsVerdict(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session := reconcilingSession(t, w, app, "migrate-store", "Move the store onto the new backend.")
	task := w.HeadlessTask()
	if task.Harness != fakeagent.Claude || task.Model != "haiku" {
		t.Errorf("the reconciliation ran %s %q, want Claude haiku", task.Harness, task.Model)
	}
	for _, want := range []string{"migrate-store", "Move the store onto the new backend.", "The store moved; the e2e spec never ran."} {
		if !strings.Contains(task.Prompt, want) {
			t.Errorf("the reconciliation prompt lacks %q:\n%s", want, task.Prompt)
		}
	}
	if strings.Contains(task.Prompt, ".jsonl") {
		t.Errorf("the reconciliation prompt points at the transcript instead of carrying it:\n%s", task.Prompt)
	}
	task.Answer(`{"assessment":"partial","confidence":"medium","whats_left":"e2e spec never ran","evidence":"last turn: tests pass except e2e"}`)
	awaitReconcileTask(app, "migrate-store", func(task protocol.Task) bool { return task.State == "done" })

	ticket := showTicket(t, cli, "migrate-store")
	notes := ticketReconciliationNotes(ticket)
	if len(notes) != 1 {
		t.Fatalf("the ticket holds reconciliation notes %q, want one", notes)
	}
	for _, want := range []string{"session " + session, "Assessment: partial (confidence: medium)", "What's left: e2e spec never ran", "Evidence: last turn: tests pass except e2e"} {
		if !strings.Contains(notes[0], want) {
			t.Errorf("the reconciliation note lacks %q:\n%s", want, notes[0])
		}
	}
	if ticket.Status != protocol.TicketStatusWorking {
		t.Errorf("the verdict moved the ticket to %s, want it left working", ticket.Status)
	}
}

func TestAVerdictIsDroppedWhenTheTicketMovedWhileItWasJudged(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	reconcilingSession(t, w, app, "migrate-store", "Move the store onto the new backend.")
	task := w.HeadlessTask()
	reportTicket(t, cli, "you", "migrate-store", protocol.DispatchWorkStateCompleted, "finished it by hand")
	task.Answer(`{"assessment":"partial","confidence":"high","whats_left":"e2e spec never ran","evidence":"last turn"}`)
	awaitReconcileTask(app, "migrate-store", func(task protocol.Task) bool { return task.State == "done" })
	if notes := ticketReconciliationNotes(showTicket(t, cli, "migrate-store")); len(notes) != 0 {
		t.Errorf("a verdict judged against the old column landed: %q", notes)
	}
}

func TestAFailedReconciliationLeavesOneBoundedDiagnosticNoteAndKeepsTheColumn(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	reconcilingSession(t, w, app, "migrate-store", "Move the store onto the new backend.")
	output := "MCP server needs authentication " + strings.Repeat("x", 3000) + " model not found"
	w.HeadlessTask().Fail(output)
	awaitReconcileTask(app, "migrate-store", func(task protocol.Task) bool { return task.State == "done" })

	ticket := showTicket(t, cli, "migrate-store")
	notes := ticketReconciliationNotes(ticket)
	if len(notes) != 1 {
		t.Fatalf("the ticket holds reconciliation notes %q, want one", notes)
	}
	for _, want := range []string{"could not determine", "classifier run failed", "MCP server needs authentication", "model not found", "…(truncated)"} {
		if !strings.Contains(notes[0], want) {
			t.Errorf("the failure note lacks %q:\n%s", want, notes[0])
		}
	}
	if len(notes[0]) > 2000 || strings.Contains(notes[0], strings.Repeat("x", 1000)) {
		t.Errorf("the failure note carries %d bytes, want the model's output cut to a bounded excerpt", len(notes[0]))
	}
	if ticket.Status != protocol.TicketStatusWorking {
		t.Errorf("the failed reconciliation moved the ticket to %s, want it left working", ticket.Status)
	}
}

func reconcilingSession(t *testing.T, w *world, app *testworld.Peer, ticket, brief string) string {
	t.Helper()
	cli := w.Client()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.Label = protocol.Ptr("store") })
	agent := w.Launched(session)
	if _, err := cli.CreateTicket("planner", "Migrate the store", brief, ticket); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.TakeTicket(session, ticket, false); err != nil {
		t.Fatal(err)
	}
	inboxLines(t, cli, session)
	reportTicket(t, cli, session, ticket, protocol.DispatchWorkStateInProgress, "")
	app.TypeLine(session, "migrate the store")
	agent.Prompted()
	replied := time.Now()
	agent.Reply("The store moved; the e2e spec never ran. <!-- attn:state=idle -->")
	answerTurnVerdict(t, w, "DONE")
	awaitIdleSince(t, app, session, replied)
	agent.Exit(0)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
	return session
}

func awaitReconcileTask(app *testworld.Peer, ticket string, match func(protocol.Task) bool) {
	app.T.Helper()
	for {
		requestID := uuid.NewString()
		listed := testworld.Request(app, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr(requestID)},
			protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == requestID })
		for _, task := range listed.Tasks {
			if task.Kind == "reconcile" && task.Subject == ticket && match(task) {
				return
			}
		}
		testworld.Await[protocol.TasksChangedMessage](app, protocol.EventTasksChanged, nil)
	}
}
