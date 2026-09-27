package store

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/jobs"
)

var _ jobs.Store = (*JobStore)(nil)

type JobStore struct {
	store   *Store
	lockDir string
	log     jobs.LogFunc
	lock    *jobs.DirLock
}

func NewJobStore(s *Store, lockDir string, log jobs.LogFunc) *JobStore {
	return &JobStore{store: s, lockDir: lockDir, log: log}
}

func (a *JobStore) Init() error { return nil }

func (a *JobStore) AcquireLock() (string, error) {
	lock, err := jobs.AcquireDirLock(a.lockDir, a.log)
	if err != nil {
		return "", err
	}
	a.lock = lock
	return lock.Path(), nil
}

func (a *JobStore) ReleaseLock(token string) {
	if a.lock == nil || token != a.lock.Path() {
		return
	}
	a.lock.Release()
	a.lock = nil
}

func (a *JobStore) RecoverOrphans(now time.Time) (int, error) {
	return a.store.RecoverRunningJobs(now)
}

func (a *JobStore) Load(id string) (*jobs.Job, error) {
	rec, ok, err := a.store.GetJob(id)
	if err != nil || !ok {
		return nil, err
	}
	return recordToJob(*rec), nil
}

func (a *JobStore) LoadByKey(kind, uniqueKey string) (*jobs.Job, error) {
	rec, ok, err := a.store.GetJobByUniqueKey(kind, uniqueKey)
	if err != nil || !ok {
		return nil, err
	}
	return recordToJob(*rec), nil
}

func (a *JobStore) Save(j *jobs.Job) error { return a.store.UpsertJob(jobToRecord(j)) }

func (a *JobStore) Delete(id string) error { return a.store.DeleteJob(id) }

func (a *JobStore) List() ([]*jobs.Job, error) {
	recs, err := a.store.ListJobs()
	if err != nil {
		return nil, err
	}
	return recordsToJobs(recs), nil
}

func (a *JobStore) Eligible(now time.Time, limit int) ([]*jobs.Job, error) {
	recs, err := a.store.EligibleJobs(now, limit)
	if err != nil {
		return nil, err
	}
	return recordsToJobs(recs), nil
}

func (a *JobStore) TrimDone(cutoff time.Time) (int, error) {
	return a.store.TrimDoneJobs(cutoff)
}

func recordsToJobs(recs []JobRecord) []*jobs.Job {
	out := make([]*jobs.Job, 0, len(recs))
	for _, rec := range recs {
		out = append(out, recordToJob(rec))
	}
	return out
}

func jobToRecord(j *jobs.Job) JobRecord {
	return JobRecord{
		ID:             j.ID,
		Kind:           j.Kind,
		UniqueKey:      j.UniqueKey,
		Priority:       j.Priority,
		Payload:        string(j.Payload),
		Result:         string(j.Result),
		State:          string(j.State),
		Attempts:       j.Attempts,
		MaxAttempts:    j.MaxAttempts,
		ScheduledAt:    j.ScheduledAt,
		LastError:      j.LastError,
		LastDiagnostic: j.LastDiagnostic,
		Requeued:       j.Requeued,
		CreatedAt:      j.CreatedAt,
		UpdatedAt:      j.UpdatedAt,
	}
}

func recordToJob(rec JobRecord) *jobs.Job {
	return &jobs.Job{
		ID:             rec.ID,
		Kind:           rec.Kind,
		UniqueKey:      rec.UniqueKey,
		Priority:       rec.Priority,
		Payload:        rawJSON(rec.Payload),
		Result:         rawJSON(rec.Result),
		State:          jobs.State(rec.State),
		Attempts:       rec.Attempts,
		MaxAttempts:    rec.MaxAttempts,
		ScheduledAt:    rec.ScheduledAt,
		LastError:      rec.LastError,
		LastDiagnostic: rec.LastDiagnostic,
		Requeued:       rec.Requeued,
		CreatedAt:      rec.CreatedAt,
		UpdatedAt:      rec.UpdatedAt,
	}
}

func rawJSON(s string) json.RawMessage {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return json.RawMessage(s)
}
