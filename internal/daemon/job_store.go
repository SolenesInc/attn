package daemon

import (
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) newSQLJobStore() *store.JobStore {
	lockDir := d.dataRoot
	if lockDir == "" {
		lockDir = config.DataDir()
	}
	return store.NewJobStore(d.store, lockDir, d.logf)
}

func jobSubject(job *jobs.Job) string {
	if job == nil {
		return ""
	}
	return job.UniqueKey
}
