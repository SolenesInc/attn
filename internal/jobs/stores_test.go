package jobs_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

type reopen func() jobs.Store

func quiet(string, ...any) {}

func onTheJobDatabase(t *testing.T, test func(t *testing.T, open reopen)) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		test(t, func() jobs.Store {
			db, err := store.NewWithDB(filepath.Join(dir, "attn.db"))
			if err != nil {
				t.Fatalf("open the job database: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			return store.NewJobStore(db, dir, quiet)
		})
	})
}

func newRunner(t *testing.T, s jobs.Store, tune func(*jobs.Options)) *jobs.Runner {
	t.Helper()
	opts := jobs.Options{Store: s, Log: quiet}
	if tune != nil {
		tune(&opts)
	}
	r := jobs.New(opts)
	t.Cleanup(r.Stop)
	return r
}

func register(t *testing.T, r *jobs.Runner, kind string, fn jobs.HandlerFunc) {
	t.Helper()
	if err := r.Register(kind, fn); err != nil {
		t.Fatalf("register %s: %v", kind, err)
	}
}

func start(t *testing.T, r *jobs.Runner) {
	t.Helper()
	if err := r.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
}

func enqueue(t *testing.T, r *jobs.Runner, kind string, opts jobs.EnqueueOptions) *jobs.Job {
	t.Helper()
	job, err := r.Enqueue(kind, opts)
	if err != nil {
		t.Fatalf("enqueue %s: %v", kind, err)
	}
	return job
}

func stored(t *testing.T, r *jobs.Runner, id string) *jobs.Job {
	t.Helper()
	job, err := r.Get(id)
	if err != nil || job == nil {
		t.Fatalf("job %s = %v, %v; want it stored", id, job, err)
	}
	return job
}

type runLog struct {
	mu  sync.Mutex
	ran []string
}

func (l *runLog) handler(_ context.Context, job *jobs.Job) (any, error) {
	var name string
	if err := job.DecodePayload(&name); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ran = append(l.ran, name)
	return nil, nil
}

func (l *runLog) names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.ran)
}

func TestDueJobsRunByPriorityThenByScheduleWithinASecond(t *testing.T) {
	onTheJobDatabase(t, func(t *testing.T, open reopen) {
		r := newRunner(t, open(), nil)
		var runs runLog
		register(t, r, "ordered", runs.handler)
		for _, job := range []struct {
			name     string
			priority int
			delay    time.Duration
		}{
			{"j5", 0, 500 * time.Millisecond},
			{"low", 1, 0},
			{"j12345", 0, 123450 * time.Microsecond},
			{"high", 10, 0},
			{"j0", 0, 0},
			{"mid", 5, 0},
			{"j1234", 0, 123400 * time.Microsecond},
			{"whole second", 0, time.Second},
			{"later", 10, 3 * time.Second},
		} {
			enqueue(t, r, "ordered", jobs.EnqueueOptions{Payload: job.name, Priority: job.priority, Delay: job.delay})
		}
		time.Sleep(1500 * time.Millisecond)
		start(t, r)
		synctest.Wait()
		if got := runs.names(); !slices.Equal(got, []string{"high", "mid", "low", "j0", "j1234", "j12345", "j5", "whole second"}) {
			t.Fatalf("ran %v, want priority first, then scheduled order, and nothing not yet due", got)
		}

		enqueue(t, r, "ordered", jobs.EnqueueOptions{Payload: "due now"})
		synctest.Wait()
		enqueue(t, r, "ordered", jobs.EnqueueOptions{Payload: "due in a second", Delay: time.Second})
		time.Sleep(time.Second)
		synctest.Wait()
		if got := runs.names()[8:]; !slices.Equal(got, []string{"due now", "due in a second"}) {
			t.Errorf("then ran %v, want each job at the moment it was scheduled for", got)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if got := runs.names()[10:]; !slices.Equal(got, []string{"later"}) {
			t.Errorf("once due, ran %v, want the future job", got)
		}
	})
}

func TestAJobLeftRunningByACrashRunsAgainWithItsSpentAttempt(t *testing.T) {
	onTheJobDatabase(t, func(t *testing.T, open reopen) {
		s := open()
		stale := time.Now().Add(-time.Hour)
		if err := s.Save(&jobs.Job{
			ID: "orphan", Kind: "compact", Payload: []byte(`"orphan"`), State: jobs.StateRunning, Attempts: 1, MaxAttempts: 3,
			ScheduledAt: stale, CreatedAt: stale, UpdatedAt: stale,
		}); err != nil {
			t.Fatalf("leave a job running: %v", err)
		}

		r := newRunner(t, s, nil)
		var runs runLog
		register(t, r, "compact", runs.handler)
		start(t, r)
		synctest.Wait()
		recovered := stored(t, r, "orphan")
		if !slices.Equal(runs.names(), []string{"orphan"}) || recovered.State != jobs.StateDone || recovered.Attempts != 2 {
			t.Errorf("after a restart the orphan ran %v and settled as %+v, want one run, done on its second attempt", runs.names(), recovered)
		}
	})
}

func TestRetentionTrimsCompletedJobsBySubSecondAgeAndKeepsDeadOnes(t *testing.T) {
	onTheJobDatabase(t, func(t *testing.T, open reopen) {
		r := newRunner(t, open(), func(o *jobs.Options) {
			o.MaxAttempts = 1
			o.Retention = 24 * time.Hour
			o.TrimInterval = 30 * 24 * time.Hour
			o.PollInterval = 30 * 24 * time.Hour
		})
		register(t, r, "ok", func(context.Context, *jobs.Job) (any, error) { return nil, nil })
		register(t, r, "bad", func(context.Context, *jobs.Job) (any, error) { return nil, errors.New("boom") })
		start(t, r)

		older := enqueue(t, r, "ok", jobs.EnqueueOptions{})
		dead := enqueue(t, r, "bad", jobs.EnqueueOptions{})
		synctest.Wait()
		time.Sleep(200 * time.Millisecond)
		newer := enqueue(t, r, "ok", jobs.EnqueueOptions{})
		synctest.Wait()
		if got := r.Trim(); got != 0 {
			t.Errorf("trimmed %d fresh jobs, want none", got)
		}

		time.Sleep(24*time.Hour - 100*time.Millisecond)
		if got := r.Trim(); got != 1 {
			t.Errorf("trimmed %d jobs, want only the one completed more than a day ago", got)
		}
		if j, _ := r.Get(older.ID); j != nil {
			t.Error("the job completed a day ago survived retention")
		}
		if j, _ := r.Get(newer.ID); j == nil || j.State != jobs.StateDone {
			t.Error("the job completed 100ms short of a day was trimmed")
		}
		if j, _ := r.Get(dead.ID); j == nil || j.State != jobs.StateDead {
			t.Error("the dead job was trimmed; it is the actionable record")
		}
	})
}
