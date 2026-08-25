package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/jobs"
)

func newTestJobStore(t *testing.T, d *Daemon) jobs.Store {
	t.Helper()
	return &sqlJobStore{store: d.store, lockDir: t.TempDir(), log: func(string, ...any) {}}
}

func jobIDForKey(t *testing.T, runner *jobs.Runner, kind, key string) string {
	t.Helper()
	job, err := runner.GetByKey(kind, key)
	if err != nil {
		t.Fatalf("look up job %s/%s: %v", kind, key, err)
	}
	if job == nil {
		t.Fatalf("no job for %s/%s", kind, key)
	}
	return job.ID
}
