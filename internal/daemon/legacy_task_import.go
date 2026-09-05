package daemon

import (
	"encoding/json"
	"strings"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

// One-time handover from the retired task runner's `tasks` table to the job queue. The
// legacy meta keys are pinned literally: they describe a format nothing writes anymore.
const legacyMetaReconcileInputs = "reconcile_inputs"

// importLegacyTasks runs before the queue is constructed, so nothing races it, and the
// move is atomic in the store: any failure leaves every old row for the next start.
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

// legacyTaskToJob always returns a record: the legacy id is preserved as the job id, and
// untranslatable meta yields a payload-less job rather than a dropped row.
func (d *Daemon) legacyTaskToJob(rec store.LegacyTaskRecord) store.JobRecord {
	payload, err := legacyTaskPayload(rec)
	if err != nil {
		d.logf("jobs: import legacy task %s (%s): %v", rec.ID, rec.Kind, err)
	}
	job := store.JobRecord{
		ID:          rec.ID,
		Kind:        rec.Kind,
		UniqueKey:   rec.Subject,
		Payload:     payload,
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

func legacyTaskPayload(rec store.LegacyTaskRecord) (string, error) {
	raw := strings.TrimSpace(rec.MetaJSON)
	if raw == "" || raw == "null" {
		return "", nil
	}
	var meta map[string]string
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return "", err
	}
	if rec.Kind == reconcileKind {
		return meta[legacyMetaReconcileInputs], nil
	}
	return "", nil
}
