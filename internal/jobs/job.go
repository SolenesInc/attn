// Package jobs MUST NOT import internal/daemon.
package jobs

import (
	"encoding/json"
	"fmt"
	"time"
)

// Never log.Printf: its stderr is discarded when the daemon runs in the background.
type LogFunc func(format string, args ...interface{})

type State string

const (
	StateQueued  State = "queued"
	StateRunning State = "running"
	StateFailed  State = "failed"
	StateDone    State = "done"
	StateDead    State = "dead"
)

func (s State) Terminal() bool { return s == StateDone || s == StateDead }

type Job struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Coalescing identity within a kind: a second Enqueue for the same kind+key
	// targets the SAME record.
	UniqueKey   string          `json:"unique_key,omitempty"`
	Priority    int             `json:"priority,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	State       State           `json:"state"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts,omitempty"`
	ScheduledAt time.Time       `json:"scheduled_at"`
	LastError   string          `json:"last_error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	// A coalescing Enqueue that arrived WHILE this job ran; finish() then returns it
	// to queued so a mid-run trigger is never lost.
	Requeued bool `json:"requeued,omitempty"`

	// The commit fence for THIS run, injected by the runner and never persisted. The
	// handler wraps its durable write in Enter/Leave so a concurrent Cancel cannot tear it.
	CommitGuard *CommitGuard `json:"-"`
}

func (j *Job) DecodePayload(v any) error {
	if j == nil || len(j.Payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(j.Payload, v); err != nil {
		return fmt.Errorf("jobs: decode payload for %s (%s): %w", j.ID, j.Kind, err)
	}
	return nil
}

// Payload/Result are slices, so a shallow copy aliases the store.
func (j *Job) clone() *Job {
	if j == nil {
		return nil
	}
	cp := *j
	cp.Payload = cloneBytes(j.Payload)
	cp.Result = cloneBytes(j.Result)
	return &cp
}

func cloneBytes(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
