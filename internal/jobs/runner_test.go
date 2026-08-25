package jobs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

const testPoll = 2 * time.Millisecond

func newTestRunner(t *testing.T, tune func(*Options)) (*Runner, *memStore, *fakeClock) {
	t.Helper()
	store := newMemStore()
	clock := newFakeClock()
	opts := Options{
		Store:        store,
		Now:          clock.now,
		PollInterval: testPoll,
		Log:          func(string, ...interface{}) {},
	}
	if tune != nil {
		tune(&opts)
	}
	r := New(opts)
	t.Cleanup(r.Stop)
	return r, store, clock
}

// Stop must be registered on the bubble's own T, whose cleanups run inside the
// bubble alongside the dispatch goroutines it joins.
func newBubbleRunner(t *testing.T, tune func(*Options)) (*Runner, *memStore) {
	t.Helper()
	store := newMemStore()
	opts := Options{
		Store: store,
		Log:   func(string, ...any) {},
	}
	if tune != nil {
		tune(&opts)
	}
	r := New(opts)
	t.Cleanup(r.Stop)
	return r, store
}

func mustStart(t *testing.T, r *Runner) {
	t.Helper()
	if err := r.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
}

func mustRegister(t *testing.T, r *Runner, kind string, fn HandlerFunc) {
	t.Helper()
	if err := r.Register(kind, fn); err != nil {
		t.Fatalf("register %s: %v", kind, err)
	}
}

func mustGet(t *testing.T, r *Runner, id string) *Job {
	t.Helper()
	j, err := r.Get(id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	if j == nil {
		t.Fatalf("job %s is gone", id)
	}
	return j
}

func TestRunsAJobAndPersistsItsResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, nil)
		type in struct {
			Name string `json:"name"`
		}
		var seen in
		mustRegister(t, r, "greet", func(_ context.Context, job *Job) (any, error) {
			if err := job.DecodePayload(&seen); err != nil {
				return nil, err
			}
			return map[string]string{"greeting": "hello " + seen.Name}, nil
		})
		mustStart(t, r)

		job, err := r.Enqueue("greet", EnqueueOptions{Payload: in{Name: "victor"}})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		synctest.Wait()

		done := mustGet(t, r, job.ID)
		if done.State != StateDone {
			t.Fatalf("state after dispatch settled = %s, want done", done.State)
		}
		if seen.Name != "victor" {
			t.Errorf("handler saw payload name %q, want victor", seen.Name)
		}
		if got, want := string(done.Result), `{"greeting":"hello victor"}`; got != want {
			t.Errorf("persisted result = %s, want %s", got, want)
		}
		if done.Attempts != 1 {
			t.Errorf("attempts = %d, want 1", done.Attempts)
		}
	})
}

func TestJobsWithoutAUniqueKeyAreDistinct(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, store := newBubbleRunner(t, nil)
		var mu sync.Mutex
		var payloads []string
		release := make(chan struct{})
		if err := r.RegisterWith("activity", func(_ context.Context, job *Job) (any, error) {
			var arg string
			if err := job.DecodePayload(&arg); err != nil {
				return nil, err
			}
			mu.Lock()
			payloads = append(payloads, arg)
			mu.Unlock()
			<-release
			return nil, nil
		}, HandlerConfig{MaxConcurrent: 2}); err != nil {
			t.Fatalf("register: %v", err)
		}
		mustStart(t, r)

		if _, err := r.Enqueue("activity", EnqueueOptions{Payload: "a"}); err != nil {
			t.Fatalf("enqueue a: %v", err)
		}
		if _, err := r.Enqueue("activity", EnqueueOptions{Payload: "b"}); err != nil {
			t.Fatalf("enqueue b: %v", err)
		}

		synctest.Wait()
		mu.Lock()
		inFlight := len(payloads)
		mu.Unlock()
		if inFlight != 2 {
			t.Fatalf("%d distinct jobs running once dispatch settled, want 2", inFlight)
		}
		if store.count() != 2 {
			t.Errorf("store holds %d records, want 2 distinct jobs", store.count())
		}
		close(release)
	})
}

func TestUniqueKeyCoalescesABurstIntoOneRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, store := newBubbleRunner(t, nil)
		var runs atomic.Int32
		mustRegister(t, r, "narrate", func(context.Context, *Job) (any, error) {
			runs.Add(1)
			return nil, nil
		})
		mustStart(t, r)

		var last *Job
		for i := 0; i < 3; i++ {
			job, err := r.Enqueue("narrate", EnqueueOptions{UniqueKey: "ws-1", Delay: time.Minute})
			if err != nil {
				t.Fatalf("enqueue %d: %v", i, err)
			}
			last = job
		}
		if store.count() != 1 {
			t.Fatalf("store holds %d records, want 1 coalesced record", store.count())
		}
		synctest.Wait()
		if runs.Load() != 0 {
			t.Fatalf("job ran %d times before its debounce elapsed", runs.Load())
		}

		time.Sleep(time.Minute)
		synctest.Wait()
		if got := mustGet(t, r, last.ID).State; got != StateDone {
			t.Fatalf("state after the debounce elapsed = %s, want done", got)
		}
		if got := runs.Load(); got != 1 {
			t.Errorf("handler ran %d times, want 1 — the burst should collapse", got)
		}
	})
}

func TestRunNowOverridesAPendingDebounce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, nil)
		mustRegister(t, r, "narrate", func(context.Context, *Job) (any, error) { return nil, nil })
		mustStart(t, r)

		if _, err := r.Enqueue("narrate", EnqueueOptions{UniqueKey: "ws-1", Delay: time.Hour}); err != nil {
			t.Fatalf("enqueue debounced: %v", err)
		}
		job, err := r.Enqueue("narrate", EnqueueOptions{UniqueKey: "ws-1", RunNow: true})
		if err != nil {
			t.Fatalf("enqueue run-now: %v", err)
		}
		synctest.Wait()
		if got := mustGet(t, r, job.ID).State; got != StateDone {
			t.Fatalf("run-now job state = %s, want done", got)
		}
	})
}

func TestATriggerArrivingMidRunRunsTheJobAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, nil)
		var runs atomic.Int32
		entered := make(chan struct{}, 4)
		release := make(chan struct{})
		mustRegister(t, r, "narrate", func(context.Context, *Job) (any, error) {
			runs.Add(1)
			entered <- struct{}{}
			<-release
			return nil, nil
		})
		mustStart(t, r)

		job, err := r.Enqueue("narrate", EnqueueOptions{UniqueKey: "ws-1"})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		<-entered

		if _, err := r.Enqueue("narrate", EnqueueOptions{UniqueKey: "ws-1", RunNow: true}); err != nil {
			t.Fatalf("mid-run enqueue: %v", err)
		}
		if got := mustGet(t, r, job.ID); !got.Requeued {
			t.Fatalf("mid-run enqueue did not mark the record requeued (state %s)", got.State)
		}
		close(release)

		<-entered
		synctest.Wait()
		if got := mustGet(t, r, job.ID).State; got != StateDone {
			t.Fatalf("state after the re-run = %s, want done", got)
		}
		if got := runs.Load(); got != 2 {
			t.Errorf("handler ran %d times, want 2 (the run plus the coalesced re-run)", got)
		}
	})
}

func TestFailuresBackOffThenGoDeadOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, func(o *Options) {
			o.MaxAttempts = 3
			o.BackoffBase = time.Minute
		})
		var deadCalls atomic.Int32
		var deadState atomic.Value
		r.OnTerminalFailure(func(j *Job) {
			deadCalls.Add(1)
			deadState.Store(string(j.State))
		})
		mustRegister(t, r, "flaky", func(context.Context, *Job) (any, error) {
			return nil, errors.New("boom")
		})
		mustStart(t, r)

		job, err := r.Enqueue("flaky", EnqueueOptions{})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}

		synctest.Wait()
		after1 := mustGet(t, r, job.ID)
		if after1.State != StateFailed {
			t.Fatalf("state after the first attempt = %s, want failed", after1.State)
		}
		if got, want := after1.ScheduledAt, time.Now().UTC().Add(time.Minute); !got.Equal(want) {
			t.Errorf("retry scheduled at %s, want %s (one base interval)", got, want)
		}

		time.Sleep(time.Minute)
		synctest.Wait()
		after2 := mustGet(t, r, job.ID)
		if after2.State != StateFailed || after2.Attempts != 2 {
			t.Fatalf("after the second attempt: state %s attempts %d, want failed/2", after2.State, after2.Attempts)
		}
		if got, want := after2.ScheduledAt, time.Now().UTC().Add(2*time.Minute); !got.Equal(want) {
			t.Errorf("second retry scheduled at %s, want %s (doubled)", got, want)
		}

		time.Sleep(2 * time.Minute)
		synctest.Wait()
		dead := mustGet(t, r, job.ID)
		if dead.State != StateDead {
			t.Fatalf("state after the third attempt = %s, want dead", dead.State)
		}
		if dead.Attempts != 3 {
			t.Errorf("attempts = %d, want 3", dead.Attempts)
		}
		if dead.LastError != "boom" {
			t.Errorf("last error = %q, want boom", dead.LastError)
		}

		if got := deadCalls.Load(); got != 1 {
			t.Errorf("terminal-failure hook fired %d times, want exactly 1", got)
		}
		if got := deadState.Load(); got != string(StateDead) {
			t.Errorf("terminal-failure hook saw state %v, want dead", got)
		}

		time.Sleep(time.Hour)
		synctest.Wait()
		if got := mustGet(t, r, job.ID); got.Attempts != 3 {
			t.Errorf("dead job was retried (attempts = %d)", got.Attempts)
		}
	})
}

func TestAJobCanRaiseItsOwnAttemptCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, func(o *Options) {
			o.MaxAttempts = 1
			o.BackoffBase = time.Minute
		})
		mustRegister(t, r, "flaky", func(context.Context, *Job) (any, error) {
			return nil, errors.New("boom")
		})
		mustStart(t, r)

		job, err := r.Enqueue("flaky", EnqueueOptions{MaxAttempts: 2})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		synctest.Wait()
		if got := mustGet(t, r, job.ID).State; got != StateFailed {
			t.Fatalf("state after the first attempt = %s, want failed", got)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := mustGet(t, r, job.ID).State; got != StateDead {
			t.Fatalf("state after the retry = %s, want dead", got)
		}
		if got := mustGet(t, r, job.ID).Attempts; got != 2 {
			t.Errorf("attempts = %d, want 2 (the job's own cap)", got)
		}
	})
}

func TestRetryRevivesADeadJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, func(o *Options) { o.MaxAttempts = 1 })
		var fail atomic.Bool
		fail.Store(true)
		mustRegister(t, r, "flaky", func(context.Context, *Job) (any, error) {
			if fail.Load() {
				return nil, errors.New("boom")
			}
			return nil, nil
		})
		mustStart(t, r)

		job, err := r.Enqueue("flaky", EnqueueOptions{})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		synctest.Wait()
		if got := mustGet(t, r, job.ID).State; got != StateDead {
			t.Fatalf("state at the attempt cap = %s, want dead", got)
		}

		fail.Store(false)
		if _, err := r.Retry(job.ID); err != nil {
			t.Fatalf("retry: %v", err)
		}
		synctest.Wait()
		if got := mustGet(t, r, job.ID).State; got != StateDone {
			t.Fatalf("state after the retry = %s, want done", got)
		}
		if got := mustGet(t, r, job.ID).LastError; got != "" {
			t.Errorf("last error = %q, want it cleared by the successful retry", got)
		}
	})
}

func TestCancelWaitsForTheCommitFence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, nil)
		committing := make(chan struct{})
		finishCommit := make(chan struct{})
		var wrote atomic.Bool
		mustRegister(t, r, "commits", func(ctx context.Context, job *Job) (any, error) {
			if !job.CommitGuard.Enter() {
				return nil, errors.New("fenced before commit")
			}
			defer job.CommitGuard.Leave()
			close(committing)
			<-finishCommit
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			wrote.Store(true)
			return nil, nil
		})
		mustStart(t, r)

		job, err := r.Enqueue("commits", EnqueueOptions{})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		<-committing

		cancelReturned := make(chan struct{})
		go func() {
			r.Cancel(job.ID)
			close(cancelReturned)
		}()

		synctest.Wait()
		select {
		case <-cancelReturned:
			t.Fatal("Cancel returned while the handler was inside its commit fence")
		default:
		}

		close(finishCommit)
		<-cancelReturned
		if !wrote.Load() {
			t.Error("the durable write was torn by the cancel")
		}
		if got := mustGet(t, r, job.ID).State; got != StateDone {
			t.Errorf("state = %s, want done — the fenced run completed", got)
		}
	})
}

func TestCancelBeforeTheFenceStopsTheWrite(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	started := make(chan struct{})
	var wrote atomic.Bool
	mustRegister(t, r, "commits", func(ctx context.Context, job *Job) (any, error) {
		close(started)
		<-ctx.Done()
		if !job.CommitGuard.Enter() {
			return nil, errors.New("cancelled before commit")
		}
		defer job.CommitGuard.Leave()
		wrote.Store(true)
		return nil, nil
	})
	mustStart(t, r)

	job, err := r.Enqueue("commits", EnqueueOptions{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-started
	r.Cancel(job.ID)

	if wrote.Load() {
		t.Error("the handler committed after being fenced")
	}
	if got := mustGet(t, r, job.ID).State; got != StateFailed {
		t.Errorf("state = %s, want failed — the cancelled run recorded its outcome", got)
	}
}

func TestRemoveByKeyForgetsTheJob(t *testing.T) {
	r, store, _ := newTestRunner(t, nil)
	mustRegister(t, r, "compact", func(context.Context, *Job) (any, error) { return nil, nil })
	mustStart(t, r)

	if _, err := r.Enqueue("compact", EnqueueOptions{UniqueKey: "ws-1", Delay: time.Hour}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if store.count() != 1 {
		t.Fatalf("store holds %d records, want 1", store.count())
	}

	r.RemoveByKey("compact", "ws-1")
	if store.count() != 0 {
		t.Errorf("store holds %d records after removal, want 0", store.count())
	}
	r.RemoveByKey("compact", "ws-1")
}

func TestStartRequeuesAJobLeftRunningByACrash(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newMemStore()
		stale := time.Now().Add(-time.Hour)
		orphan := &Job{
			ID:          "orphan",
			Kind:        "compact",
			State:       StateRunning,
			Attempts:    1,
			ScheduledAt: stale,
			CreatedAt:   stale,
			UpdatedAt:   stale,
		}
		if err := store.Save(orphan); err != nil {
			t.Fatalf("seed orphan: %v", err)
		}

		r := New(Options{Store: store, Log: func(string, ...any) {}})
		t.Cleanup(r.Stop)
		var ran atomic.Bool
		mustRegister(t, r, "compact", func(context.Context, *Job) (any, error) {
			ran.Store(true)
			return nil, nil
		})
		mustStart(t, r)

		synctest.Wait()
		if !ran.Load() {
			t.Fatal("the recovered job never ran")
		}
		if got := mustGet(t, r, "orphan").State; got != StateDone {
			t.Fatalf("recovered job state = %s, want done", got)
		}
	})
}

func TestPriorityOrdersTheQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, nil)
		var mu sync.Mutex
		var order []string
		mustRegister(t, r, "ordered", func(_ context.Context, job *Job) (any, error) {
			var name string
			if err := job.DecodePayload(&name); err != nil {
				return nil, err
			}
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return nil, nil
		})

		if _, err := r.Enqueue("ordered", EnqueueOptions{Payload: "low", Priority: 1}); err != nil {
			t.Fatalf("enqueue low: %v", err)
		}
		if _, err := r.Enqueue("ordered", EnqueueOptions{Payload: "high", Priority: 10}); err != nil {
			t.Fatalf("enqueue high: %v", err)
		}
		if _, err := r.Enqueue("ordered", EnqueueOptions{Payload: "mid", Priority: 5}); err != nil {
			t.Fatalf("enqueue mid: %v", err)
		}
		mustStart(t, r)

		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		if len(order) != 3 {
			t.Fatalf("%d of 3 jobs ran once dispatch settled: %v", len(order), order)
		}
		want := []string{"high", "mid", "low"}
		for i := range want {
			if order[i] != want[i] {
				t.Fatalf("ran in order %v, want %v", order, want)
			}
		}
	})
}

func TestAKindIsSerializedWithItselfButNotWithOthers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, nil)
		var serialInflight, serialPeak atomic.Int32
		release := make(chan struct{})
		bothKinds := make(chan string, 2)

		mustRegister(t, r, "serial", func(context.Context, *Job) (any, error) {
			n := serialInflight.Add(1)
			for {
				peak := serialPeak.Load()
				if n <= peak || serialPeak.CompareAndSwap(peak, n) {
					break
				}
			}
			bothKinds <- "serial"
			<-release
			serialInflight.Add(-1)
			return nil, nil
		})
		mustRegister(t, r, "other", func(context.Context, *Job) (any, error) {
			bothKinds <- "other"
			<-release
			return nil, nil
		})
		mustStart(t, r)

		for i := range 2 {
			if _, err := r.Enqueue("serial", EnqueueOptions{}); err != nil {
				t.Fatalf("enqueue serial %d: %v", i, err)
			}
		}
		if _, err := r.Enqueue("other", EnqueueOptions{}); err != nil {
			t.Fatalf("enqueue other: %v", err)
		}

		synctest.Wait()
		got := map[string]bool{}
		for range 2 {
			select {
			case kind := <-bothKinds:
				got[kind] = true
			default:
				t.Fatalf("only %v started once dispatch settled, want both serial and other", got)
			}
		}
		if !got["serial"] || !got["other"] {
			t.Fatalf("kinds running together = %v, want both serial and other", got)
		}
		if peak := serialPeak.Load(); peak != 1 {
			t.Errorf("peak concurrent serial runs = %d, want 1", peak)
		}
		close(release)
	})
}

func TestAnUnregisteredKindFailsInPlace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newMemStore()
		now := time.Now()
		stale := &Job{
			ID:          "stale",
			Kind:        "retired_kind",
			State:       StateQueued,
			ScheduledAt: now,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := store.Save(stale); err != nil {
			t.Fatalf("seed stale: %v", err)
		}

		r := New(Options{Store: store, MaxAttempts: 1, Log: func(string, ...any) {}})
		t.Cleanup(r.Stop)
		mustStart(t, r)

		synctest.Wait()
		if got := mustGet(t, r, "stale").State; got != StateDead {
			t.Fatalf("unknown-kind job state = %s, want dead", got)
		}
		if got := mustGet(t, r, "stale").LastError; got == "" {
			t.Error("unknown-kind failure recorded no error to read")
		}
	})
}

func TestEnqueueRejectsAnUnregisteredKind(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	mustStart(t, r)
	if _, err := r.Enqueue("nope", EnqueueOptions{}); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("enqueue error = %v, want ErrUnknownKind", err)
	}
}

func TestAnUnmarshallableResultFailsTheRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, func(o *Options) { o.MaxAttempts = 1 })
		mustRegister(t, r, "bad_result", func(context.Context, *Job) (any, error) {
			return math.Inf(1), nil
		})
		mustStart(t, r)

		job, err := r.Enqueue("bad_result", EnqueueOptions{})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		synctest.Wait()
		if got := mustGet(t, r, job.ID).State; got != StateDead {
			t.Fatalf("state after an unmarshallable result = %s, want dead", got)
		}
		if got := mustGet(t, r, job.ID).LastError; got == "" {
			t.Error("the marshal failure was not recorded")
		}
	})
}

func TestRetentionTrimsCompletedJobsAndKeepsDeadOnes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, store := newBubbleRunner(t, func(o *Options) {
			o.MaxAttempts = 1
			o.Retention = 24 * time.Hour
			// The hourly retention ticker really fires inside a bubble (48 times over
			// the window below), so it is parked past it.
			o.TrimInterval = 30 * 24 * time.Hour
		})
		mustRegister(t, r, "ok", func(context.Context, *Job) (any, error) { return nil, nil })
		mustRegister(t, r, "bad", func(context.Context, *Job) (any, error) {
			return nil, errors.New("boom")
		})
		mustStart(t, r)

		done, err := r.Enqueue("ok", EnqueueOptions{})
		if err != nil {
			t.Fatalf("enqueue ok: %v", err)
		}
		dead, err := r.Enqueue("bad", EnqueueOptions{})
		if err != nil {
			t.Fatalf("enqueue bad: %v", err)
		}
		synctest.Wait()
		if got := mustGet(t, r, done.ID).State; got != StateDone {
			t.Fatalf("the succeeding job settled at %s, want done", got)
		}
		if got := mustGet(t, r, dead.ID).State; got != StateDead {
			t.Fatalf("the failing job settled at %s, want dead", got)
		}

		if got := r.Trim(); got != 0 {
			t.Errorf("trimmed %d fresh jobs, want 0", got)
		}

		time.Sleep(48 * time.Hour)
		if got := r.Trim(); got != 1 {
			t.Errorf("trimmed %d jobs, want 1 (the completed one)", got)
		}
		if j, _ := r.Get(done.ID); j != nil {
			t.Error("the completed job survived retention")
		}
		if j, _ := r.Get(dead.ID); j == nil {
			t.Error("the dead job was trimmed; it is the actionable record")
		}
		if store.count() != 1 {
			t.Errorf("store holds %d records, want 1", store.count())
		}
	})
}

func TestThePeriodicRetentionPassTrimsOnItsOwnInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var trims []string
		r, store := newBubbleRunner(t, func(o *Options) {
			o.MaxAttempts = 1
			o.Retention = 24 * time.Hour
			o.Log = func(format string, args ...any) {
				if !strings.HasPrefix(format, "jobs: trimmed") {
					return
				}
				mu.Lock()
				trims = append(trims, fmt.Sprintf(format, args...))
				mu.Unlock()
			}
		})
		trimCount := func() int {
			mu.Lock()
			defer mu.Unlock()
			return len(trims)
		}
		mustRegister(t, r, "ok", func(context.Context, *Job) (any, error) { return nil, nil })
		mustRegister(t, r, "bad", func(context.Context, *Job) (any, error) {
			return nil, errors.New("boom")
		})
		mustStart(t, r)

		old, err := r.Enqueue("ok", EnqueueOptions{})
		if err != nil {
			t.Fatalf("enqueue ok: %v", err)
		}
		dead, err := r.Enqueue("bad", EnqueueOptions{})
		if err != nil {
			t.Fatalf("enqueue bad: %v", err)
		}
		synctest.Wait()
		if got := mustGet(t, r, old.ID).State; got != StateDone {
			t.Fatalf("the succeeding job settled at %s, want done", got)
		}
		if got := mustGet(t, r, dead.ID).State; got != StateDead {
			t.Fatalf("the failing job settled at %s, want dead", got)
		}

		time.Sleep(24*time.Hour - time.Minute)
		synctest.Wait()
		if j, _ := r.Get(old.ID); j == nil {
			t.Error("a job younger than the retention window was trimmed")
		}
		if got := trimCount(); got != 0 {
			t.Errorf("retention reported %d trims inside the window, want 0: %v", got, trims)
		}

		young, err := r.Enqueue("ok", EnqueueOptions{})
		if err != nil {
			t.Fatalf("enqueue young: %v", err)
		}
		synctest.Wait()
		if got := mustGet(t, r, young.ID).State; got != StateDone {
			t.Fatalf("the second job settled at %s, want done", got)
		}

		time.Sleep(time.Hour + 2*time.Minute)
		synctest.Wait()
		if j, _ := r.Get(old.ID); j != nil {
			t.Error("the aged-out job survived the periodic retention pass")
		}
		if j, _ := r.Get(young.ID); j == nil {
			t.Error("the periodic pass trimmed a job younger than the retention window")
		}
		if j, _ := r.Get(dead.ID); j == nil {
			t.Error("the periodic pass trimmed the dead job; it is the actionable record")
		}
		if got := store.count(); got != 2 {
			t.Errorf("store holds %d records, want 2 (the young one and the dead one)", got)
		}
		if got := trimCount(); got != 1 {
			t.Errorf("retention reported %d trimming passes, want exactly 1: %v", got, trims)
		} else if want := "jobs: trimmed 1 completed job(s) older than 24h0m0s"; trims[0] != want {
			t.Errorf("retention pass reported %q, want %q", trims[0], want)
		}
	})
}

func TestAFailedClaimReleasesItsConcurrencySlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, store := newBubbleRunner(t, nil)
		var runs atomic.Int32
		mustRegister(t, r, "compact", func(context.Context, *Job) (any, error) {
			runs.Add(1)
			return nil, nil
		})
		mustStart(t, r)

		job, err := r.Enqueue("compact", EnqueueOptions{UniqueKey: "ws-1", Delay: time.Minute})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		store.failNextSave(errors.New("disk on fire"))
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := mustGet(t, r, job.ID).State; got != StateQueued {
			t.Fatalf("state after the claim write failed = %s, want queued (the job is still owed a run)", got)
		}
		time.Sleep(defaultPollInterval)
		synctest.Wait()
		if got := mustGet(t, r, job.ID).State; got != StateDone {
			t.Fatalf("state after the next dispatch pass = %s, want done", got)
		}
		if got := runs.Load(); got != 1 {
			t.Errorf("handler ran %d times, want 1", got)
		}
	})
}

func TestASecondRunnerRefusesTheSameStore(t *testing.T) {
	r, store, clock := newTestRunner(t, nil)
	mustStart(t, r)

	second := New(Options{Store: store, Now: clock.now, PollInterval: testPoll, Log: func(string, ...interface{}) {}})
	if err := second.Start(); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Start error = %v, want ErrAlreadyRunning", err)
	}
}

func TestADisabledRunnerIsASafeNoOp(t *testing.T) {
	r := New(Options{})
	if !r.Disabled() {
		t.Fatal("a runner with no store should be disabled")
	}
	if err := r.Start(); err != nil {
		t.Errorf("Start on a disabled runner = %v, want nil", err)
	}
	if _, err := r.Enqueue("anything", EnqueueOptions{}); !errors.Is(err, ErrDisabled) {
		t.Errorf("Enqueue = %v, want ErrDisabled", err)
	}
	if err := r.Register("anything", func(context.Context, *Job) (any, error) { return nil, nil }); !errors.Is(err, ErrDisabled) {
		t.Errorf("Register = %v, want ErrDisabled", err)
	}
	list, err := r.List()
	if err != nil || list != nil {
		t.Errorf("List = (%v, %v), want (nil, nil)", list, err)
	}
	r.Cancel("anything")
	r.Remove("anything")
	r.RemoveByKey("anything", "key")
	r.Stop()
}

func TestStopDrainsInFlightRuns(t *testing.T) {
	r, _, _ := newTestRunner(t, nil)
	started := make(chan struct{})
	var exited atomic.Bool
	mustRegister(t, r, "slow", func(ctx context.Context, _ *Job) (any, error) {
		close(started)
		<-ctx.Done()
		exited.Store(true)
		return nil, ctx.Err()
	})
	mustStart(t, r)

	if _, err := r.Enqueue("slow", EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-started
	r.Stop()
	if !exited.Load() {
		t.Error("Stop returned before the in-flight run exited")
	}
	r.Stop()
}
