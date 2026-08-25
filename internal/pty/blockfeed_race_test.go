//go:build darwin && arm64

package pty

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

var testPromptMark = []byte("\x1b]133;A\x07")

// under replayMu (blockFeeder's contract), so no internal locking.
type pinningBlockTable struct {
	refs []blockRef
}

func (t *pinningBlockTable) ApplyMarker(_ osc133Marker, ref blockRef, altScreen bool) {
	if ref == nil {
		return
	}
	if altScreen {
		ref.Free()
		return
	}
	t.refs = append(t.refs, ref)
}

func (t *pinningBlockTable) SnapshotBlocks() []AttachBlockData {
	var out []AttachBlockData
	for i, r := range t.refs {
		if _, y, ok := r.ScreenPoint(); ok {
			out = append(out, AttachBlockData{ID: uint64(i + 1), PromptRow: int32(y)})
		}
	}
	return out
}

func (t *pinningBlockTable) Restore(blocks []AttachBlockData, pin func(x, y int) blockRef) {
	for _, b := range blocks {
		if ref := pin(0, int(b.PromptRow)); ref != nil {
			t.refs = append(t.refs, ref)
		}
	}
}

func (t *pinningBlockTable) Close() {
	for _, r := range t.refs {
		r.Free()
	}
	t.refs = nil
}

func TestBlockSnapshotAtomicity(t *testing.T) {
	const cols, rows = 80, 24
	const marks = 150

	refBase := ghosttyvt.LiveTrackedRefs()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })

	gt, err := ghosttyvt.New(cols, rows, ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("ghosttyvt.New: %v", err)
	}
	table := &pinningBlockTable{}
	s := &Session{
		id:          "block-race",
		cols:        cols,
		rows:        rows,
		ptmx:        r,
		child:       &childProcess{cmd: &exec.Cmd{}},
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now(),
	}
	s.ghostty = gt
	s.wireFeed = &wireFeeder{term: gt, blocks: &blockFeeder{term: gt, table: table}}
	go s.readLoop(nil, func(string, ...any) {})

	// Without the pacing the read loop takes one coalesced pipe read and the
	// snapshotter never observes a mid-stream state.
	go func() {
		for i := 0; i < marks; i++ {
			line := fmt.Sprintf("\x1b]133;A\x07MARK-%04d\r\nfiller-%04d-a\r\nfiller-%04d-b\r\n", i, i, i)
			if _, werr := w.Write([]byte(line)); werr != nil {
				t.Errorf("pipe write: %v", werr)
				return
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()

	restoredLines := func(info AttachInfo) []string {
		restored, rerr := ghosttyvt.Restore(info.GhosttySnapshot, ghosttyvt.Options{})
		if rerr != nil {
			t.Fatalf("ghosttyvt.Restore: %v", rerr)
		}
		defer restored.Close()
		lines := strings.Split(restored.PlainText(), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " ")
		}
		return lines
	}
	assertBlocksIndexDump := func(info AttachInfo) {
		t.Helper()
		lines := restoredLines(info)
		for _, b := range info.GhosttyBlocks {
			y := int(b.PromptRow)
			if y < 0 || y >= len(lines) {
				t.Fatalf("block %d row %d out of range (restored dump has %d rows)", b.ID, y, len(lines))
			}
			if !strings.HasPrefix(lines[y], "MARK-") {
				t.Fatalf("block %d row %d points at %q in its own dump — snapshot triple not atomic", b.ID, y, lines[y])
			}
		}
	}

	partialChecks := 0
	deadline := time.Now().Add(10 * time.Second)
	for {
		info := s.info()
		if n := len(info.GhosttyBlocks); n > 0 {
			assertBlocksIndexDump(info)
			if n < marks {
				partialChecks++
			} else {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for all marks to reach the block table")
		}
	}
	if partialChecks == 0 {
		t.Fatal("every snapshot saw the settled table; the race was never exercised")
	}

	settled := s.info()
	if len(settled.GhosttyBlocks) != marks {
		t.Fatalf("settled snapshot has %d blocks, want %d", len(settled.GhosttyBlocks), marks)
	}
	lines := restoredLines(settled)
	for i, b := range settled.GhosttyBlocks {
		want := fmt.Sprintf("MARK-%04d", i)
		if got := lines[int(b.PromptRow)]; got != want {
			t.Fatalf("settled block %d resolves to row %d = %q, want %q", b.ID, b.PromptRow, got, want)
		}
	}

	_ = w.Close()
	select {
	case <-s.exited:
	case <-time.After(2 * time.Second):
		t.Fatal("read loop did not exit after pipe close")
	}
	s.closePTY()
	if got := ghosttyvt.LiveTrackedRefs(); got != refBase {
		t.Fatalf("tracked refs leaked: live=%d baseline=%d", got, refBase)
	}
}
