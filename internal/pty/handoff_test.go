package pty

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// These run both halves in one process; the exec itself is measured in
// docs/plans/2026-08-22-worker-inplace-upgrade.md.
const echoChild = `printf 'banner one\r\n'; while read line; do printf 'got %s\r\n' "$line"; done`

type collector struct {
	mu      sync.Mutex
	output  []byte
	seqs    []uint32
	arrived chan struct{}
}

func newCollector() *collector {
	return &collector{arrived: make(chan struct{}, 64)}
}

func (c *collector) send(data []byte, seq uint32) bool {
	c.mu.Lock()
	c.output = append(c.output, data...)
	c.seqs = append(c.seqs, seq)
	c.mu.Unlock()
	select {
	case c.arrived <- struct{}{}:
	default:
	}
	return true
}

func (c *collector) waitFor(t *testing.T, marker string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		c.mu.Lock()
		seen := bytes.Contains(c.output, []byte(marker))
		c.mu.Unlock()
		if seen {
			return
		}
		select {
		case <-c.arrived:
		case <-deadline:
			c.mu.Lock()
			got := string(c.output)
			c.mu.Unlock()
			t.Fatalf("timed out waiting for %q; output so far: %q", marker, got)
		}
	}
}

func (c *collector) firstSeq(t *testing.T) uint32 {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.seqs) == 0 {
		t.Fatal("no chunks arrived")
	}
	return c.seqs[0]
}

func spawnEchoSession(t *testing.T, id string) (*Manager, *collector) {
	t.Helper()
	m := NewManager(nil)
	if err := m.Spawn(SpawnOptions{
		ID:              id,
		CWD:             t.TempDir(),
		Agent:           "probe-handoff",
		ExternalCommand: []string{"/bin/sh", "-c", echoChild},
		Cols:            80,
		Rows:            24,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}
	c := newCollector()
	if _, err := m.Attach(id, "before", c.send, nil); err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	c.waitFor(t, "banner one")
	return m, c
}

func screenOf(t *testing.T, m *Manager, id string) string {
	t.Helper()
	session, err := m.getSession(id)
	if err != nil {
		t.Fatalf("getSession(%s): %v", id, err)
	}
	session.replayMu.Lock()
	defer session.replayMu.Unlock()
	if session.ghostty == nil {
		t.Fatal("session has no terminal")
	}
	return strings.TrimRight(session.ghostty.PlainText(), "\n \t")
}

func TestHandoffAndAdoptKeepTheChildRunning(t *testing.T) {
	const id = "handoff-basic"
	before, beforeSub := spawnEchoSession(t, id)
	const pixelW, pixelH = 720, 1080
	if changed, err := before.Resize(id, 80, 24, pixelW, pixelH); err != nil {
		t.Fatalf("Resize() before handoff: %v", err)
	} else if !changed {
		t.Fatal("the measured geometry was not applied before handoff")
	}
	if err := before.Input(id, []byte("first\n")); err != nil {
		t.Fatalf("Input() error: %v", err)
	}
	beforeSub.waitFor(t, "got first")
	wantScreen := screenOf(t, before, id)
	beforePID := func() int {
		session, err := before.getSession(id)
		if err != nil {
			t.Fatalf("getSession: %v", err)
		}
		return session.child.processID()
	}()

	state, err := before.Handoff(id)
	if err != nil {
		t.Fatalf("Handoff() error: %v", err)
	}
	t.Cleanup(before.Shutdown)
	if state.ChildPID != beforePID {
		t.Errorf("handoff child pid = %d, want %d", state.ChildPID, beforePID)
	}
	if len(state.VTDump) == 0 {
		t.Fatal("handoff carried no VT dump")
	}
	if state.LastSeq == 0 {
		t.Error("handoff carried no sequence watermark")
	}
	if state.PixelW != pixelW || state.PixelH != pixelH {
		t.Errorf("handoff pixels = %dx%d, want %dx%d", state.PixelW, state.PixelH, pixelW, pixelH)
	}

	after := NewManager(nil)
	t.Cleanup(after.Shutdown)
	if err := after.Adopt(state); err != nil {
		t.Fatalf("Adopt() error: %v", err)
	}
	if changed, err := after.Resize(id, 80, 24, pixelW, pixelH); err != nil {
		t.Fatalf("Resize() after adopt: %v", err)
	} else if changed {
		t.Fatal("adopt forgot the applied geometry and repeated its resize")
	}
	if got := screenOf(t, after, id); got != wantScreen {
		t.Errorf("the adopted screen differs:\n got %q\nwant %q", got, wantScreen)
	}

	afterSub := newCollector()
	if _, err := after.Attach(id, "after", afterSub.send, nil); err != nil {
		t.Fatalf("Attach() after adopt: %v", err)
	}
	if err := after.Input(id, []byte("second\n")); err != nil {
		t.Fatalf("Input() after adopt: %v", err)
	}
	afterSub.waitFor(t, "got second")

	if got := afterSub.firstSeq(t); got <= state.LastSeq {
		t.Errorf("first seq after the adopt = %d, want > %d (the handoff watermark)", got, state.LastSeq)
	}
}

func TestAdoptedSessionStillReapsItsChild(t *testing.T) {
	// Only the spawning process gets a child's exit status on Unix, so the adopt
	// must keep the pid.
	const id = "handoff-exit"
	before, _ := spawnEchoSession(t, id)
	state, err := before.Handoff(id)
	if err != nil {
		t.Fatalf("Handoff() error: %v", err)
	}
	t.Cleanup(before.Shutdown)

	after := NewManager(nil)
	exits := make(chan ExitInfo, 1)
	after.SetExitHandler(func(info ExitInfo) { exits <- info })
	if err := after.Adopt(state); err != nil {
		t.Fatalf("Adopt() error: %v", err)
	}

	if err := after.Input(id, []byte("\x04")); err != nil {
		t.Fatalf("Input() error: %v", err)
	}
	select {
	case info := <-exits:
		if info.ID != id {
			t.Errorf("exit reported for %q, want %q", info.ID, id)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the adopted session never reported its child's exit")
	}
}

func TestHandoffCarriesCommandBlocks(t *testing.T) {
	const id = "handoff-blocks"
	m := NewManager(nil)
	script := `printf '\033]133;A\007$ \033]133;B\007echo hi\r\n\033]133;C\007hi\r\n\033]133;D;0\007'; ` +
		`printf '\033]133;A\007$ \033]133;B\007READY'; sleep 30`
	if err := m.Spawn(SpawnOptions{
		ID:              id,
		CWD:             t.TempDir(),
		Agent:           "probe-blocks",
		ExternalCommand: []string{"/bin/sh", "-c", script},
		Cols:            80,
		Rows:            24,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}
	c := newCollector()
	if _, err := m.Attach(id, "before", c.send, nil); err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	c.waitFor(t, "READY")

	session, err := m.getSession(id)
	if err != nil {
		t.Fatalf("getSession: %v", err)
	}
	session.replayMu.Lock()
	want := session.wireFeed.snapshotBlocks()
	session.replayMu.Unlock()
	if len(want) < 2 {
		t.Fatalf("fixture produced %d blocks, want the completed one and the pending one", len(want))
	}

	state, err := m.Handoff(id)
	if err != nil {
		t.Fatalf("Handoff() error: %v", err)
	}
	t.Cleanup(m.Shutdown)

	after := NewManager(nil)
	t.Cleanup(after.Shutdown)
	if err := after.Adopt(state); err != nil {
		t.Fatalf("Adopt() error: %v", err)
	}
	adopted, err := after.getSession(id)
	if err != nil {
		t.Fatalf("getSession after adopt: %v", err)
	}
	adopted.replayMu.Lock()
	got := adopted.wireFeed.snapshotBlocks()
	adopted.replayMu.Unlock()

	if len(got) != len(want) {
		t.Fatalf("adopted %d blocks, want %d", len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		if g.ID != w.ID || g.PromptRow != w.PromptRow || g.Pending != w.Pending {
			t.Errorf("block %d = {id %d row %d pending %v}, want {id %d row %d pending %v}",
				i, g.ID, g.PromptRow, g.Pending, w.ID, w.PromptRow, w.Pending)
		}
		if (g.Command == nil) != (w.Command == nil) {
			t.Errorf("block %d command presence differs: got %v want %v", i, g.Command, w.Command)
		} else if g.Command != nil && *g.Command != *w.Command {
			t.Errorf("block %d command = %q, want %q", i, *g.Command, *w.Command)
		}
	}
}

func TestHandoffRefusesAnExitedSession(t *testing.T) {
	const id = "handoff-dead"
	m := NewManager(nil)
	t.Cleanup(m.Shutdown)
	exits := make(chan ExitInfo, 1)
	m.SetExitHandler(func(info ExitInfo) { exits <- info })
	if err := m.Spawn(SpawnOptions{
		ID:              id,
		CWD:             t.TempDir(),
		Agent:           "probe-dead",
		ExternalCommand: []string{"/bin/sh", "-c", "exit 3"},
		Cols:            80,
		Rows:            24,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}
	select {
	case <-exits:
	case <-time.After(10 * time.Second):
		t.Fatal("the fixture child never exited")
	}
	if _, err := m.Handoff(id); err == nil {
		t.Fatal("Handoff() accepted a session whose child is gone")
	}
}
