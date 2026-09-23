package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"

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
)

type gitExecutionMarker struct{}

type queuedGitTask struct {
	task       gitTask
	ctx        context.Context
	admitted   chan struct{}
	runCtx     context.Context
	cancel     context.CancelCauseFunc
	stopCancel func() bool
	err        error
	running    bool
}

type coordinatedGitExecutor struct {
	mu sync.Mutex

	config gitExecutorConfig
	client *attngit.Client
	root   context.Context
	cancel context.CancelCauseFunc

	interactive              []*queuedGitTask
	deferred                 []*queuedGitTask
	active                   int
	deferredActive           int
	interactiveSinceDeferred int
	closedErr                error
	enqueueObserver          func(gitTask)
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
	return d.gitExec
}

func (d *Daemon) closeGitExecution(cause error) {
	d.gitExec.Close(cause)
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
		config: config,
		client: client,
		root:   root,
		cancel: cancel,
	}, nil
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

func (e *coordinatedGitExecutor) Run(ctx context.Context, task gitTask, run func(context.Context, *attngit.Client) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Value(gitExecutionMarker{}) != nil {
		return ErrNestedGitExecution
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	item := &queuedGitTask{task: task, ctx: ctx, admitted: make(chan struct{})}
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
	if item.err != nil {
		return item.err
	}

	defer func() {
		recovered := recover()
		if item.stopCancel != nil {
			item.stopCancel()
		}
		item.cancel(nil)
		e.finish(item)
		if recovered != nil {
			panic(recovered)
		}
	}()

	runCtx := context.WithValue(item.runCtx, gitExecutionMarker{}, true)
	return run(runCtx, e.client)
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
	} else {
		if len(e.interactive) >= e.config.MaxQueuedInteractive {
			return &ErrGitQueueSaturated{Lane: gitInteractive, Limit: e.config.MaxQueuedInteractive}
		}
		e.interactive = append(e.interactive, item)
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
	e.active++
	if item.task.Lane == gitDeferred {
		e.deferredActive++
	}
	close(item.admitted)
}

func (e *coordinatedGitExecutor) finish(item *queuedGitTask) {
	e.mu.Lock()
	e.active--
	if item.task.Lane == gitDeferred {
		e.deferredActive--
	}
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
