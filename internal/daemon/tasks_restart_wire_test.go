package daemon_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestATaskThatFailedForGoodIsStillListedWithItsCauseAfterARestart(t *testing.T) {
	inBubbleAnsweringHeadlessTasks(t, []fakeagent.Harness{fakeagent.Claude}, func(t *testing.T, w *world) {
		w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
			task.Fail("API Error: 529 overloaded")
		})
		w.Spawn(w.App(), fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
			m.InitialPrompt = protocol.Ptr("the login form rejects valid passwords")
		})
		w.advance(0)
		w.advance(time.Minute)
		w.advance(2 * time.Minute)
		before := deadTasks(w.App())
		if len(before) != 1 || protocol.Deref(before[0].LastError) == "" {
			t.Fatalf("dead tasks = %+v, want the failed title task with its cause", before)
		}

		w.restart()
		after := deadTasks(w.App())
		if len(after) != 1 || after[0].ID != before[0].ID || after[0].Attempts != before[0].Attempts ||
			protocol.Deref(after[0].LastError) != protocol.Deref(before[0].LastError) || after[0].Subject != before[0].Subject {
			t.Errorf("after a restart the dead tasks are %+v, want %+v unchanged", after, before)
		}
	})
}

func TestPeriodicDutiesStayOutOfTheTaskListAndNeverTellTheAppTheyRan(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "idle")
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		w.advance(0)
		shown := len(tasksChangedOf(app))
		w.advance(time.Hour + time.Minute)
		if again := len(tasksChangedOf(app)); again != shown {
			t.Errorf("an idle hour of periodic duties told the app %d times that tasks changed", again-shown)
		}
		if listed := listTasks(app); len(listed) != 0 {
			t.Errorf("after an idle hour the task list is %+v, want it empty", listed)
		}
	})
}

func listTasks(app *testworld.Peer) []protocol.Task {
	app.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr(requestID)},
		protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == requestID }).Tasks
}

func deadTasks(app *testworld.Peer) []protocol.Task {
	app.T.Helper()
	var dead []protocol.Task
	for _, task := range listTasks(app) {
		if task.State == "dead" {
			dead = append(dead, task)
		}
	}
	return dead
}

func tasksChangedOf(app *testworld.Peer) []protocol.WebSocketEvent {
	var changed []protocol.WebSocketEvent
	for _, event := range app.Received() {
		if event.Event == protocol.EventTasksChanged {
			changed = append(changed, event)
		}
	}
	return changed
}
