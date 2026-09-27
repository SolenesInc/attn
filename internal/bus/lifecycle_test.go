package bus

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

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
	if err := b.Register("test:slow", All, handler); err != nil {
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
	go func() { done <- b.Unregister("test:slow") }()

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
	if _, ok, err := s.GetConsumer("test:slow"); err != nil || ok {
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
	if err := b.Register("test:notes", All, handler); err != nil {
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
	go func() { done <- b.Unregister("test:notes") }()
	waitFor(t, "the consumer to be retired", d.isRetired)

	if _, ok, err := s.GetConsumer("test:notes"); err != nil || !ok {
		t.Fatalf("expected the row to still exist mid-unregister (found=%v, err=%v)", ok, err)
	}
	if err := b.Register("test:notes", All, func(context.Context, Event) error { return nil }); err == nil {
		t.Fatal("Register was served while the name was being unregistered")
	}

	releaseHandler()
	if err := <-done; err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	rec := newRecorder()
	if err := b.Register("test:notes", All, rec.handle); err != nil {
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

	d := b.newDurable("test:gone", All, nil, func(context.Context, Event) error { return nil })
	t.Cleanup(d.cancel)
	d.retire()

	if err := b.advance(d, 7); err != nil {
		t.Fatalf("a late cursor advance must be dropped silently, got %v", err)
	}
	if _, ok, err := s.GetConsumer("test:gone"); err != nil || ok {
		t.Fatalf("a retired consumer moved or recreated its row (found=%v, err=%v)", ok, err)
	}

	d.recordFailure("handler boom", 3)
	if reason, failures := d.stallReason(), d.drainFailures(); reason != "" || failures != 0 {
		t.Fatalf("a retired consumer recorded a stall (%q, %d attempts)", reason, failures)
	}
}

func TestUnregisterReportsAFailedRowDelete(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	rec := newRecorder()
	if err := b.Register("test:notes", All, rec.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)
	waitFor(t, "the registration to be persisted", func() bool {
		_, ok, _ := s.GetConsumer("test:notes")
		return ok
	})

	s.setDeleteErr(errors.New("database is having a bad night"))
	if err := b.Unregister("test:notes"); err == nil {
		t.Fatal("Unregister reported success while the row could not be deleted")
	}

	s.setDeleteErr(nil)
	if err := b.Unregister("test:notes"); err != nil {
		t.Fatalf("retried Unregister: %v", err)
	}
	if _, ok, err := s.GetConsumer("test:notes"); err != nil || ok {
		t.Fatalf("the row survived the retry (found=%v, err=%v)", ok, err)
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
	if err := b.Register("test:notes", All, func(context.Context, Event) error { return nil }); err == nil {
		t.Fatal("Register reported success while the log could not be read")
	}
	s.setBoundsErr(nil)

	rec := newRecorder()
	if err := b.Register("test:notes", All, rec.handle); err != nil {
		t.Fatalf("Register after a failed attempt: %v; the name was left claimed", err)
	}
	if _, err := b.Publish("after.install", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the retried registration to deliver", func() bool { return rec.count() >= 1 })
}
