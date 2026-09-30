package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

const testPoll = 2 * time.Millisecond

func TestEnqueueDuringAUniqueJobRunsOneFollowupAfterTheCurrentPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, nil)
		started := make(chan int)
		finishFirst := make(chan struct{})
		var runs atomic.Int32
		mustRegister(t, r, "keep", func(ctx context.Context, _ *Job) (any, error) {
			n := int(runs.Add(1))
			select {
			case started <- n:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if n == 1 {
				select {
				case <-finishFirst:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, nil
		})
		mustStart(t, r)
		job, err := r.Enqueue("keep", EnqueueOptions{UniqueKey: "all"})
		if err != nil {
			t.Fatal(err)
		}
		if n := <-started; n != 1 {
			t.Fatalf("first pass = %d", n)
		}
		for range 3 {
			if _, err := r.Enqueue("keep", EnqueueOptions{UniqueKey: "all"}); err != nil {
				t.Fatal(err)
			}
		}
		close(finishFirst)
		if n := <-started; n != 2 {
			t.Fatalf("followup pass = %d", n)
		}
		synctest.Wait()
		if got := mustGet(t, r, job.ID).State; got != StateDone || runs.Load() != 2 {
			t.Fatalf("coalesced job: state=%s, runs=%d; want done after two passes", got, runs.Load())
		}
	})
}

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
