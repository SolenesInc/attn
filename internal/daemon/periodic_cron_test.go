package daemon

import (
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/jobs"
)

func TestStartJobQueueArmsThePeriodicTicks(t *testing.T) {
	d := newBubbleDaemon(t)
	notebookRoot := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		d.store.SetSetting(SettingNotebookRoot, notebookRoot)
		d.startJobQueue()
		runner := d.jobQueueRef()
		t.Cleanup(runner.Stop)
		synctest.Wait()

		for _, kind := range []string{automationScheduleKind, crewLifecycleKind, sessionPullRequestRefreshKind} {
			entry, err := runner.CronEntry(kind)
			if err != nil {
				t.Fatalf("cron entry for %s: %v", kind, err)
			}
			if entry == nil {
				t.Fatalf("%s is not armed", kind)
			}
			if entry.State != jobs.StateQueued {
				t.Fatalf("%s entry state = %s, want queued", kind, entry.State)
			}
		}

		list, err := runner.List()
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, job := range list {
			switch job.Kind {
			case automationScheduleKind, crewLifecycleKind, sessionPullRequestRefreshKind:
				t.Fatalf("the work list included the %s heartbeat: %+v", job.Kind, job)
			}
		}
	})
}
