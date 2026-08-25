package daemon

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

var _ jobs.Store = (*sqlJobStore)(nil)

type sqlJobStore struct {
	store   *store.Store
	lockDir string
	log     jobs.LogFunc
}

func (d *Daemon) newSQLJobStore() *sqlJobStore {
	lockDir := d.dataRoot
	if lockDir == "" {
		lockDir = config.DataDir()
	}
	return &sqlJobStore{store: d.store, lockDir: lockDir, log: d.logf}
}

func jobSubject(job *jobs.Job) string {
	if job == nil {
		return ""
	}
	return job.UniqueKey
}

// Init is a no-op: migration {86} creates the jobs table when the DB opens.
func (a *sqlJobStore) Init() error { return nil }

func (a *sqlJobStore) AcquireLock() (string, error) { return jobs.AcquireDirLock(a.lockDir, a.log) }
func (a *sqlJobStore) ReleaseLock(token string)     { jobs.ReleaseDirLock(token, a.log) }

func (a *sqlJobStore) RecoverOrphans(now time.Time) (int, error) {
	return a.store.RecoverRunningJobs(now)
}

func (a *sqlJobStore) Load(id string) (*jobs.Job, error) {
	rec, ok, err := a.store.GetJob(id)
	if err != nil || !ok {
		return nil, err
	}
	return recordToJob(*rec), nil
}

func (a *sqlJobStore) LoadByKey(kind, uniqueKey string) (*jobs.Job, error) {
	rec, ok, err := a.store.GetJobByUniqueKey(kind, uniqueKey)
	if err != nil || !ok {
		return nil, err
	}
	return recordToJob(*rec), nil
}

func (a *sqlJobStore) Save(j *jobs.Job) error { return a.store.UpsertJob(jobToRecord(j)) }

func (a *sqlJobStore) Delete(id string) error { return a.store.DeleteJob(id) }

func (a *sqlJobStore) List() ([]*jobs.Job, error) {
	recs, err := a.store.ListJobs()
	if err != nil {
		return nil, err
	}
	return recordsToJobs(recs), nil
}

func (a *sqlJobStore) Eligible(now time.Time, limit int) ([]*jobs.Job, error) {
	recs, err := a.store.EligibleJobs(now, limit)
	if err != nil {
		return nil, err
	}
	return recordsToJobs(recs), nil
}

func (a *sqlJobStore) TrimDone(cutoff time.Time) (int, error) {
	return a.store.TrimDoneJobs(cutoff)
}

func recordsToJobs(recs []store.JobRecord) []*jobs.Job {
	out := make([]*jobs.Job, 0, len(recs))
	for _, rec := range recs {
		out = append(out, recordToJob(rec))
	}
	return out
}

func jobToRecord(j *jobs.Job) store.JobRecord {
	return store.JobRecord{
		ID:          j.ID,
		Kind:        j.Kind,
		UniqueKey:   j.UniqueKey,
		Priority:    j.Priority,
		Payload:     string(j.Payload),
		Result:      string(j.Result),
		State:       string(j.State),
		Attempts:    j.Attempts,
		MaxAttempts: j.MaxAttempts,
		ScheduledAt: j.ScheduledAt,
		LastError:   j.LastError,
		Requeued:    j.Requeued,
		CreatedAt:   j.CreatedAt,
		UpdatedAt:   j.UpdatedAt,
	}
}

func recordToJob(rec store.JobRecord) *jobs.Job {
	return &jobs.Job{
		ID:          rec.ID,
		Kind:        rec.Kind,
		UniqueKey:   rec.UniqueKey,
		Priority:    rec.Priority,
		Payload:     rawJSON(rec.Payload),
		Result:      rawJSON(rec.Result),
		State:       jobs.State(rec.State),
		Attempts:    rec.Attempts,
		MaxAttempts: rec.MaxAttempts,
		ScheduledAt: rec.ScheduledAt,
		LastError:   rec.LastError,
		Requeued:    rec.Requeued,
		CreatedAt:   rec.CreatedAt,
		UpdatedAt:   rec.UpdatedAt,
	}
}

func rawJSON(s string) json.RawMessage {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return json.RawMessage(s)
}
