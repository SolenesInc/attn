package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

const cronKind = "heartbeat"

func TestAFailingCronEntryStaysArmed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _ := newBubbleRunner(t, func(o *Options) { o.MaxAttempts = 1 })
		var fires atomic.Int64
		if err := r.RegisterCron(cronKind, time.Minute, func(context.Context, *Job) (any, error) {
			fires.Add(1)
			return nil, errors.New("tick failed")
		}, HandlerConfig{}); err != nil {
			t.Fatalf("register cron: %v", err)
		}
		mustStart(t, r)

		for want := int64(1); want <= 3; want++ {
			time.Sleep(time.Minute)
			synctest.Wait()
			if got := fires.Load(); got != want {
				t.Fatalf("cron fired %d times after %d intervals, want %d", got, want, want)
			}
			next, err := r.CronEntry(cronKind)
			if err != nil || next == nil {
				t.Fatalf("cron entry after fire %d: %v (%+v)", want, err, next)
			}
			if next.State != StateQueued || !next.ScheduledAt.After(time.Now()) {
				t.Fatalf("cron entry did not re-arm after fire %d: state %s scheduled %s", want, next.State, next.ScheduledAt)
			}
		}
		entry, err := r.CronEntry(cronKind)
		if err != nil || entry == nil {
			t.Fatalf("cron entry: %v (%+v)", err, entry)
		}
		if entry.State != StateQueued {
			t.Fatalf("failing cron entry state = %s, want queued (attempt cap must not retire it)", entry.State)
		}
		if entry.LastError != "tick failed" {
			t.Fatalf("failing cron entry last_error = %q, want the fire's error", entry.LastError)
		}
	})
}

func TestRestartKeepsAnExistingCronSchedule(t *testing.T) {
	store := newMemStore()
	clock := newFakeClock()
	opts := func() Options {
		return Options{Store: store, Now: clock.now, PollInterval: testPoll, Log: func(string, ...interface{}) {}}
	}
	noop := func(context.Context, *Job) (any, error) { return nil, nil }

	first := New(opts())
	if err := first.RegisterCron(cronKind, time.Hour, noop, HandlerConfig{}); err != nil {
		t.Fatalf("register cron: %v", err)
	}
	mustStart(t, first)
	armed, err := first.CronEntry(cronKind)
	if err != nil || armed == nil {
		t.Fatalf("cron entry: %v (%+v)", err, armed)
	}
	first.Stop()

	clock.advance(30 * time.Minute)

	second := New(opts())
	if err := second.RegisterCron(cronKind, time.Hour, noop, HandlerConfig{}); err != nil {
		t.Fatalf("re-register cron: %v", err)
	}
	mustStart(t, second)
	t.Cleanup(second.Stop)

	after, err := second.CronEntry(cronKind)
	if err != nil || after == nil {
		t.Fatalf("cron entry after restart: %v (%+v)", err, after)
	}
	if !after.ScheduledAt.Equal(armed.ScheduledAt) {
		t.Fatalf("restart moved the next fire from %s to %s", armed.ScheduledAt, after.ScheduledAt)
	}
	if after.ID != armed.ID {
		t.Fatalf("restart created a second cron entry (%s then %s)", armed.ID, after.ID)
	}
}

func TestArmingRevivesACronEntryAPriorBuildKilled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newMemStore()
		opts := func() Options {
			return Options{Store: store, Log: func(string, ...any) {}}
		}
		noop := func(context.Context, *Job) (any, error) { return nil, nil }

		armed := New(opts())
		if err := armed.RegisterCron(cronKind, time.Hour, noop, HandlerConfig{}); err != nil {
			t.Fatalf("register cron: %v", err)
		}
		mustStart(t, armed)
		armed.Stop()

		time.Sleep(2 * time.Hour)
		stranger := New(opts())
		if err := stranger.Register("something-else", noop); err != nil {
			t.Fatalf("register other kind: %v", err)
		}
		mustStart(t, stranger)
		synctest.Wait()
		if j, _ := store.LoadByKey(cronKind, CronKey); j == nil || !j.State.Terminal() {
			t.Fatalf("the unregistered cron entry was not retired (%+v)", j)
		}
		stranger.Stop()

		time.Sleep(2 * time.Hour)
		fired := make(chan struct{}, 4)
		revived := New(opts())
		if err := revived.RegisterCron(cronKind, time.Hour, func(context.Context, *Job) (any, error) {
			fired <- struct{}{}
			return nil, nil
		}, HandlerConfig{}); err != nil {
			t.Fatalf("re-register cron: %v", err)
		}
		mustStart(t, revived)
		t.Cleanup(revived.Stop)

		entry, err := revived.CronEntry(cronKind)
		if err != nil || entry == nil {
			t.Fatalf("cron entry after revive: %v (%+v)", err, entry)
		}
		if entry.State.Terminal() {
			t.Fatalf("cron entry left in a terminal state (%s); the heartbeat is dead for good", entry.State)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		select {
		case <-fired:
		default:
			j, _ := store.LoadByKey(cronKind, CronKey)
			t.Fatalf("the revived cron entry never fired; it sits at state=%q scheduled=%s", j.State, j.ScheduledAt)
		}
	})
}

func TestShorteningTheIntervalPullsAnArmedEntryIn(t *testing.T) {
	store := newMemStore()
	clock := newFakeClock()
	opts := func() Options {
		return Options{Store: store, Now: clock.now, PollInterval: testPoll, Log: func(string, ...interface{}) {}}
	}
	noop := func(context.Context, *Job) (any, error) { return nil, nil }

	first := New(opts())
	if err := first.RegisterCron(cronKind, time.Hour, noop, HandlerConfig{}); err != nil {
		t.Fatalf("register cron: %v", err)
	}
	mustStart(t, first)
	armed, err := first.CronEntry(cronKind)
	if err != nil || armed == nil {
		t.Fatalf("cron entry: %v (%+v)", err, armed)
	}
	first.Stop()

	second := New(opts())
	if err := second.RegisterCron(cronKind, time.Minute, noop, HandlerConfig{}); err != nil {
		t.Fatalf("re-register cron: %v", err)
	}
	mustStart(t, second)
	t.Cleanup(second.Stop)

	after, err := second.CronEntry(cronKind)
	if err != nil || after == nil {
		t.Fatalf("cron entry after restart: %v (%+v)", err, after)
	}
	if !after.ScheduledAt.Before(armed.ScheduledAt) {
		t.Fatalf("a shortened interval left the fire at %s (was %s)", after.ScheduledAt, armed.ScheduledAt)
	}
	if wait := after.ScheduledAt.Sub(clock.now()); wait > time.Minute {
		t.Fatalf("next fire is %s out, past the new %s interval", wait, time.Minute)
	}
	if after.ID != armed.ID {
		t.Fatalf("pulling the entry in minted a second cron record (%s then %s)", armed.ID, after.ID)
	}
}

func TestLengtheningTheIntervalLeavesAnArmedEntryAlone(t *testing.T) {
	store := newMemStore()
	clock := newFakeClock()
	opts := func() Options {
		return Options{Store: store, Now: clock.now, PollInterval: testPoll, Log: func(string, ...interface{}) {}}
	}
	noop := func(context.Context, *Job) (any, error) { return nil, nil }

	first := New(opts())
	if err := first.RegisterCron(cronKind, time.Hour, noop, HandlerConfig{}); err != nil {
		t.Fatalf("register cron: %v", err)
	}
	mustStart(t, first)
	armed, err := first.CronEntry(cronKind)
	if err != nil || armed == nil {
		t.Fatalf("cron entry: %v (%+v)", err, armed)
	}
	first.Stop()

	second := New(opts())
	if err := second.RegisterCron(cronKind, 6*time.Hour, noop, HandlerConfig{}); err != nil {
		t.Fatalf("re-register cron: %v", err)
	}
	mustStart(t, second)
	t.Cleanup(second.Stop)

	after, err := second.CronEntry(cronKind)
	if err != nil || after == nil {
		t.Fatalf("cron entry after restart: %v (%+v)", err, after)
	}
	if !after.ScheduledAt.Equal(armed.ScheduledAt) {
		t.Fatalf("a lengthened interval moved the next fire from %s to %s", armed.ScheduledAt, after.ScheduledAt)
	}
}
