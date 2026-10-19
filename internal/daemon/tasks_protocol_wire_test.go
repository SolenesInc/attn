package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestATaskTransitionReachesTheAppAndItsListingNeverCarriesThePayload(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)
	watcher := w.App()
	app.TypeLine(session, "add a discount field")
	agent.Prompted()
	task := w.HeadlessTask()
	testworld.Await[protocol.TasksChangedMessage](watcher, protocol.EventTasksChanged, nil)

	listed := testworld.Request(watcher, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr("tasks")},
		protocol.EventTaskListResult, func(r map[string]any) bool { return r["request_id"] == "tasks" })
	tasks, _ := listed["tasks"].([]any)
	var title map[string]any
	for _, task := range tasks {
		if fields, ok := task.(map[string]any); ok && fields["kind"] == "session_title" {
			title = fields
		}
	}
	if title == nil || title["subject"] != session || title["attempts"] == nil {
		t.Fatalf("the title task = %v, want its subject and attempts", title)
	}
	for _, field := range []string{"id", "state", "created_at", "updated_at", "next_attempt_at"} {
		if value, ok := title[field].(string); !ok || value == "" {
			t.Errorf("the title task has %s %v, want a non-empty string", field, title[field])
		}
	}
	for _, private := range []string{"payload", "result"} {
		if _, leaked := title[private]; leaked {
			t.Errorf("the task list carries the task's %s: %v", private, title)
		}
	}
	task.Answer("Add discount field")
	awaitTitleTask(app, session, func(task protocol.Task) bool { return task.State == "done" })
}
