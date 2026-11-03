package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemonctl"
)

func TestClassifyPreviousEnd(t *testing.T) {
	started := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	prev := &daemonRun{ID: "run-a", PID: 4242, StartedAt: started}
	bootedBefore := started.Add(-time.Hour)
	bootedAfter := started.Add(time.Hour)
	matching := &daemonctl.StopNote{PID: 4242, Reason: "source_fingerprint_mismatch", At: started.Add(time.Minute)}
	stale := &daemonctl.StopNote{PID: 1717, Reason: "stop", At: started.Add(-time.Minute)}
	for _, tc := range []struct {
		name string
		prev *daemonRun
		note *daemonctl.StopNote
		boot time.Time
		want previousEnd
	}{
		{"no run file", nil, nil, bootedBefore, previousEnd{End: "unknown"}},
		{"no run file with a stop note", nil, matching, bootedAfter, previousEnd{End: "unknown"}},
		{"matching stop note", prev, matching, bootedBefore, previousEnd{End: "stopped", StopReason: "source_fingerprint_mismatch", Run: "run-a"}},
		{"matching stop note after a boot", prev, matching, bootedAfter, previousEnd{End: "stopped", StopReason: "source_fingerprint_mismatch", Run: "run-a"}},
		{"stale stop note", prev, stale, bootedBefore, previousEnd{End: "exited_unexpectedly", Run: "run-a"}},
		{"stale stop note after a boot", prev, stale, bootedAfter, previousEnd{End: "machine_restarted", Run: "run-a"}},
		{"boot after the previous start", prev, nil, bootedAfter, previousEnd{End: "machine_restarted", Run: "run-a"}},
		{"neither", prev, nil, bootedBefore, previousEnd{End: "exited_unexpectedly", Run: "run-a"}},
		{"boot time unreadable", prev, nil, time.Time{}, previousEnd{End: "unknown", Run: "run-a"}},
	} {
		if got := classifyPreviousEnd(tc.prev, tc.note, tc.boot); got != tc.want {
			t.Errorf("%s: classifyPreviousEnd = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
