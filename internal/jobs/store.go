package jobs

import "time"

type Store interface {
	Init() error

	AcquireLock() (string, error)
	ReleaseLock(token string)

	RecoverOrphans(now time.Time) (int, error)

	Load(id string) (*Job, error)
	LoadByKey(kind, uniqueKey string) (*Job, error)
	Save(j *Job) error
	Delete(id string) error
	List() ([]*Job, error)

	Eligible(now time.Time, limit int) ([]*Job, error)
	// NextScheduled reports the earliest scheduled_at among jobs Eligible could return.
	NextScheduled() (time.Time, bool, error)

	TrimDone(cutoff time.Time) (int, error)
}
