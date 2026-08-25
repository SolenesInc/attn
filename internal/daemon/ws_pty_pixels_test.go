package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

type resizeCall struct {
	cols, rows, xpixel, ypixel uint16
}

type recordingResizeBackend struct {
	mu      sync.Mutex
	calls   []resizeCall
	changed bool
}

func (b *recordingResizeBackend) Resize(_ context.Context, _ string, cols, rows, xpixel, ypixel uint16) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, resizeCall{cols, rows, xpixel, ypixel})
	return b.changed, nil
}

func (b *recordingResizeBackend) lastCall(t *testing.T) resizeCall {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.calls) == 0 {
		t.Fatal("the backend was never resized")
	}
	return b.calls[len(b.calls)-1]
}

func (b *recordingResizeBackend) Spawn(context.Context, ptybackend.SpawnOptions) error { return nil }
func (b *recordingResizeBackend) Attach(context.Context, string, string, ...ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{}, nil, nil
}
func (b *recordingResizeBackend) Input(context.Context, string, []byte) error { return nil }
func (b *recordingResizeBackend) SetTheme(context.Context, string, pty.TerminalTheme) error {
	return nil
}
func (b *recordingResizeBackend) Kill(context.Context, string, syscall.Signal) error { return nil }
func (b *recordingResizeBackend) Remove(context.Context, string) error               { return nil }
func (b *recordingResizeBackend) SessionIDs(context.Context) []string                { return nil }
func (b *recordingResizeBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	return ptybackend.RecoveryReport{}, nil
}
func (b *recordingResizeBackend) Shutdown(context.Context) error { return nil }

func newResizeDaemon(t *testing.T) (*Daemon, *recordingResizeBackend, *broadcastCapture) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)
	backend := &recordingResizeBackend{changed: true}
	d.ptyBackend = backend
	return d, backend, captureBroadcasts(d)
}

func resizedEvent(t *testing.T, capture *broadcastCapture) protocol.WebSocketEvent {
	t.Helper()
	for _, event := range capture.snapshot() {
		if event.Event == protocol.EventPtyResized {
			return event
		}
	}
	t.Fatal("no pty_resized reached the wire")
	return protocol.WebSocketEvent{}
}

func TestPtyResizeCarriesPixelGeometryToTheBackendAndTheEcho(t *testing.T) {
	d, backend, capture := newResizeDaemon(t)

	d.handlePtyResize(nil, &protocol.PtyResizeMessage{
		ID: "sess-1", Cols: 40, Rows: 12,
		Xpixel: protocol.Ptr(720), Ypixel: protocol.Ptr(540),
	})

	if got := backend.lastCall(t); got != (resizeCall{40, 12, 720, 540}) {
		t.Fatalf("the backend was resized with %+v, want the reported pixels", got)
	}
	event := resizedEvent(t, capture)
	if protocol.Deref(event.Xpixel) != 720 || protocol.Deref(event.Ypixel) != 540 {
		t.Fatalf("pty_resized echoed %v x %v pixels, want 720 x 540", event.Xpixel, event.Ypixel)
	}
}

func TestPtyResizeDoesNotBroadcastAnUnchangedGeometry(t *testing.T) {
	d, backend, capture := newResizeDaemon(t)
	backend.changed = false

	d.handlePtyResize(nil, &protocol.PtyResizeMessage{
		ID: "sess-1", Cols: 40, Rows: 12,
		Xpixel: protocol.Ptr(720), Ypixel: protocol.Ptr(540),
	})

	if got := backend.lastCall(t); got != (resizeCall{40, 12, 720, 540}) {
		t.Fatalf("the backend was resized with %+v, want the reported geometry", got)
	}
	for _, event := range capture.snapshot() {
		if event.Event == protocol.EventPtyResized {
			t.Fatal("unchanged geometry reached the wire")
		}
	}
}

func TestPtyResizeLeavesPixelsAbsentWhenNoneWereReported(t *testing.T) {
	d, backend, capture := newResizeDaemon(t)

	d.handlePtyResize(nil, &protocol.PtyResizeMessage{ID: "sess-1", Cols: 40, Rows: 12})

	if got := backend.lastCall(t); got != (resizeCall{40, 12, 0, 0}) {
		t.Fatalf("the backend was resized with %+v, want zero pixels", got)
	}
	event := resizedEvent(t, capture)
	if event.Xpixel != nil || event.Ypixel != nil {
		t.Fatalf("pty_resized echoed %v x %v pixels, want both fields left off", event.Xpixel, event.Ypixel)
	}
}

func TestPtyResizeDropsPixelsTheKernelCannotHold(t *testing.T) {
	// ws_xpixel is uint16: 70000 would truncate to 4464 and read as a real
	// measurement all the way down to the emitter.
	d, backend, capture := newResizeDaemon(t)

	d.handlePtyResize(nil, &protocol.PtyResizeMessage{
		ID: "sess-1", Cols: 40, Rows: 12,
		Xpixel: protocol.Ptr(70000), Ypixel: protocol.Ptr(540),
	})

	if got := backend.lastCall(t); got != (resizeCall{40, 12, 0, 0}) {
		t.Fatalf("the backend was resized with %+v, want the unusable geometry dropped", got)
	}
	event := resizedEvent(t, capture)
	if event.Xpixel != nil || event.Ypixel != nil {
		t.Fatalf("pty_resized echoed %v x %v pixels, want both fields left off", event.Xpixel, event.Ypixel)
	}
	if got := backend.lastCall(t); got.cols != 40 || got.rows != 12 {
		t.Fatalf("the grid was not applied: %+v", got)
	}
}

func TestPtyResizeDropsASingleAxis(t *testing.T) {
	d, backend, _ := newResizeDaemon(t)

	d.handlePtyResize(nil, &protocol.PtyResizeMessage{
		ID: "sess-1", Cols: 40, Rows: 12, Xpixel: protocol.Ptr(720),
	})

	if got := backend.lastCall(t); got != (resizeCall{40, 12, 0, 0}) {
		t.Fatalf("the backend was resized with %+v, want a half-measured pane dropped", got)
	}
}
