package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
)

type gitLane uint8

const (
	gitInteractive gitLane = iota
	gitDeferred
)

type gitTaskKind string

const (
	gitTaskPicker           gitTaskKind = "picker"
	gitTaskRepositoryInfo   gitTaskKind = "repository_info"
	gitTaskBranch           gitTaskKind = "branch"
	gitTaskStatus           gitTaskKind = "status"
	gitTaskFileDiff         gitTaskKind = "file_diff"
	gitTaskPresent          gitTaskKind = "present"
	gitTaskSessionIdentity  gitTaskKind = "session_identity"
	gitTaskWorktreeObserve  gitTaskKind = "worktree_observe"
	gitTaskWorktreeMutation gitTaskKind = "worktree_mutation"
	gitTaskReopen           gitTaskKind = "reopen"
	gitTaskDelegation       gitTaskKind = "delegation"
	gitTaskGarden           gitTaskKind = "garden"
	gitTaskAutomation       gitTaskKind = "automation"
	gitTaskFileIndex        gitTaskKind = "file_index"
	gitTaskSeedArtifact     gitTaskKind = "seed_artifact"
	gitTaskTicketReconcile  gitTaskKind = "ticket_reconcile"
	gitTaskAutoMode         gitTaskKind = "auto_mode"
	gitTaskPluginInstall    gitTaskKind = "plugin_install"
)

type gitTask struct {
	Kind gitTaskKind
	Lane gitLane
}

type gitExecutor interface {
	Run(context.Context, gitTask, func(context.Context, *attngit.Client) error) error
	Close(error)
}

type gitExecutorConfig struct {
	MaxActive            int
	MaxDeferredActive    int
	InteractiveBurst     int
	MaxQueuedInteractive int
	MaxQueuedDeferred    int
}

func (c gitExecutorConfig) validate() error {
	switch {
	case c.MaxActive < 2:
		return errors.New("git executor MaxActive must be at least 2")
	case c.MaxDeferredActive <= 0 || c.MaxDeferredActive >= c.MaxActive:
		return errors.New("git executor MaxDeferredActive must be positive and below MaxActive")
	case c.InteractiveBurst <= 0:
		return errors.New("git executor InteractiveBurst must be positive")
	case c.MaxQueuedInteractive <= 0:
		return errors.New("git executor MaxQueuedInteractive must be positive")
	case c.MaxQueuedDeferred <= 0:
		return errors.New("git executor MaxQueuedDeferred must be positive")
	default:
		return nil
	}
}

type ErrGitQueueSaturated struct {
	Lane  gitLane
	Limit int
}

func (e *ErrGitQueueSaturated) Error() string {
	return fmt.Sprintf("git %s queue saturated at %d", e.Lane, e.Limit)
}

func (l gitLane) String() string {
	if l == gitDeferred {
		return "deferred"
	}
	return "interactive"
}

var (
	ErrNestedGitExecution = errors.New("nested git execution")
	ErrGitExecutorClosed  = errors.New("git executor closed")
	errGitCallbackPanic   = errors.New("git callback panicked")
)

type gitExecutionMarker struct{}

type queuedGitTask struct {
	task       gitTask
	ctx        context.Context
	submitted  time.Time
	admitted   chan struct{}
	runCtx     context.Context
	cancel     context.CancelCauseFunc
	stopCancel func() bool
	err        error
	running    bool
}

type gitExecutorKindStats struct {
	Completed     uint64
	Failed        uint64
	Canceled      uint64
	ChildCommands uint64
	QueueDuration time.Duration
	RunDuration   time.Duration
}

type gitExecutorSnapshot struct {
	Active                    int
	DeferredActive            int
	QueuedInteractive         int
	QueuedDeferred            int
	ActiveHighWater           int
	DeferredActiveHighWater   int
	InteractiveQueueHighWater int
	DeferredQueueHighWater    int
	ByKind                    map[gitTaskKind]gitExecutorKindStats
}

type coordinatedGitExecutor struct {
	mu sync.Mutex

	config gitExecutorConfig
	client *attngit.Client
	root   context.Context
	cancel context.CancelCauseFunc

	interactive              []*queuedGitTask
	deferred                 []*queuedGitTask
	running                  map[*queuedGitTask]struct{}
	active                   int
	deferredActive           int
	interactiveSinceDeferred int
	closedErr                error

	activeHighWater           int
	deferredActiveHighWater   int
	interactiveQueueHighWater int
	deferredQueueHighWater    int
	byKind                    map[gitTaskKind]gitExecutorKindStats
	enqueueObserver           func(gitTask)
}

var productionGitExecutorConfig = gitExecutorConfig{
	MaxActive:            6,
	MaxDeferredActive:    3,
	InteractiveBurst:     3,
	MaxQueuedInteractive: 64,
	MaxQueuedDeferred:    128,
}

func (d *Daemon) wireGitExecution(config gitExecutorConfig) {
	executor, err := newGitExecutor(config, attngit.NewClient())
	if err != nil {
		panic(err)
	}
	d.gitExec = executor
}

func (d *Daemon) gitExecution() gitExecutor {
	d.gitExecMu.Lock()
	defer d.gitExecMu.Unlock()
	if d.gitExec == nil {
		executor, err := newGitExecutor(productionGitExecutorConfig, attngit.NewClient())
		if err != nil {
			panic(err)
		}
		d.gitExec = executor
	}
	return d.gitExec
}

func (d *Daemon) closeGitExecution(cause error) {
	d.gitExecMu.Lock()
	executor := d.gitExec
	d.gitExecMu.Unlock()
	if executor != nil {
		executor.Close(cause)
	}
}

func newGitExecutor(config gitExecutorConfig, client *attngit.Client) (*coordinatedGitExecutor, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if client == nil {
		client = attngit.NewClient()
	}
	root, cancel := context.WithCancelCause(context.Background())
	return &coordinatedGitExecutor{
		config:  config,
		client:  client,
		root:    root,
		cancel:  cancel,
		running: make(map[*queuedGitTask]struct{}),
		byKind:  make(map[gitTaskKind]gitExecutorKindStats),
	}, nil
}

func newDirectGitExecutor() (gitExecutor, error) {
	return newGitExecutor(testDirectGitExecutorConfig(), attngit.NewClient())
}

func gitValue[T any](ctx context.Context, executor gitExecutor, task gitTask, run func(context.Context, *attngit.Client) (T, error)) (T, error) {
	var value T
	err := executor.Run(ctx, task, func(runCtx context.Context, client *attngit.Client) error {
		var runErr error
		value, runErr = run(runCtx, client)
		return runErr
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return value, nil
}

func (e *coordinatedGitExecutor) Run(ctx context.Context, task gitTask, run func(context.Context, *attngit.Client) error) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Value(gitExecutionMarker{}) != nil {
		return ErrNestedGitExecution
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	item := &queuedGitTask{task: task, ctx: ctx, submitted: time.Now(), admitted: make(chan struct{})}
	if err := e.enqueue(item); err != nil {
		return err
	}

	select {
	case <-item.admitted:
	case <-ctx.Done():
		if e.cancelQueued(item, context.Cause(ctx)) {
			return context.Cause(ctx)
		}
		<-item.admitted
	}
	// Close can drain the queue between ctx.Done and cancelQueued; such an item never ran.
	if item.err != nil {
		return item.err
	}

	started := time.Now()
	childCommands := 0
	client := e.client.WithCommandObserver(func(attngit.Operation) {
		childCommands++
	})
	defer func() {
		recovered := recover()
		canceled := context.Cause(item.runCtx) != nil
		if item.stopCancel != nil {
			item.stopCancel()
		}
		item.cancel(nil)
		finishErr := err
		if recovered != nil {
			finishErr = errGitCallbackPanic
		}
		e.finish(item, time.Since(started), childCommands, finishErr, canceled)
		if recovered != nil {
			panic(recovered)
		}
	}()

	runCtx := context.WithValue(item.runCtx, gitExecutionMarker{}, true)
	return run(runCtx, client)
}

func (e *coordinatedGitExecutor) enqueue(item *queuedGitTask) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closedErr != nil {
		return e.closedErr
	}
	if item.task.Lane == gitDeferred {
		if len(e.deferred) >= e.config.MaxQueuedDeferred {
			return &ErrGitQueueSaturated{Lane: gitDeferred, Limit: e.config.MaxQueuedDeferred}
		}
		e.deferred = append(e.deferred, item)
		if len(e.deferred) > e.deferredQueueHighWater {
			e.deferredQueueHighWater = len(e.deferred)
		}
	} else {
		if len(e.interactive) >= e.config.MaxQueuedInteractive {
			return &ErrGitQueueSaturated{Lane: gitInteractive, Limit: e.config.MaxQueuedInteractive}
		}
		e.interactive = append(e.interactive, item)
		if len(e.interactive) > e.interactiveQueueHighWater {
			e.interactiveQueueHighWater = len(e.interactive)
		}
	}
	if e.enqueueObserver != nil {
		e.enqueueObserver(item.task)
	}
	e.dispatchLocked()
	return nil
}

func (e *coordinatedGitExecutor) cancelQueued(item *queuedGitTask, cause error) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if item.running {
		item.cancel(cause)
		return false
	}
	queue := &e.interactive
	if item.task.Lane == gitDeferred {
		queue = &e.deferred
	}
	for i, queued := range *queue {
		if queued != item {
			continue
		}
		*queue = append((*queue)[:i], (*queue)[i+1:]...)
		item.err = cause
		close(item.admitted)
		e.dispatchLocked()
		return true
	}
	return false
}

func (e *coordinatedGitExecutor) dispatchLocked() {
	for e.active < e.config.MaxActive {
		canRunDeferred := len(e.deferred) > 0 && e.deferredActive < e.config.MaxDeferredActive
		if len(e.interactive) > 0 && (!canRunDeferred || e.interactiveSinceDeferred < e.config.InteractiveBurst) {
			e.admitLocked(e.popInteractiveLocked())
			e.interactiveSinceDeferred++
			continue
		}
		if canRunDeferred {
			e.admitLocked(e.popDeferredLocked())
			e.interactiveSinceDeferred = 0
			continue
		}
		if len(e.interactive) > 0 {
			e.admitLocked(e.popInteractiveLocked())
			e.interactiveSinceDeferred++
			continue
		}
		return
	}
}

func (e *coordinatedGitExecutor) popInteractiveLocked() *queuedGitTask {
	item := e.interactive[0]
	e.interactive = e.interactive[1:]
	return item
}

func (e *coordinatedGitExecutor) popDeferredLocked() *queuedGitTask {
	item := e.deferred[0]
	e.deferred = e.deferred[1:]
	return item
}

func (e *coordinatedGitExecutor) admitLocked(item *queuedGitTask) {
	item.running = true
	item.runCtx, item.cancel = context.WithCancelCause(e.root)
	item.stopCancel = context.AfterFunc(item.ctx, func() {
		item.cancel(context.Cause(item.ctx))
	})
	e.running[item] = struct{}{}
	e.active++
	if item.task.Lane == gitDeferred {
		e.deferredActive++
	}
	if e.active > e.activeHighWater {
		e.activeHighWater = e.active
	}
	if e.deferredActive > e.deferredActiveHighWater {
		e.deferredActiveHighWater = e.deferredActive
	}
	close(item.admitted)
}

func (e *coordinatedGitExecutor) finish(item *queuedGitTask, runDuration time.Duration, childCommands int, runErr error, canceled bool) {
	e.mu.Lock()
	delete(e.running, item)
	e.active--
	if item.task.Lane == gitDeferred {
		e.deferredActive--
	}
	stats := e.byKind[item.task.Kind]
	stats.Completed++
	stats.ChildCommands += uint64(childCommands)
	stats.QueueDuration += time.Since(item.submitted) - runDuration
	stats.RunDuration += runDuration
	if runErr != nil {
		stats.Failed++
		if canceled || errors.Is(runErr, context.Canceled) {
			stats.Canceled++
		}
	}
	e.byKind[item.task.Kind] = stats
	e.dispatchLocked()
	e.mu.Unlock()
}

func (e *coordinatedGitExecutor) Close(cause error) {
	if cause == nil {
		cause = ErrGitExecutorClosed
	}
	e.mu.Lock()
	if e.closedErr != nil {
		e.mu.Unlock()
		return
	}
	e.closedErr = cause
	e.cancel(cause)
	queued := append(append([]*queuedGitTask{}, e.interactive...), e.deferred...)
	e.interactive = nil
	e.deferred = nil
	for _, item := range queued {
		item.err = cause
		close(item.admitted)
	}
	e.mu.Unlock()
}

func (e *coordinatedGitExecutor) Snapshot() gitExecutorSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	byKind := make(map[gitTaskKind]gitExecutorKindStats, len(e.byKind))
	for kind, stats := range e.byKind {
		byKind[kind] = stats
	}
	return gitExecutorSnapshot{
		Active:                    e.active,
		DeferredActive:            e.deferredActive,
		QueuedInteractive:         len(e.interactive),
		QueuedDeferred:            len(e.deferred),
		ActiveHighWater:           e.activeHighWater,
		DeferredActiveHighWater:   e.deferredActiveHighWater,
		InteractiveQueueHighWater: e.interactiveQueueHighWater,
		DeferredQueueHighWater:    e.deferredQueueHighWater,
		ByKind:                    byKind,
	}
}
