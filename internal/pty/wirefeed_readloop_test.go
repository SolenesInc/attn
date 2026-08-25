//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

import (
	"bytes"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

func wireSession(t *testing.T, id string, cols, rows int, sub *collectingSubscriber) *os.File {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	ptmx := os.NewFile(uintptr(fds[0]), "ptmx")
	peer := os.NewFile(uintptr(fds[1]), "peer")
	t.Cleanup(func() { _ = ptmx.Close(); _ = peer.Close() })

	term := newKittyTerminal(t, cols, rows, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	s := &Session{
		id:          id,
		cols:        uint16(cols),
		rows:        uint16(rows),
		ptmx:        ptmx,
		child:       &childProcess{cmd: &exec.Cmd{}},
		ghostty:     term,
		wireFeed:    newWireFeeder(term, 0, nil, 0),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now(),
	}
	go s.readLoop(nil, func(string, ...any) {})
	s.addSubscriber("client", sub.send, sub.drop)
	return peer
}

type collectingSubscriber struct {
	mu       sync.Mutex
	received []byte
	empties  int
	changed  chan struct{}
	dropped  chan string
}

func newCollectingSubscriber() *collectingSubscriber {
	return &collectingSubscriber{
		changed: make(chan struct{}, 64),
		dropped: make(chan string, 1),
	}
}

func (c *collectingSubscriber) send(data []byte, _ uint32) bool {
	c.mu.Lock()
	if len(data) == 0 {
		c.empties++
	}
	c.received = append(c.received, data...)
	c.mu.Unlock()
	select {
	case c.changed <- struct{}{}:
	default:
	}
	return true
}

func (c *collectingSubscriber) drop(reason string) {
	select {
	case c.dropped <- reason:
	default:
	}
}

func (c *collectingSubscriber) waitFor(t *testing.T, what string, want func([]byte) bool) []byte {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		c.mu.Lock()
		got := append([]byte(nil), c.received...)
		c.mu.Unlock()
		if want(got) {
			return got
		}
		select {
		case <-c.changed:
		case <-deadline:
			t.Fatalf("timed out waiting for %s; received %q", what, got)
		}
	}
}

func TestReadLoopFansOutTheRewrittenStream(t *testing.T) {
	sub := newCollectingSubscriber()
	peer := wireSession(t, "wire-out", 20, 8, sub)

	const before, after = "\x1b[6;3Hhead", "tail"
	stripped := before + after
	if _, err := peer.Write([]byte(before + kittyPlaceRGB(20, 16, 96, "") + after)); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	got := sub.waitFor(t, "the text after the image", func(b []byte) bool {
		return bytes.Contains(b, []byte(after))
	})

	if bytes.Contains(got, []byte("\x1b_G")) {
		t.Errorf("a kitty APC reached the client: %q", got)
	}
	if !bytes.Contains(got, []byte("head")) {
		t.Errorf("the text before the image was lost: %q", got)
	}
	if string(got) == stripped {
		t.Errorf("nothing was substituted for the image; the client received the stripped stream %q", got)
	}
}

func TestReadLoopSkipsTheFanOutForAHeldEscape(t *testing.T) {
	taken := make(chan struct{}, 8)
	readLoopSeqGapHook = func() {
		select {
		case taken <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { readLoopSeqGapHook = nil })

	sub := newCollectingSubscriber()
	peer := wireSession(t, "wire-held", 20, 8, sub)

	full := kittyPlaceRGB(22, 16, 32, "")
	cut := len(full) - 6
	if _, err := peer.Write([]byte(full[:cut])); err != nil {
		t.Fatalf("peer write: %v", err)
	}
	select {
	case <-taken:
	case <-time.After(5 * time.Second):
		t.Fatal("the read loop never took the half-transmitted image")
	}

	if _, err := peer.Write([]byte(full[cut:] + "done")); err != nil {
		t.Fatalf("peer write: %v", err)
	}
	sub.waitFor(t, "the text after the image", func(b []byte) bool {
		return bytes.Contains(b, []byte("done"))
	})

	sub.mu.Lock()
	empties := sub.empties
	sub.mu.Unlock()
	if empties != 0 {
		t.Errorf("%d empty payloads were fanned out; a chunk with nothing to carry should skip the fan-out", empties)
	}
}

func TestReadLoopDropsSubscribersWhenLayoutCannotBeExpressed(t *testing.T) {
	sub := newCollectingSubscriber()
	peer := wireSession(t, "wire-resync", 20, 6, sub)

	if _, err := peer.Write([]byte("\x1b[?1049h alt0\r\nalt1\r\nalt2\r\nalt3\r\nalt4\r\n\x1b[6;1Halt5")); err != nil {
		t.Fatalf("peer write: %v", err)
	}
	sub.waitFor(t, "the alternate screen to fill", func(b []byte) bool {
		return bytes.Contains(b, []byte("alt5"))
	})

	if _, err := peer.Write([]byte(kittyPlaceRGB(21, 16, 16*8, ""))); err != nil {
		t.Fatalf("peer write: %v", err)
	}

	select {
	case reason := <-sub.dropped:
		if reason != kittyResyncAnchorClamped {
			t.Fatalf("dropped with reason %q, want %q", reason, kittyResyncAnchorClamped)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the client was never dropped; the snapshot re-push is the only thing that resyncs it")
	}
}

func TestForceResyncDropsEverySubscriber(t *testing.T) {
	s := &Session{id: "resync", subscribers: make(map[string]*sessionSubscriber)}
	var mu sync.Mutex
	reasons := map[string]int{}
	for _, id := range []string{"a", "b"} {
		s.addSubscriber(id, func([]byte, uint32) bool { return true }, func(reason string) {
			mu.Lock()
			defer mu.Unlock()
			reasons[id+":"+reason]++
		})
	}

	s.forceResync("kitty_layout_anchor_clamped")

	if len(reasons) != 2 || reasons["a:kitty_layout_anchor_clamped"] != 1 || reasons["b:kitty_layout_anchor_clamped"] != 1 {
		t.Errorf("onDrop calls = %v, want each subscriber told once with the reason", reasons)
	}
	s.subMu.RLock()
	left := len(s.subscribers)
	s.subMu.RUnlock()
	if left != 0 {
		t.Errorf("%d subscribers still attached after a resync", left)
	}

	s.forceResync("kitty_layout_anchor_clamped")
	if len(reasons) != 2 {
		t.Errorf("onDrop calls after a second resync = %v, want the same two", reasons)
	}
}
