package daemon_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheTaskListShowsTheNewestUpdatedTaskFirstWithinASecond(t *testing.T) {
	inBubbleAnsweringHeadlessTasks(t, []fakeagent.Harness{fakeagent.Claude}, func(t *testing.T, w *world) {
		w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) { task.Answer("Name the checkout work") })
		app := w.App()
		var agents []*bubbleClaude
		for _, directory := range []string{"first", "second", "third", "fourth"} {
			agents = append(agents, w.bubbleClaude(t, app, directory))
		}
		for i, step := range []time.Duration{0, 123400 * time.Microsecond, 50 * time.Microsecond, 376550 * time.Microsecond} {
			w.advance(step)
			app.TypeLine(agents[i].id, "add a discount field")
			awaitTitleTask(app, agents[i].id, func(task protocol.Task) bool { return task.State == "done" })
		}

		requestID := uuid.NewString()
		listed := testworld.Request(app, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr(requestID)},
			protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == requestID })
		var subjects []string
		for _, task := range listed.Tasks {
			if task.Kind == "session_title" {
				subjects = append(subjects, task.Subject)
			}
		}
		want := []string{agents[3].id, agents[2].id, agents[1].id, agents[0].id}
		if !slices.Equal(subjects, want) {
			t.Errorf("title tasks = %v, want newest-updated first %v", subjects, want)
		}
	})
}
