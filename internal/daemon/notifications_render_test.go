package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/jobs"
)

func TestTaskFailureRenderersDescribeEachTrigger(t *testing.T) {
	d := &Daemon{}
	task := &jobs.Job{ID: "job-1", UniqueKey: "session-1", LastError: "safe cause"}

	tests := []struct {
		name        string
		render      taskFailureRenderer
		wantActions []string
	}{
		{"session activity", d.renderSessionActivityFailure, []string{notificationActionOpenSession, notificationActionRetryTask}},
		{"session title", d.renderSessionTitleFailure, []string{notificationActionOpenSession, notificationActionRetryTask}},
		{"snooze wake", d.renderSnoozeWakeFailure, []string{notificationActionOpenSession, notificationActionRetryTask}},
		{"Garden review", d.renderGardenReviewFailure, []string{notificationActionRetryTask}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.render(task)
			if got.Title == "" || got.Trigger == "" || got.Impact == "" || got.Cause != "safe cause" {
				t.Fatalf("notification = %+v", got)
			}
			if len(got.Actions) != len(test.wantActions) {
				t.Fatalf("actions = %+v, want %v", got.Actions, test.wantActions)
			}
			for i, want := range test.wantActions {
				if got.Actions[i].Kind != want {
					t.Fatalf("action %d = %q, want %q", i, got.Actions[i].Kind, want)
				}
			}
		})
	}
}
