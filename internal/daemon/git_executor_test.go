package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
)

func testGitExecutor(t *testing.T, config gitExecutorConfig) *coordinatedGitExecutor {
	t.Helper()
	executor, err := newGitExecutor(config, attngit.NewClient())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { executor.Close(errors.New("test complete")) })
	return executor
}

func testGitConfig() gitExecutorConfig {
	return gitExecutorConfig{
		MaxActive:            2,
		MaxDeferredActive:    1,
		InteractiveBurst:     2,
		MaxQueuedInteractive: 8,
		MaxQueuedDeferred:    8,
	}
}

func runBlockedGitTask(executor gitExecutor, task gitTask, started chan<- string, release <-chan struct{}, name string) <-chan error {
	result := make(chan error, 1)
	go func() {
		result <- executor.Run(context.Background(), task, func(context.Context, *attngit.Client) error {
			started <- name
			<-release
			return nil
		})
	}()
	return result
}

func TestGitExecutorCapsAndReservedInteractiveCapacity(t *testing.T) {
	executor := testGitExecutor(t, testGitConfig())
	enqueued := make(chan gitTask, 3)
	executor.enqueueObserver = func(task gitTask) { enqueued <- task }
	started := make(chan string, 3)
	releaseDeferred := make(chan struct{})
	releaseQueuedDeferred := make(chan struct{})
	releaseInteractive := make(chan struct{})
	deferredTask := gitTask{Kind: gitTaskReopen, Lane: gitDeferred}
	interactiveTask := gitTask{Kind: gitTaskRepositoryInfo, Lane: gitInteractive}

	first := runBlockedGitTask(executor, deferredTask, started, releaseDeferred, "deferred-1")
	<-enqueued
	if got := <-started; got != "deferred-1" {
		t.Fatalf("first start=%q", got)
	}
	second := runBlockedGitTask(executor, deferredTask, started, releaseQueuedDeferred, "deferred-2")
	<-enqueued
	interactive := runBlockedGitTask(executor, interactiveTask, started, releaseInteractive, "interactive")
	<-enqueued
	if got := <-started; got != "interactive" {
		t.Fatalf("reserved slot admitted %q, want interactive", got)
	}
	snapshot := executor.Snapshot()
	if snapshot.Active != 2 || snapshot.DeferredActive != 1 || snapshot.QueuedDeferred != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	close(releaseDeferred)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "deferred-2" {
		t.Fatalf("next start=%q, want deferred-2", got)
	}
	close(releaseQueuedDeferred)
	close(releaseInteractive)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if err := <-interactive; err != nil {
		t.Fatal(err)
	}
}

func TestGitExecutorFIFOAndDeferredProgress(t *testing.T) {
	executor := testGitExecutor(t, testGitConfig())
	enqueued := make(chan gitTask, 6)
	executor.enqueueObserver = func(task gitTask) { enqueued <- task }
	started := make(chan string, 6)
	releases := map[string]chan struct{}{}
	results := map[string]<-chan error{}
	launch := func(name string, lane gitLane) {
		releases[name] = make(chan struct{})
		results[name] = runBlockedGitTask(executor, gitTask{Kind: gitTaskBranch, Lane: lane}, started, releases[name], name)
	}

	launch("interactive-1", gitInteractive)
	<-enqueued
	if got := <-started; got != "interactive-1" {
		t.Fatalf("start=%q", got)
	}
	launch("interactive-2", gitInteractive)
	<-enqueued
	if got := <-started; got != "interactive-2" {
		t.Fatalf("start=%q", got)
	}
	launch("deferred-1", gitDeferred)
	<-enqueued
	launch("interactive-3", gitInteractive)
	<-enqueued
	launch("interactive-4", gitInteractive)
	<-enqueued
	close(releases["interactive-1"])
	if err := <-results["interactive-1"]; err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "deferred-1" {
		t.Fatalf("weighted turn started %q, want deferred-1", got)
	}
	close(releases["interactive-2"])
	if err := <-results["interactive-2"]; err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "interactive-3" {
		t.Fatalf("interactive FIFO started %q, want interactive-3", got)
	}
	close(releases["interactive-3"])
	if err := <-results["interactive-3"]; err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "interactive-4" {
		t.Fatalf("interactive FIFO started %q, want interactive-4", got)
	}
	close(releases["deferred-1"])
	close(releases["interactive-4"])
	if err := <-results["deferred-1"]; err != nil {
		t.Fatal(err)
	}
	if err := <-results["interactive-4"]; err != nil {
		t.Fatal(err)
	}
}

func TestGitExecutorCancellationSaturationNestedAndShutdown(t *testing.T) {
	config := testGitConfig()
	config.MaxQueuedInteractive = 1
	executor := testGitExecutor(t, config)
	enqueued := make(chan gitTask, 3)
	executor.enqueueObserver = func(task gitTask) { enqueued <- task }
	started := make(chan string, 2)
	releaseOne := make(chan struct{})
	releaseTwo := make(chan struct{})
	one := runBlockedGitTask(executor, gitTask{Kind: gitTaskBranch}, started, releaseOne, "one")
	<-enqueued
	<-started
	two := runBlockedGitTask(executor, gitTask{Kind: gitTaskBranch}, started, releaseTwo, "two")
	<-enqueued
	<-started

	queuedCtx, cancelQueued := context.WithCancelCause(context.Background())
	queuedResult := make(chan error, 1)
	unexpectedQueuedRun := errors.New("canceled queued callback ran")
	go func() {
		queuedResult <- executor.Run(queuedCtx, gitTask{Kind: gitTaskBranch}, func(context.Context, *attngit.Client) error {
			return unexpectedQueuedRun
		})
	}()
	<-enqueued
	saturatedErr := executor.Run(context.Background(), gitTask{Kind: gitTaskBranch}, func(context.Context, *attngit.Client) error { return nil })
	var saturated *ErrGitQueueSaturated
	if !errors.As(saturatedErr, &saturated) {
		t.Fatalf("saturation error=%v", saturatedErr)
	}
	cancelCause := errors.New("caller left")
	cancelQueued(cancelCause)
	if err := <-queuedResult; !errors.Is(err, cancelCause) {
		t.Fatalf("queued cancellation=%v", err)
	}

	close(releaseOne)
	if err := <-one; err != nil {
		t.Fatal(err)
	}
	nestedErr := executor.Run(context.Background(), gitTask{Kind: gitTaskBranch}, func(ctx context.Context, client *attngit.Client) error {
		return executor.Run(ctx, gitTask{Kind: gitTaskBranch}, func(context.Context, *attngit.Client) error { return nil })
	})
	if !errors.Is(nestedErr, ErrNestedGitExecution) {
		t.Fatalf("nested error=%v", nestedErr)
	}

	shutdown := errors.New("daemon stopping")
	executor.Close(shutdown)
	close(releaseTwo)
	if err := <-two; err != nil {
		t.Fatal(err)
	}
	if err := executor.Run(context.Background(), gitTask{}, func(context.Context, *attngit.Client) error { return nil }); !errors.Is(err, shutdown) {
		t.Fatalf("post-close error=%v", err)
	}
}

func TestGitExecutorRunningCancellationAndPanicReleaseCapacity(t *testing.T) {
	executor := testGitExecutor(t, testGitConfig())
	started := make(chan struct{})
	ctx, cancel := context.WithCancelCause(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- executor.Run(ctx, gitTask{Kind: gitTaskStatus}, func(runCtx context.Context, _ *attngit.Client) error {
			close(started)
			<-runCtx.Done()
			return context.Cause(runCtx)
		})
	}()
	<-started
	cause := errors.New("request canceled")
	cancel(cause)
	if err := <-result; !errors.Is(err, cause) {
		t.Fatalf("running cancellation=%v", err)
	}
	stats := executor.Snapshot().ByKind[gitTaskStatus]
	if stats.Completed != 1 || stats.Failed != 1 || stats.Canceled != 1 {
		t.Fatalf("canceled status stats=%+v", stats)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected callback panic")
			}
		}()
		_ = executor.Run(context.Background(), gitTask{Kind: gitTaskStatus}, func(context.Context, *attngit.Client) error {
			panic("boom")
		})
	}()
	if err := executor.Run(context.Background(), gitTask{Kind: gitTaskStatus}, func(context.Context, *attngit.Client) error { return nil }); err != nil {
		t.Fatalf("capacity after panic: %v", err)
	}
	stats = executor.Snapshot().ByKind[gitTaskStatus]
	if stats.Completed != 3 || stats.Failed != 2 || stats.Canceled != 1 {
		t.Fatalf("final status stats=%+v", stats)
	}
}

func TestGitExecutorShutdownCancelsRunningAndQueuedWork(t *testing.T) {
	executor := testGitExecutor(t, testGitConfig())
	started := make(chan struct{}, 2)
	running := make(chan error, 2)
	for _, kind := range []gitTaskKind{gitTaskStatus, gitTaskBranch} {
		go func() {
			running <- executor.Run(context.Background(), gitTask{Kind: kind}, func(ctx context.Context, _ *attngit.Client) error {
				started <- struct{}{}
				<-ctx.Done()
				return context.Cause(ctx)
			})
		}()
	}
	<-started
	<-started

	queued := make(chan error, 1)
	queuedSeen := make(chan gitTask, 1)
	unexpectedQueuedRun := errors.New("queued callback ran during shutdown")
	executor.enqueueObserver = func(task gitTask) { queuedSeen <- task }
	go func() {
		queued <- executor.Run(context.Background(), gitTask{Kind: gitTaskFileDiff}, func(context.Context, *attngit.Client) error {
			return unexpectedQueuedRun
		})
	}()
	<-queuedSeen

	shutdown := errors.New("daemon stopping")
	executor.Close(shutdown)
	for range 2 {
		if err := <-running; !errors.Is(err, shutdown) {
			t.Fatalf("running shutdown error=%v", err)
		}
	}
	if err := <-queued; !errors.Is(err, shutdown) {
		t.Fatalf("queued shutdown error=%v", err)
	}
}

// closeOnCauseContext runs onCause the first time context.Cause inspects it after
// cancellation, landing Close between Run choosing ctx.Done and cancelQueued.
type closeOnCauseContext struct {
	context.Context
	once    sync.Once
	onCause func()
}

func (c *closeOnCauseContext) Value(key any) any {
	if c.Err() != nil {
		c.once.Do(c.onCause)
	}
	return c.Context.Value(key)
}

func TestGitExecutorShutdownRacingQueuedCancellationReturnsTheShutdown(t *testing.T) {
	executor := testGitExecutor(t, testGitConfig())
	started := make(chan string, 2)
	release := make(chan struct{})
	defer close(release)
	runBlockedGitTask(executor, gitTask{Kind: gitTaskStatus}, started, release, "one")
	runBlockedGitTask(executor, gitTask{Kind: gitTaskBranch}, started, release, "two")
	<-started
	<-started

	shutdown := errors.New("daemon stopping")
	parent, cancel := context.WithCancel(context.Background())
	ctx := &closeOnCauseContext{Context: parent, onCause: func() { executor.Close(shutdown) }}
	queuedSeen := make(chan gitTask, 1)
	executor.enqueueObserver = func(task gitTask) { queuedSeen <- task }
	queued := make(chan error, 1)
	go func() {
		queued <- executor.Run(ctx, gitTask{Kind: gitTaskFileDiff}, func(context.Context, *attngit.Client) error {
			return errors.New("queued callback ran during shutdown")
		})
	}()
	<-queuedSeen
	cancel()
	if err := <-queued; !errors.Is(err, shutdown) {
		t.Fatalf("queued error=%v, want the shutdown cause", err)
	}
}

func TestGitExecutorMeasurementsCountLogicalWorkAndChildren(t *testing.T) {
	executor := testGitExecutor(t, testGitConfig())
	task := gitTask{Kind: gitTaskRepositoryInfo, Lane: gitInteractive}
	if err := executor.Run(context.Background(), task, func(ctx context.Context, client *attngit.Client) error {
		_, err := client.Output(ctx, attngit.OpMetadata, "", "--version")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := executor.Snapshot()
	stats := snapshot.ByKind[gitTaskRepositoryInfo]
	if stats.Completed != 1 || stats.ChildCommands != 1 || stats.Failed != 0 {
		t.Fatalf("repository info stats=%+v", stats)
	}
}

func TestGitExecutorFailureIsNotCountedAsCancellation(t *testing.T) {
	executor := testGitExecutor(t, testGitConfig())
	failure := errors.New("git failed")
	if err := executor.Run(context.Background(), gitTask{Kind: gitTaskRepositoryInfo}, func(context.Context, *attngit.Client) error {
		return failure
	}); !errors.Is(err, failure) {
		t.Fatalf("run error=%v", err)
	}
	stats := executor.Snapshot().ByKind[gitTaskRepositoryInfo]
	if stats.Completed != 1 || stats.Failed != 1 || stats.Canceled != 0 {
		t.Fatalf("repository info stats=%+v", stats)
	}
}

func TestGitExecutorSaturationReceipt(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	tracked := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "initial")
	if err := os.WriteFile(tracked, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	executor := testGitExecutor(t, productionGitExecutorConfig)
	enqueued := make(chan gitTask, 64)
	executor.enqueueObserver = func(task gitTask) { enqueued <- task }
	release := make(chan struct{})
	deferredResults := make(chan error, 50)
	for range 50 {
		go func() {
			deferredResults <- executor.Run(context.Background(), gitTask{Kind: gitTaskReopen, Lane: gitDeferred}, func(context.Context, *attngit.Client) error {
				<-release
				return nil
			})
		}()
	}
	for range 50 {
		<-enqueued
	}

	type latency struct {
		Kind    gitTaskKind   `json:"kind"`
		Elapsed time.Duration `json:"elapsed"`
		Err     error         `json:"-"`
	}
	latencies := make(chan latency, 3)
	go func() {
		started := time.Now()
		err := executor.Run(context.Background(), gitTask{Kind: gitTaskRepositoryInfo, Lane: gitInteractive}, func(ctx context.Context, client *attngit.Client) error {
			if _, err := client.GetBranchInfo(ctx, repo); err != nil {
				return err
			}
			if _, err := client.GetHeadCommit(ctx, repo); err != nil {
				return err
			}
			if _, err := client.GetDefaultBranch(ctx, repo); err != nil {
				return err
			}
			_, err := client.ObserveWorktrees(ctx, repo)
			return err
		})
		latencies <- latency{Kind: gitTaskRepositoryInfo, Elapsed: time.Since(started), Err: err}
	}()
	go func() {
		started := time.Now()
		_, _, err := newGitStatusReader(executor).Status(context.Background(), repo, gitStatusModeFull)
		latencies <- latency{Kind: gitTaskStatus, Elapsed: time.Since(started), Err: err}
	}()
	go func() {
		started := time.Now()
		_, err := newFileDiffReader(executor).FileDiff(context.Background(), repo, "tracked.txt", "HEAD", "", false)
		latencies <- latency{Kind: gitTaskFileDiff, Elapsed: time.Since(started), Err: err}
	}()
	receiptLatencies := make(map[gitTaskKind]string, 3)
	for range 3 {
		result := <-latencies
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		receiptLatencies[result.Kind] = result.Elapsed.String()
	}
	snapshot := executor.Snapshot()
	close(release)
	for range 50 {
		if err := <-deferredResults; err != nil {
			t.Fatal(err)
		}
	}
	receipt := struct {
		DeferredBatch             int                    `json:"deferred_batch"`
		Config                    gitExecutorConfig      `json:"config"`
		ActiveHighWater           int                    `json:"active_high_water"`
		DeferredActiveHighWater   int                    `json:"deferred_active_high_water"`
		InteractiveQueueHighWater int                    `json:"interactive_queue_high_water"`
		DeferredQueueHighWater    int                    `json:"deferred_queue_high_water"`
		InteractiveLatency        map[gitTaskKind]string `json:"interactive_latency"`
	}{
		DeferredBatch: 50, Config: productionGitExecutorConfig,
		ActiveHighWater: snapshot.ActiveHighWater, DeferredActiveHighWater: snapshot.DeferredActiveHighWater,
		InteractiveQueueHighWater: snapshot.InteractiveQueueHighWater, DeferredQueueHighWater: snapshot.DeferredQueueHighWater,
		InteractiveLatency: receiptLatencies,
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(encoded))
}

func TestSharedCallsFanOutAndLastWaiterCancellation(t *testing.T) {
	calls := newSharedCalls[string, int](context.Background())
	joined := make(chan int, 2)
	calls.joinObserver = func(_ string, waiters int) { joined <- waiters }
	started := make(chan struct{})
	release := make(chan struct{})
	var runs atomic.Int32
	run := func(ctx context.Context) (int, error) {
		runs.Add(1)
		close(started)
		select {
		case <-release:
			return 42, nil
		case <-ctx.Done():
			return 0, context.Cause(ctx)
		}
	}
	first := make(chan int, 1)
	go func() {
		value, _ := calls.Do(context.Background(), "same", run)
		first <- value
	}()
	if waiters := <-joined; waiters != 1 {
		t.Fatalf("first waiters=%d", waiters)
	}
	<-started
	second := make(chan int, 1)
	go func() {
		value, _ := calls.Do(context.Background(), "same", run)
		second <- value
	}()
	if waiters := <-joined; waiters != 2 {
		t.Fatalf("second waiters=%d", waiters)
	}
	close(release)
	if <-first != 42 || <-second != 42 || runs.Load() != 1 {
		t.Fatalf("fanout runs=%d", runs.Load())
	}

	canceled := make(chan struct{})
	runCanceled := func(ctx context.Context) (int, error) {
		<-ctx.Done()
		close(canceled)
		return 0, context.Cause(ctx)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := calls.Do(ctx, "cancel", runCanceled)
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter cancellation=%v", err)
	}
	<-canceled
}
