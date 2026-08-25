package bus

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegisterAfterStartDeliversFromHead(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	if _, err := b.Publish("before.install", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	rec := newRecorder()
	if err := b.Register("app:notes", All, rec.handle); err != nil {
		t.Fatalf("Register after Start: %v", err)
	}

	if _, err := b.Publish("after.install", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the post-registration fact", func() bool { return rec.count() >= 1 })

	names, _ := rec.snapshot()
	if len(names) != 1 || names[0] != "after.install" {
		t.Fatalf("delivered %v; a consumer installed at runtime must not replay history", names)
	}
	if _, ok, err := s.GetConsumer("app:notes"); err != nil || !ok {
		t.Fatalf("registration was not persisted (found=%v, err=%v)", ok, err)
	}
}

func TestRegisterAfterStartResumesAnExistingCursor(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	if err := s.SaveConsumer(Consumer{Name: "app:notes", Cursor: 0, Filter: "*", Enabled: true}, time.Now()); err != nil {
		t.Fatalf("seeding the consumer: %v", err)
	}
	for _, name := range []string{"a.happened", "b.happened"} {
		if _, err := b.Publish(name, "", nil); err != nil {
			t.Fatalf("Publish(%s): %v", name, err)
		}
	}

	rec := newRecorder()
	if err := b.Register("app:notes", All, rec.handle); err != nil {
		t.Fatalf("Register after Start: %v", err)
	}
	waitFor(t, "the backlog to be delivered", func() bool { return rec.count() >= 2 })

	names, _ := rec.snapshot()
	if len(names) != 2 || names[0] != "a.happened" || names[1] != "b.happened" {
		t.Fatalf("delivered %v; a runtime registration must resume its persisted cursor", names)
	}
}

func TestUnregisterStopsDeliveryAndDeletesTheRow(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	witness := newRecorder()
	if err := b.Register("witness", All, witness.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	rec := newRecorder()
	if err := b.Register("app:notes", All, rec.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := b.Publish("one.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the first delivery", func() bool { return rec.count() == 1 })

	if err := b.Unregister("app:notes"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if _, ok, err := s.GetConsumer("app:notes"); err != nil || ok {
		t.Fatalf("the consumer row survived Unregister (found=%v, err=%v)", ok, err)
	}

	if _, err := b.Publish("two.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the witness to receive the later fact", func() bool { return witness.count() == 2 })
	if n := rec.count(); n != 1 {
		t.Fatalf("the unregistered consumer received %d events, want 1: its loop is still delivering", n)
	}
}

func TestUnregisterInterruptsAConsumerParkedInRetryBackoff(t *testing.T) {
	s := newMemStore()
	b := New(Options{
		Store:        s,
		Log:          func(string, ...interface{}) {},
		PollInterval: 5 * time.Millisecond,
		// A retry sleep far longer than any correct run needs: if Unregister waits for
		// the timer, this test hangs to its tripwire instead of passing slowly.
		RetryBase:    time.Hour,
		RetryCap:     time.Hour,
		TrimInterval: time.Hour,
	})

	var attempts atomic.Int64
	handler := func(context.Context, Event) error {
		attempts.Add(1)
		return errors.New("handler boom")
	}
	if err := b.Register("app:stuck", All, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	if _, err := b.Publish("work.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the handler to fail once", func() bool { return attempts.Load() >= 1 })

	done := make(chan error, 1)
	go func() { done <- b.Unregister("app:stuck") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Unregister: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Unregister blocked behind the consumer's retry sleep")
	}
}

func TestUnregisterDeletesTheRowOnlyAfterTheLoopExits(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	entered := make(chan struct{})
	release := make(chan struct{})
	releaseHandler := sync.OnceFunc(func() { close(release) })
	handler := func(context.Context, Event) error {
		close(entered)
		<-release
		return nil
	}
	if err := b.Register("app:slow", All, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)
	t.Cleanup(releaseHandler)

	d := b.durables[0]
	var deletedWhileRunning atomic.Bool
	s.onDelete = func(string) {
		select {
		case <-d.done:
		default:
			deletedWhileRunning.Store(true)
		}
	}

	if _, err := b.Publish("work.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	<-entered

	done := make(chan error, 1)
	go func() { done <- b.Unregister("app:slow") }()

	waitFor(t, "the consumer to be retired", d.isRetired)
	releaseHandler()

	if err := <-done; err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if deletedWhileRunning.Load() {
		t.Fatal("the row was deleted while the delivery loop was still running")
	}
	select {
	case <-d.done:
	default:
		t.Fatal("Unregister returned while the delivery loop was still running")
	}
	if _, ok, err := s.GetConsumer("app:slow"); err != nil || ok {
		t.Fatalf("a late handler result wrote to the deleted registration (found=%v, err=%v)", ok, err)
	}
}

func TestRegisterIsRefusedWhileTheNameIsBeingUnregistered(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	entered := make(chan struct{})
	release := make(chan struct{})
	releaseHandler := sync.OnceFunc(func() { close(release) })
	handler := func(context.Context, Event) error {
		close(entered)
		<-release
		return nil
	}
	if err := b.Register("app:notes", All, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)
	t.Cleanup(releaseHandler)

	d := b.durables[0]
	if _, err := b.Publish("work.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	<-entered

	done := make(chan error, 1)
	go func() { done <- b.Unregister("app:notes") }()
	waitFor(t, "the consumer to be retired", d.isRetired)

	if _, ok, err := s.GetConsumer("app:notes"); err != nil || !ok {
		t.Fatalf("expected the row to still exist mid-unregister (found=%v, err=%v)", ok, err)
	}
	if err := b.Register("app:notes", All, func(context.Context, Event) error { return nil }); err == nil {
		t.Fatal("Register was served while the name was being unregistered")
	}

	releaseHandler()
	if err := <-done; err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	rec := newRecorder()
	if err := b.Register("app:notes", All, rec.handle); err != nil {
		t.Fatalf("Register after the unregister completed: %v; the name stayed claimed", err)
	}
	if _, err := b.Publish("after.reinstall", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the reinstalled consumer to deliver", func() bool { return rec.count() >= 1 })
}

func TestRetiredConsumerDropsLateResults(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	d := b.newDurable("app:gone", All, nil, func(context.Context, Event) error { return nil })
	t.Cleanup(d.cancel)
	d.retire()

	if err := b.advance(d, 7); err != nil {
		t.Fatalf("a late cursor advance must be dropped silently, got %v", err)
	}
	if _, ok, err := s.GetConsumer("app:gone"); err != nil || ok {
		t.Fatalf("a retired consumer moved or recreated its row (found=%v, err=%v)", ok, err)
	}

	d.recordFailure("handler boom", 3)
	if reason, failures := d.stallReason(), d.drainFailures(); reason != "" || failures != 0 {
		t.Fatalf("a retired consumer recorded a stall (%q, %d attempts)", reason, failures)
	}
}

func TestUnregisteringAStalledConsumerReleasesTheRetentionFloor(t *testing.T) {
	s := newMemStore()
	clk := &testClock{now: time.Now()}
	b := New(Options{
		Store:        s,
		Log:          func(string, ...interface{}) {},
		Now:          clk.get,
		Retention:    time.Hour,
		TrimInterval: time.Hour,
		PollInterval: 5 * time.Millisecond,
		RetryBase:    5 * time.Millisecond,
		RetryCap:     20 * time.Millisecond,
	})

	var attempts atomic.Int64
	handler := func(context.Context, Event) error {
		attempts.Add(1)
		return errors.New("handler boom")
	}
	if err := b.Register("app:stuck", All, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	clk.advance(-4 * time.Hour)
	for _, name := range []string{"a.happened", "b.happened"} {
		if _, err := b.Publish(name, "", nil); err != nil {
			t.Fatalf("Publish(%s): %v", name, err)
		}
	}
	clk.advance(4 * time.Hour)
	waitFor(t, "the consumer to stall on the first fact", func() bool { return attempts.Load() >= 2 })

	if n, err := b.Trim(); err != nil || n != 0 {
		t.Fatalf("trimmed %d event(s) (err=%v); a stalled enabled consumer must pin the log", n, err)
	}

	if err := b.Unregister("app:stuck"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if n, err := b.Trim(); err != nil || n != 2 {
		t.Fatalf("trimmed %d event(s) (err=%v) after the consumer was unregistered, want 2: the floor is still pinned", n, err)
	}
}

func TestUnregisterIsIdempotentAndClearsOrphanRows(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	if err := s.SaveConsumer(Consumer{Name: "app:ghost", Cursor: 0, Filter: "*", Enabled: true}, time.Now()); err != nil {
		t.Fatalf("seeding the orphan row: %v", err)
	}

	if err := b.Unregister("app:ghost"); err != nil {
		t.Fatalf("Unregister of an orphan row: %v", err)
	}
	if _, ok, err := s.GetConsumer("app:ghost"); err != nil || ok {
		t.Fatalf("the orphan row survived (found=%v, err=%v)", ok, err)
	}
	if err := b.Unregister("app:ghost"); err != nil {
		t.Fatalf("second Unregister: %v; an uninstall path must be re-runnable", err)
	}
	if err := b.Unregister("never-registered"); err != nil {
		t.Fatalf("Unregister of an unknown name: %v", err)
	}
}

func TestUnregisterReportsAFailedRowDelete(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	rec := newRecorder()
	if err := b.Register("app:notes", All, rec.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)
	waitFor(t, "the registration to be persisted", func() bool {
		_, ok, _ := s.GetConsumer("app:notes")
		return ok
	})

	s.setDeleteErr(errors.New("database is having a bad night"))
	if err := b.Unregister("app:notes"); err == nil {
		t.Fatal("Unregister reported success while the row could not be deleted")
	}

	s.setDeleteErr(nil)
	if err := b.Unregister("app:notes"); err != nil {
		t.Fatalf("retried Unregister: %v", err)
	}
	if _, ok, err := s.GetConsumer("app:notes"); err != nil || ok {
		t.Fatalf("the row survived the retry (found=%v, err=%v)", ok, err)
	}
}

func TestReinstallAfterUnregisterStartsAtHead(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	first := newRecorder()
	if err := b.Register("app:notes", All, first.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := b.Publish("one.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the first delivery", func() bool { return first.count() == 1 })
	if err := b.Unregister("app:notes"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	if _, err := b.Publish("while.gone", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	second := newRecorder()
	if err := b.Register("app:notes", All, second.handle); err != nil {
		t.Fatalf("re-Register: %v", err)
	}
	if _, err := b.Publish("after.reinstall", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the post-reinstall fact", func() bool { return second.count() >= 1 })

	names, _ := second.snapshot()
	if len(names) != 1 || names[0] != "after.reinstall" {
		t.Fatalf("delivered %v; a reinstalled consumer must start at head", names)
	}
}

func TestRegisterAfterStartRollsBackWhenRegistrationFails(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	s.setBoundsErr(errors.New("database is having a bad night"))
	if err := b.Register("app:notes", All, func(context.Context, Event) error { return nil }); err == nil {
		t.Fatal("Register reported success while the log could not be read")
	}
	s.setBoundsErr(nil)

	rec := newRecorder()
	if err := b.Register("app:notes", All, rec.handle); err != nil {
		t.Fatalf("Register after a failed attempt: %v; the name was left claimed", err)
	}
	if _, err := b.Publish("after.install", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the retried registration to deliver", func() bool { return rec.count() >= 1 })
}

func TestSetFilterChangesDeliveryAndKeepsTheCursor(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	rec := newRecorder()
	if err := b.Register("app:notes", Filter{"ticket.*"}, rec.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := b.Publish("ticket.commented", "t1", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the first subscription to deliver", func() bool { return rec.count() >= 1 })

	before, ok, err := s.GetConsumer("app:notes")
	if err != nil || !ok {
		t.Fatalf("reading the consumer: found=%v err=%v", ok, err)
	}

	if err := b.SetFilter("app:notes", Filter{"session.*"}); err != nil {
		t.Fatalf("SetFilter: %v", err)
	}

	after, ok, err := s.GetConsumer("app:notes")
	if err != nil || !ok {
		t.Fatalf("reading the consumer after SetFilter: found=%v err=%v", ok, err)
	}
	if after.Cursor != before.Cursor {
		t.Fatalf("cursor moved from %d to %d; changing subscriptions must not move a consumer's position", before.Cursor, after.Cursor)
	}
	if after.Filter != "session.*" {
		t.Fatalf("persisted filter is %q, want session.*; a filter that is not persisted is one a restart forgets", after.Filter)
	}
	if !after.Enabled {
		t.Fatal("SetFilter cleared the enabled bit; the kill switch is not its business")
	}

	if _, err := b.Publish("ticket.commented", "t2", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := b.Publish("session.state.changed", "s1", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the new subscription to deliver", func() bool { return rec.count() >= 2 })

	names, _ := rec.snapshot()
	if len(names) != 2 || names[1] != "session.state.changed" {
		t.Fatalf("delivered %v; want the second delivery to be the newly subscribed fact and the unsubscribed one to be skipped", names)
	}
}

func TestSetFilterRefusesAnUnregisteredConsumer(t *testing.T) {
	b := testBus(t, newMemStore())
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	err := b.SetFilter("app:ghost", All)
	if err == nil {
		t.Fatal("SetFilter on an unregistered consumer returned nil")
	}
	if !strings.Contains(err.Error(), "app:ghost") {
		t.Errorf("the refusal does not name the consumer: %v", err)
	}
}
