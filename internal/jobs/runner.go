package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultMaxAttempts    = 5
	DefaultBackoffBase    = time.Minute
	DefaultBackoffCap     = time.Hour
	DefaultHandlerTimeout = 5 * time.Minute
	DefaultRetention      = 30 * 24 * time.Hour
	DefaultTrimInterval   = time.Hour
	defaultPollInterval   = time.Second
)

// eligiblePageSize is a tripwire: measured eligible working sets are single
// digits (146 rows in ~/.attn, 206 in ~/.attn-dev, nearly all terminal).
const eligiblePageSize = 1000

var ErrDisabled = errors.New("jobs: runner is disabled (no store)")

var ErrUnknownKind = errors.New("jobs: no handler registered for kind")

// A HandlerFunc must wrap its single durable write in job.CommitGuard.Enter/Leave.
type HandlerFunc func(ctx context.Context, job *Job) (any, error)

type handler struct {
	fn       HandlerFunc
	timeout  time.Duration
	limit    int
	interval time.Duration
}

type HandlerConfig struct {
	Timeout       time.Duration
	MaxConcurrent int
}

type EnqueueOptions struct {
	UniqueKey   string
	Payload     any
	Delay       time.Duration
	RunNow      bool
	Priority    int
	MaxAttempts int
}

type Options struct {
	Store        Store
	Log          LogFunc
	Now          func() time.Time
	PollInterval time.Duration
	MaxAttempts  int
	BackoffBase  time.Duration
	BackoffCap   time.Duration
	Retention    time.Duration
	TrimInterval time.Duration
}

type Runner struct {
	store    Store
	log      LogFunc
	now      func() time.Time
	disabled bool

	pollInterval time.Duration
	maxAttempts  int
	backoffBase  time.Duration
	backoffCap   time.Duration
	retention    time.Duration
	trimInterval time.Duration

	// onChange fires after every transition, possibly concurrently; it must not block.
	onChange func(jobID string)

	// onTerminalFailure fires with ioMu released and a cloned record, so it may
	// touch the store.
	onTerminalFailure func(*Job)

	mu          sync.Mutex
	handlers    map[string]handler
	started     bool
	startErr    error
	pendingArms map[string]*pendingArm

	// ioMu serializes every read-modify-write on a job record. When both locks
	// are needed ioMu is ALWAYS the outer lock.
	ioMu sync.Mutex

	runs     map[string]*activeRun // guarded by mu
	inflight map[string]int        // guarded by mu

	wake chan struct{}
	done chan struct{}
	exit chan struct{}

	lockToken string
}

type activeRun struct {
	id     string
	kind   string
	cancel context.CancelFunc
	guard  *CommitGuard
	done   chan struct{}
}

func New(opts Options) *Runner {
	log := opts.Log
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	r := &Runner{
		store:        opts.Store,
		log:          log,
		now:          func() time.Time { return now().UTC() },
		disabled:     opts.Store == nil,
		handlers:     make(map[string]handler),
		pendingArms:  make(map[string]*pendingArm),
		runs:         make(map[string]*activeRun),
		inflight:     make(map[string]int),
		pollInterval: nonZeroDuration(opts.PollInterval, defaultPollInterval),
		maxAttempts:  nonZeroInt(opts.MaxAttempts, DefaultMaxAttempts),
		backoffBase:  nonZeroDuration(opts.BackoffBase, DefaultBackoffBase),
		backoffCap:   nonZeroDuration(opts.BackoffCap, DefaultBackoffCap),
		retention:    nonZeroDuration(opts.Retention, DefaultRetention),
		trimInterval: nonZeroDuration(opts.TrimInterval, DefaultTrimInterval),
		wake:         make(chan struct{}, 1),
	}
	return r
}

func (r *Runner) Disabled() bool { return r.disabled }

func (r *Runner) OnChange(fn func(jobID string)) {
	r.mu.Lock()
	r.onChange = fn
	r.mu.Unlock()
}

func (r *Runner) OnTerminalFailure(fn func(*Job)) {
	r.mu.Lock()
	r.onTerminalFailure = fn
	r.mu.Unlock()
}

func (r *Runner) Register(kind string, fn HandlerFunc) error {
	return r.RegisterWith(kind, fn, HandlerConfig{})
}

func (r *Runner) RegisterWith(kind string, fn HandlerFunc, cfg HandlerConfig) error {
	if r.disabled {
		return ErrDisabled
	}
	if fn == nil {
		return errors.New("jobs: handler must not be nil")
	}
	if kind == "" {
		return errors.New("jobs: kind must not be empty")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultHandlerTimeout
	}
	limit := cfg.MaxConcurrent
	if limit <= 0 {
		limit = 1
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[kind]; exists {
		return fmt.Errorf("jobs: kind already registered: %s", kind)
	}
	r.handlers[kind] = handler{fn: fn, timeout: timeout, limit: limit}
	return nil
}

func (r *Runner) Start() error {
	if r.disabled {
		return nil
	}
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return errors.New("jobs: runner already started")
	}
	if initErr := r.store.Init(); initErr != nil {
		startErr := fmt.Errorf("jobs: init store: %w", initErr)
		r.startErr = startErr
		r.mu.Unlock()
		return startErr
	}
	// A second live Runner on the same store would double-execute every job.
	token, err := r.store.AcquireLock()
	if err != nil {
		r.startErr = err
		r.mu.Unlock()
		return err
	}
	r.startErr = nil
	r.lockToken = token
	r.started = true
	r.done = make(chan struct{})
	r.exit = make(chan struct{})
	done := r.done
	r.mu.Unlock()

	r.ioMu.Lock()
	n, err := r.store.RecoverOrphans(r.now())
	r.ioMu.Unlock()
	if err != nil {
		r.log("jobs: recover orphan running jobs: %v", err)
	} else if n > 0 {
		r.log("jobs: recovered %d orphan running job(s)", n)
	}

	r.armCron()

	go r.loop()
	go r.retentionLoop(done)
	return nil
}

func (r *Runner) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	// Flip under mu so a concurrent second Stop returns instead of double-closing done.
	r.started = false
	done, exit := r.done, r.exit
	token := r.lockToken
	r.lockToken = ""
	r.mu.Unlock()

	// Stop the loop launching MORE runs before joining what is in flight, or
	// cancelAll never terminates.
	close(done)
	<-exit
	r.cancelAll()
	r.store.ReleaseLock(token)
}

func (r *Runner) Enqueue(kind string, opts EnqueueOptions) (*Job, error) {
	if r.disabled {
		return nil, ErrDisabled
	}
	if kind == "" {
		return nil, errors.New("jobs: kind is required")
	}

	r.mu.Lock()
	entry, known := r.handlers[kind]
	r.mu.Unlock()
	if !known {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
	// Any other key on a cron kind mints a second self-perpetuating entry that
	// List hides and CronEntry never finds.
	if entry.interval > 0 && opts.UniqueKey != CronKey {
		return nil, fmt.Errorf("%w: %s (use RegisterCron; a cron kind has one entry)", ErrCronKind, kind)
	}

	payload, err := marshalPayload(opts.Payload)
	if err != nil {
		return nil, fmt.Errorf("jobs: encode payload for %s: %w", kind, err)
	}

	now := r.now()
	scheduled := now
	if !opts.RunNow && opts.Delay > 0 {
		scheduled = now.Add(opts.Delay)
	}

	r.ioMu.Lock()
	defer r.ioMu.Unlock()

	var existing *Job
	if opts.UniqueKey != "" {
		existing, err = r.store.LoadByKey(kind, opts.UniqueKey)
		if err != nil {
			return nil, err
		}
	}

	var job *Job
	switch {
	case existing == nil:
		job = &Job{
			ID:          uuid.NewString(),
			Kind:        kind,
			UniqueKey:   opts.UniqueKey,
			State:       StateQueued,
			ScheduledAt: scheduled,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	case existing.State == StateRunning:
		// Overwriting an in-flight run tears its bookkeeping; Requeued makes
		// finish() re-queue instead of retiring.
		existing.Requeued = true
		existing.ScheduledAt = scheduled
		existing.UpdatedAt = now
		job = existing
	default:
		existing.State = StateQueued
		existing.Attempts = 0
		existing.LastError = ""
		existing.Result = nil
		existing.Requeued = false
		existing.ScheduledAt = scheduled
		existing.UpdatedAt = now
		job = existing
	}

	// A nil Payload LEAVES an existing payload intact: a bare re-trigger must
	// not wipe the inputs a prior enqueue stashed.
	if payload != nil {
		job.Payload = payload
	}
	if opts.Priority != 0 {
		job.Priority = opts.Priority
	}
	if opts.MaxAttempts != 0 {
		job.MaxAttempts = opts.MaxAttempts
	}

	if err := r.store.Save(job); err != nil {
		return nil, err
	}
	r.notifyWorkChange(job.Kind, job.ID)
	r.nudge()
	return job.clone(), nil
}

func (r *Runner) Retry(id string) (*Job, error) {
	if r.disabled {
		return nil, ErrDisabled
	}
	r.ioMu.Lock()
	defer r.ioMu.Unlock()

	existing, err := r.store.Load(id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}
	if existing.State != StateFailed && existing.State != StateDead {
		return existing.clone(), nil
	}
	now := r.now()
	existing.State = StateQueued
	existing.Attempts = 0
	existing.LastError = ""
	existing.Requeued = false
	existing.ScheduledAt = now
	existing.UpdatedAt = now
	if err := r.store.Save(existing); err != nil {
		return nil, err
	}
	r.notifyChange(existing.ID)
	r.nudge()
	return existing.clone(), nil
}

// Cancel blocks until the run's goroutine has exited; a run already inside its
// commit fence is not canceled.
func (r *Runner) Cancel(id string) {
	if r.disabled {
		return
	}
	r.mu.Lock()
	run := r.runs[id]
	if run == nil {
		r.mu.Unlock()
		return
	}
	r.fenceAndWait(run)
}

// CALLER must hold r.mu; fenceAndWait RELEASES it before blocking.
func (r *Runner) fenceAndWait(run *activeRun) {
	if run.guard.tryFence() {
		run.cancel()
	}
	done := run.done
	r.mu.Unlock()
	<-done
}

func (r *Runner) Remove(id string) {
	if r.disabled {
		return
	}
	r.Cancel(id)
	r.ioMu.Lock()
	err := r.store.Delete(id)
	r.ioMu.Unlock()
	if err != nil {
		r.log("jobs: remove %s: %v", id, err)
		return
	}
	r.notifyChange(id)
}

func (r *Runner) RemoveByKey(kind, uniqueKey string) {
	if r.disabled || uniqueKey == "" {
		return
	}
	r.ioMu.Lock()
	existing, err := r.store.LoadByKey(kind, uniqueKey)
	r.ioMu.Unlock()
	if err != nil {
		r.log("jobs: remove %s/%s: %v", kind, uniqueKey, err)
		return
	}
	if existing == nil {
		return
	}
	r.Remove(existing.ID)
}

func (r *Runner) List() ([]*Job, error) {
	if r.disabled {
		return nil, nil
	}
	all, err := r.store.List()
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, j := range all {
		if j != nil && r.cronInterval(j.Kind) > 0 {
			continue
		}
		out = append(out, j)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (r *Runner) Get(id string) (*Job, error) {
	if r.disabled {
		return nil, nil
	}
	return r.store.Load(id)
}

func (r *Runner) GetByKey(kind, uniqueKey string) (*Job, error) {
	if r.disabled || uniqueKey == "" {
		return nil, nil
	}
	return r.store.LoadByKey(kind, uniqueKey)
}

func (r *Runner) loop() {
	defer close(r.exit)
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		r.retryCronArming()
		for {
			select {
			case <-r.done:
				return
			default:
			}
			progressed, err := r.dispatch()
			if err != nil {
				r.log("jobs: dispatch pass: %v", err)
				break
			}
			if !progressed {
				break
			}
		}
		select {
		case <-r.done:
			return
		case <-r.wake:
		case <-ticker.C:
		}
	}
}

func (r *Runner) dispatch() (progressed bool, err error) {
	r.ioMu.Lock()

	now := r.now()
	eligible, err := r.store.Eligible(now, eligiblePageSize)
	if err != nil {
		r.ioMu.Unlock()
		return false, err
	}
	if len(eligible) >= eligiblePageSize {
		r.log("jobs: eligible page full at limit %d; the queue is backlogged and this pass sees only the first %d",
			eligiblePageSize, eligiblePageSize)
	}

	type launchSpec struct {
		job  *Job
		exec handler
		ctx  context.Context
		run  *activeRun
	}
	var launch []launchSpec
	var deadJobs []*Job
	var failedUnknownIDs []string

	for _, j := range eligible {
		if !r.runnable(j) {
			continue
		}
		// Reserve the slot under mu before persisting the claim, so a later
		// same-kind candidate in this pass cannot over-commit the cap.
		r.mu.Lock()
		exec, ok := r.handlers[j.Kind]
		if !ok {
			r.mu.Unlock()
			r.recordPermanentFailureLocked(j, fmt.Errorf("%w: %s", ErrUnknownKind, j.Kind))
			deadJobs = append(deadJobs, j)
			failedUnknownIDs = append(failedUnknownIDs, j.ID)
			continue
		}
		if r.inflight[j.Kind] >= exec.limit {
			r.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		run := &activeRun{
			id:     j.ID,
			kind:   j.Kind,
			cancel: cancel,
			guard:  &CommitGuard{},
			done:   make(chan struct{}),
		}
		r.inflight[j.Kind]++
		r.runs[j.ID] = run
		r.mu.Unlock()

		// Persist the claim under ioMu — never wrap store I/O in mu.
		j.State = StateRunning
		j.Attempts++
		j.Requeued = false
		j.UpdatedAt = now
		if err := r.store.Save(j); err != nil {
			r.log("jobs: persist running state for %s: %v", j.ID, err)
			// Roll the reservation back so the per-kind slot is not leaked.
			r.mu.Lock()
			delete(r.runs, j.ID)
			r.inflight[j.Kind]--
			r.mu.Unlock()
			cancel()
			continue
		}
		launch = append(launch, launchSpec{job: j, exec: exec, ctx: ctx, run: run})
	}

	r.ioMu.Unlock()

	for _, id := range failedUnknownIDs {
		r.notifyChange(id)
	}
	for _, ls := range launch {
		r.notifyWorkChange(ls.job.Kind, ls.job.ID)
	}
	for _, dj := range deadJobs {
		r.notifyTerminalFailure(dj)
	}
	for _, ls := range launch {
		go r.execute(ls.job, ls.exec, ls.ctx, ls.run)
	}
	return len(failedUnknownIDs) > 0 || len(launch) > 0, nil
}

func (r *Runner) runnable(j *Job) bool {
	if j.State != StateFailed {
		return true
	}
	return j.Attempts < r.attemptCap(j)
}

func (r *Runner) attemptCap(j *Job) int {
	if j.MaxAttempts > 0 {
		return j.MaxAttempts
	}
	return r.maxAttempts
}

func (r *Runner) execute(j *Job, exec handler, ctx context.Context, run *activeRun) {
	guard := run.guard
	cancel := run.cancel

	timeoutStop := make(chan struct{})
	timer := time.NewTimer(exec.timeout)
	go func() {
		defer timer.Stop()
		select {
		case <-timer.C:
			if guard.tryFence() {
				cancel()
			}
		case <-timeoutStop:
		}
	}()

	jobForHandler := j.clone()
	jobForHandler.CommitGuard = guard

	result, runErr := func() (result any, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				result = nil
				err = fmt.Errorf("jobs: handler panic: %v", rec)
			}
		}()
		return exec.fn(ctx, jobForHandler)
	}()

	var encoded json.RawMessage
	if runErr == nil {
		encoded, runErr = marshalPayload(result)
		if runErr != nil {
			runErr = fmt.Errorf("jobs: encode result for %s (%s): %w", j.ID, j.Kind, runErr)
		}
	}

	close(timeoutStop)
	cancel()

	// Record the terminal outcome BEFORE signaling exit, so a Cancel unblocking
	// on run.done already sees the durable record.
	r.finish(j.ID, encoded, runErr)

	r.mu.Lock()
	delete(r.runs, run.id)
	r.inflight[run.kind]--
	r.mu.Unlock()
	close(run.done)

	r.nudge()
}

// finish re-loads the record under ioMu so a coalesced trigger that landed
// mid-run is honored, not clobbered.
func (r *Runner) finish(id string, result json.RawMessage, runErr error) {
	r.ioMu.Lock()

	cur, err := r.store.Load(id)
	if err != nil {
		r.ioMu.Unlock()
		r.log("jobs: reload %s before finish: %v", id, err)
		return
	}
	if cur == nil {
		r.ioMu.Unlock()
		return
	}

	if interval := r.cronInterval(cur.Kind); interval > 0 {
		cur.Result = result
		r.rearmCronLocked(cur, interval, runErr)
		r.ioMu.Unlock()
		return
	}

	requeue := false
	wentDead := false
	if runErr != nil {
		if cur.Requeued {
			now := r.now()
			cur.State = StateQueued
			cur.Requeued = false
			cur.Attempts = 0
			cur.LastError = runErr.Error()
			cur.UpdatedAt = now
			if err := r.store.Save(cur); err != nil {
				r.ioMu.Unlock()
				r.log("jobs: persist requeued-after-failure state for %s: %v", id, err)
				return
			}
			requeue = true
		} else {
			wentDead = r.recordFailureLocked(cur, runErr)
		}
	} else {
		now := r.now()
		cur.LastError = ""
		cur.Result = result
		cur.UpdatedAt = now
		if cur.Requeued {
			cur.State = StateQueued
			cur.Requeued = false
			cur.Attempts = 0
			requeue = true
		} else {
			cur.State = StateDone
		}
		if err := r.store.Save(cur); err != nil {
			r.ioMu.Unlock()
			r.log("jobs: persist done state for %s: %v", id, err)
			return
		}
	}
	r.ioMu.Unlock()

	r.notifyChange(id)
	if wentDead {
		r.notifyTerminalFailure(cur)
	}
	if requeue {
		r.nudge()
	}
}

// Caller holds ioMu. Reports a StateDead crossing so the caller can fire the
// terminal-failure hook AFTER releasing ioMu (never under it).
func (r *Runner) recordFailureLocked(j *Job, cause error) (wentDead bool) {
	now := r.now()
	limit := r.attemptCap(j)
	j.LastError = cause.Error()
	j.UpdatedAt = now
	if j.Attempts >= limit {
		j.State = StateDead
		j.ScheduledAt = now
		wentDead = true
		r.log("jobs: %s (%s) dead after %d attempts: %v", j.ID, j.Kind, j.Attempts, cause)
	} else {
		j.State = StateFailed
		j.ScheduledAt = now.Add(r.backoff(j.Attempts))
		r.log("jobs: %s (%s) failed (attempt %d/%d), retry at %s: %v",
			j.ID, j.Kind, j.Attempts, limit, j.ScheduledAt.Format(time.RFC3339), cause)
	}
	if err := r.store.Save(j); err != nil {
		r.log("jobs: persist failure state for %s: %v", j.ID, err)
	}
	return wentDead
}

// Caller holds ioMu. An unclaimable job never increments Attempts, so backoff
// would re-fail it forever.
func (r *Runner) recordPermanentFailureLocked(j *Job, cause error) {
	now := r.now()
	j.State = StateDead
	j.LastError = cause.Error()
	j.ScheduledAt = now
	j.UpdatedAt = now
	r.log("jobs: %s (%s) dead, cannot be retried: %v", j.ID, j.Kind, cause)
	if err := r.store.Save(j); err != nil {
		r.log("jobs: persist permanent failure for %s: %v", j.ID, err)
	}
}

func (r *Runner) notifyTerminalFailure(j *Job) {
	r.mu.Lock()
	fn := r.onTerminalFailure
	r.mu.Unlock()
	if fn != nil {
		fn(j.clone())
	}
}

func (r *Runner) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := r.backoffBase
	for i := 1; i < attempt; i++ {
		d *= 2
		// `d <= 0` guards int64 overflow: a wrapped-negative delay yields a past
		// ScheduledAt and a hot retry loop.
		if d <= 0 || d >= r.backoffCap {
			return r.backoffCap
		}
	}
	if d > r.backoffCap {
		return r.backoffCap
	}
	return d
}

// Only valid AFTER the dispatch loop has exited, so no new run can register
// while it drains.
func (r *Runner) cancelAll() {
	r.mu.Lock()
	runs := make([]*activeRun, 0, len(r.runs))
	for _, run := range r.runs {
		runs = append(runs, run)
	}
	r.mu.Unlock()
	for _, run := range runs {
		if run.guard.tryFence() {
			run.cancel()
		}
		<-run.done
	}
}

func (r *Runner) retentionLoop(done <-chan struct{}) {
	ticker := time.NewTicker(r.trimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			r.Trim()
		}
	}
}

func (r *Runner) Trim() int {
	if r.disabled {
		return 0
	}
	r.ioMu.Lock()
	n, err := r.store.TrimDone(r.now().Add(-r.retention))
	r.ioMu.Unlock()
	if err != nil {
		r.log("jobs: retention pass failed: %v", err)
		return 0
	}
	if n > 0 {
		r.log("jobs: trimmed %d completed job(s) older than %s", n, r.retention)
	}
	return n
}

func (r *Runner) nudge() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Transitions of cron kinds are dropped: a heartbeat firing forever would
// append a durable event and re-push a snapshot per fire.
func (r *Runner) notifyWorkChange(kind, jobID string) {
	if r.cronInterval(kind) > 0 {
		return
	}
	r.notifyChange(jobID)
}

func (r *Runner) notifyChange(jobID string) {
	r.mu.Lock()
	fn := r.onChange
	r.mu.Unlock()
	if fn != nil {
		fn(jobID)
	}
}

// A nil value encodes to nil, not "null" — Enqueue relies on nil meaning
// "leave what is there".
func marshalPayload(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		if len(raw) == 0 {
			return nil, nil
		}
		return cloneBytes(raw), nil
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func nonZeroDuration(v, fallback time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return fallback
}

func nonZeroInt(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}
