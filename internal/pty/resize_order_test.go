//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

import (
	"sync"
	"testing"
)

func TestResizeIsDeliveredAfterBytesAlreadyInFlight(t *testing.T) {
	spawn := newKittySpawnCmd(t, "resize-order", "before", "stty -echo; printf READY; read release; cat %s; read hold")
	spawn.waitForOutput(t, "READY")
	session, err := spawn.manager.getSession(spawn.id)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	events := make(chan string, 2)
	session.addSubscriber(
		"resize-order",
		func(data []byte, _ uint32) bool {
			events <- "output:" + string(data)
			return true
		},
		nil,
		OnResize(func(ResizeUpdate) { events <- "resize" }),
	)

	readAdmitted := make(chan struct{})
	resizeWaiting := make(chan struct{})
	releaseRead := make(chan struct{})
	var once sync.Once
	readHook := func() {
		once.Do(func() {
			close(readAdmitted)
			<-releaseRead
		})
	}
	var resizeOnce sync.Once
	resizeHook := func() { resizeOnce.Do(func() { close(resizeWaiting) }) }
	readLoopAdmissionGapHook.Store(&readHook)
	resizeAdmissionHook.Store(&resizeHook)
	t.Cleanup(func() {
		readLoopAdmissionGapHook.Store(nil)
		resizeAdmissionHook.Store(nil)
	})

	if err := spawn.manager.Input(spawn.id, []byte("\n")); err != nil {
		t.Fatalf("release first output: %v", err)
	}
	<-readAdmitted
	resizeDone := make(chan error, 1)
	go func() {
		_, err := spawn.manager.Resize(spawn.id, 80, 12, 0, 0)
		resizeDone <- err
	}()
	<-resizeWaiting
	close(releaseRead)
	if err := <-resizeDone; err != nil {
		t.Fatalf("resize: %v", err)
	}

	if got := <-events; got != "output:before" {
		t.Fatalf("first event = %q, want the bytes already being processed", got)
	}
	if got := <-events; got != "resize" {
		t.Fatalf("second event = %q, want the resize boundary", got)
	}
}

func TestUnchangedResizeStillReachesSubscribers(t *testing.T) {
	spawn := newKittySpawnCmd(t, "resize-noop", "", "read hold")
	session, err := spawn.manager.getSession(spawn.id)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	info := session.subscriptionInfo()
	resized := make(chan ResizeUpdate, 1)
	session.addSubscriber(
		"resize-noop",
		func([]byte, uint32) bool { return true },
		nil,
		OnResize(func(update ResizeUpdate) { resized <- update }),
	)

	changed, err := spawn.manager.Resize(spawn.id, info.Cols, info.Rows, 0, 0)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if changed {
		t.Fatal("unchanged resize reported a model change")
	}
	update := <-resized
	if update.Cols != info.Cols || update.Rows != info.Rows {
		t.Fatalf("resize = %+v, want %dx%d", update, info.Cols, info.Rows)
	}
}
