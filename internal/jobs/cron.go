package jobs

import (
	"errors"
	"fmt"
	"time"
)

const CronKey = "cron"

// A cron entry NEVER dies: a failing fire is logged and re-armed instead of counting
// toward the attempt cap.
func (r *Runner) RegisterCron(kind string, interval time.Duration, fn HandlerFunc, cfg HandlerConfig) error {
	if interval <= 0 {
		return fmt.Errorf("jobs: cron interval for %s must be positive, got %s", kind, interval)
	}
	if err := r.RegisterWith(kind, fn, cfg); err != nil {
		return err
	}
	r.mu.Lock()
	entry := r.handlers[kind]
	entry.interval = interval
	r.handlers[kind] = entry
	r.mu.Unlock()
	return nil
}

func (r *Runner) cronInterval(kind string) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handlers[kind].interval
}

func (r *Runner) armCron() {
	r.mu.Lock()
	kinds := make(map[string]time.Duration, len(r.handlers))
	for kind, entry := range r.handlers {
		if entry.interval > 0 {
			kinds[kind] = entry.interval
		}
	}
	r.mu.Unlock()

	for kind, interval := range kinds {
		r.armCronKind(kind, interval)
	}
}

// In memory because a failed arm left NO record behind: dispatch never selects it and
// finish() never re-arms it.
type pendingArm struct {
	interval time.Duration
	attempts int
	retryAt  time.Time
	lastErr  error
}

func (r *Runner) armCronKind(kind string, interval time.Duration) {
	if err := r.writeCronEntry(kind, interval); err != nil {
		r.deferCronArm(kind, interval, err)
		return
	}
	r.mu.Lock()
	pending := r.pendingArms[kind]
	delete(r.pendingArms, kind)
	r.mu.Unlock()
	if pending != nil {
		r.log("jobs: cron %s is armed after %d failed attempt(s)", kind, pending.attempts)
	}
}

// An on-schedule record is left alone: re-arming every boot would starve a daemon restarted
// more often than the interval. A terminal or further-out record is revived or pulled in.
func (r *Runner) writeCronEntry(kind string, interval time.Duration) error {
	existing, err := r.GetByKey(kind, CronKey)
	if err != nil {
		return fmt.Errorf("read cron entry: %w", err)
	}
	if existing != nil && !existing.State.Terminal() {
		wait := existing.ScheduledAt.Sub(r.now())
		if wait <= interval {
			return nil
		}
		r.log("jobs: cron %s was armed for %s out, past its %s interval; pulling it in",
			kind, wait.Round(time.Second), interval)
	} else if existing != nil {
		r.log("jobs: cron entry for %s was %s (%s); reviving it", kind, existing.State, existing.LastError)
	}
	if _, err := r.Enqueue(kind, EnqueueOptions{UniqueKey: CronKey, Delay: interval}); err != nil {
		return fmt.Errorf("enqueue cron entry: %w", err)
	}
	return nil
}

func (r *Runner) deferCronArm(kind string, interval time.Duration, cause error) {
	r.mu.Lock()
	pending := r.pendingArms[kind]
	if pending == nil {
		pending = &pendingArm{interval: interval}
		r.pendingArms[kind] = pending
	}
	pending.attempts++
	pending.lastErr = cause
	pending.retryAt = r.now().Add(r.backoff(pending.attempts))
	attempts, retryAt := pending.attempts, pending.retryAt
	r.mu.Unlock()
	r.log("jobs: CRON %s IS NOT ARMED (attempt %d): %v — it will not fire until it is; retrying at %s",
		kind, attempts, cause, retryAt.Format(time.RFC3339))
}

// Runs once per dispatch pass, so it costs one uncontended mutex on a runner with
// nothing parked — which is every healthy runner.
func (r *Runner) retryCronArming() {
	r.mu.Lock()
	if len(r.pendingArms) == 0 {
		r.mu.Unlock()
		return
	}
	now := r.now()
	due := make(map[string]time.Duration, len(r.pendingArms))
	for kind, pending := range r.pendingArms {
		if !now.Before(pending.retryAt) {
			due[kind] = pending.interval
		}
	}
	r.mu.Unlock()
	for kind, interval := range due {
		r.armCronKind(kind, interval)
	}
}

func (r *Runner) cronArmError(kind string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return fmt.Errorf("the runner did not start: %w", r.startErr)
	}
	if pending := r.pendingArms[kind]; pending != nil {
		return fmt.Errorf("arming it has failed %d time(s), last: %w", pending.attempts, pending.lastErr)
	}
	return nil
}

// Caller holds ioMu.
func (r *Runner) rearmCronLocked(j *Job, interval time.Duration, runErr error) {
	now := r.now()
	j.State = StateQueued
	j.Attempts = 0
	j.Requeued = false
	j.ScheduledAt = now.Add(interval)
	j.UpdatedAt = now
	if runErr != nil {
		j.LastError = runErr.Error()
		r.log("jobs: cron %s failed, next fire at %s: %v",
			j.Kind, j.ScheduledAt.Format(time.RFC3339), runErr)
	} else {
		j.LastError = ""
	}
	if err := r.store.Save(j); err != nil {
		// The claim write left the row RUNNING, which dispatch never selects; only the orphan
		// recovery at the next Start puts it back in the rotation.
		r.log("jobs: CRON %s DID NOT RE-ARM: %v — it will not fire again until the daemon restarts", j.Kind, err)
	}
}

var ErrNotCron = errors.New("jobs: kind is not a cron entry")

var ErrCronKind = errors.New("jobs: kind is a cron entry and cannot be enqueued directly")

// A missing record is an error whenever the runner knows why it is missing: "no entry,
// no error" reads as "not armed yet" and explains nothing.
func (r *Runner) CronEntry(kind string) (*Job, error) {
	if r.disabled {
		return nil, ErrDisabled
	}
	if r.cronInterval(kind) <= 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotCron, kind)
	}
	entry, err := r.GetByKey(kind, CronKey)
	if err != nil || entry != nil {
		return entry, err
	}
	if armErr := r.cronArmError(kind); armErr != nil {
		return nil, fmt.Errorf("jobs: cron entry for %s is not armed: %w", kind, armErr)
	}
	return nil, nil
}
