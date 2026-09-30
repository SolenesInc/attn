package testworld

import "github.com/victorarias/attn/internal/protocol"

func AwaitTaskDone(app *Peer, kind string) {
	app.T.Helper()
	for {
		listed := Request(app, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr("await-task")},
			protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == "await-task" })
		if !listed.Success {
			app.T.Fatalf("task list: %s", protocol.Deref(listed.Error))
		}
		for _, task := range listed.Tasks {
			if task.Kind != kind {
				continue
			}
			if task.State == "done" {
				return
			}
			if task.State == "failed" || task.State == "dead" {
				app.T.Fatalf("task %s %s: %s", kind, task.State, protocol.Deref(task.LastError))
			}
		}
		Await[protocol.TasksChangedMessage](app, protocol.EventTasksChanged, nil)
	}
}
