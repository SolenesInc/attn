package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestATaskTransitionReachesTheAppAndItsListingNeverCarriesThePayload(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)
	app.TypeLine(session, "add a discount field")
	agent.Prompted()
	agent.Reply("Before or after tax? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	watcher := w.App()

	watcher.Send(protocol.SnoozeTurnMessage{Cmd: protocol.CmdSnoozeTurn, SessionID: session, Until: time.Now().Add(time.Hour).Format(time.RFC3339Nano)})
	testworld.Await[protocol.TasksChangedMessage](watcher, protocol.EventTasksChanged, nil)
	testworld.AwaitSession(watcher, session, func(s protocol.Session) bool { return protocol.Deref(s.TurnSnoozedUntil) != "" })

	listed := testworld.Request(watcher, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr("tasks")},
		protocol.EventTaskListResult, func(r map[string]any) bool { return r["request_id"] == "tasks" })
	tasks, _ := listed["tasks"].([]any)
	var wake map[string]any
	for _, task := range tasks {
		if fields, ok := task.(map[string]any); ok && fields["kind"] == "session_snooze_wake" {
			wake = fields
		}
	}
	if wake == nil || wake["subject"] != session || wake["attempts"] == nil {
		t.Fatalf("the snooze's wake task = %v, want its subject and attempts", wake)
	}
	for _, field := range []string{"id", "state", "created_at", "updated_at", "next_attempt_at"} {
		if value, ok := wake[field].(string); !ok || value == "" {
			t.Errorf("the snooze's wake task has %s %v, want a non-empty string", field, wake[field])
		}
	}
	for _, private := range []string{"payload", "result"} {
		if _, leaked := wake[private]; leaked {
			t.Errorf("the task list carries the task's %s: %v", private, wake)
		}
	}
}
