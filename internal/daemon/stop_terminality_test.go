package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestStopIsNonTerminal(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		crons    int
		relax    bool
		want     bool
	}{
		{
			name:     "background work still running",
			statuses: []string{"running"},
			want:     true,
		},
		{
			name:  "parked on a scheduled wakeup: the turn still ended",
			crons: 1,
			want:  false,
		},
		{
			name:     "both outstanding",
			statuses: []string{"running"},
			crons:    1,
			want:     true,
		},
		{
			name:     "a finished background task is not outstanding work",
			statuses: []string{"completed"},
			want:     false,
		},
		{
			name:     "mixed statuses count as running if any is",
			statuses: []string{"completed", "running"},
			want:     true,
		},
		{
			name:     "status casing is the harness's, not ours",
			statuses: []string{"Running"},
			want:     true,
		},
		{
			name: "nothing outstanding: the turn ended",
			want: false,
		},
		{
			name:     "chief relax: its background work does not defer the end of the turn",
			statuses: []string{"running"},
			relax:    true,
			want:     false,
		},
		{
			name:     "chief relax: a parked schedule does not defer it either",
			statuses: []string{"running"},
			crons:    1,
			relax:    true,
			want:     false,
		},
		{
			name:  "chief relax: cron only, still a real end of turn",
			crons: 1,
			relax: true,
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &protocol.StopMessage{
				Cmd:             protocol.CmdStop,
				ID:              "sess",
				BackgroundTasks: tasksWithStatuses(tc.statuses...),
			}
			if tc.crons > 0 {
				msg.PendingSessionCrons = protocol.Ptr(tc.crons)
			}
			if got := stopIsNonTerminal(msg, tc.relax); got != tc.want {
				t.Fatalf("stopIsNonTerminal() = %v, want %v", got, tc.want)
			}
		})
	}
}

func tasksWithStatuses(statuses ...string) []protocol.StopBackgroundTask {
	tasks := make([]protocol.StopBackgroundTask, 0, len(statuses))
	for _, status := range statuses {
		tasks = append(tasks, protocol.StopBackgroundTask{Type: "background_session", Status: status})
	}
	return tasks
}
