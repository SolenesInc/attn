package daemon

import (
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) importLegacyTasks() {
	if d.store == nil {
		return
	}
	imported, err := d.store.MigrateLegacyTasks(d.legacyTaskToJob)
	if err != nil {
		d.logf("jobs: hand over the retired task runner's records: %v "+
			"— nothing was moved and the old rows are intact; the next daemon start retries it", err)
		return
	}
	if imported > 0 {
		d.logf("jobs: imported %d task record(s) from the retired task runner", imported)
	}
}

func (d *Daemon) legacyTaskToJob(rec store.LegacyTaskRecord) store.JobRecord {
	job := store.JobRecord{
		ID:          rec.ID,
		Kind:        rec.Kind,
		UniqueKey:   rec.Subject,
		Payload:     "",
		State:       rec.State,
		Attempts:    rec.Attempts,
		ScheduledAt: rec.NextAttemptAt,
		LastError:   rec.LastError,
		Requeued:    rec.Requeued,
		CreatedAt:   rec.CreatedAt,
		UpdatedAt:   rec.UpdatedAt,
	}
	if job.State == "" {
		job.State = string(jobs.StateQueued)
	}
	return job
}
