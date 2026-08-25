//go:build darwin && arm64

package pty

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

// Run under `go test -race ./internal/pty`: without it these races prove nothing.

func newLifetimeRaceSession(t *testing.T, id string, cols, rows int) (*Session, *os.File) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })

	gt, err := ghosttyvt.New(cols, rows, ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("ghosttyvt.New: %v", err)
	}

	s := &Session{
		id:          id,
		cols:        uint16(cols),
		rows:        uint16(rows),
		ptmx:        r,
		child:       &childProcess{cmd: &exec.Cmd{}},
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now(),
	}
	s.ghostty = gt
	s.wireFeed = &wireFeeder{term: gt, blocks: &blockFeeder{term: gt, table: newBlockTable()}}
	return s, w
}

func TestAttachRacesRemove(t *testing.T) {
	const cols, rows = 80, 24
	refBase := ghosttyvt.LiveTrackedRefs()

	for i := 0; i < 40; i++ {
		func() {
			s, w := newLifetimeRaceSession(t, fmt.Sprintf("attach-remove-%d", i), cols, rows)
			go s.readLoop(nil, func(string, ...any) {})

			for j := 0; j < 8; j++ {
				if _, err := fmt.Fprintf(w, "\x1b]133;A\x07MARK-%02d\r\n", j); err != nil {
					t.Errorf("pipe write: %v", err)
					return
				}
			}

			var wg sync.WaitGroup
			start := make(chan struct{})

			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for k := 0; k < 20; k++ {
					info := s.info()
					for _, b := range info.GhosttyBlocks {
						if b.PromptRow < 0 {
							t.Errorf("negative prompt row %d from a torn-down session", b.PromptRow)
							return
						}
					}
				}
			}()

			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				s.closePTY()
			}()

			close(start)
			wg.Wait()

			after := s.info()
			if len(after.GhosttySnapshot) != 0 {
				t.Fatalf("post-teardown snapshot returned %d bytes, want none", len(after.GhosttySnapshot))
			}
			if len(after.GhosttyBlocks) != 0 {
				t.Fatalf("post-teardown snapshot returned %d blocks, want none", len(after.GhosttyBlocks))
			}
		}()
	}

	if got := ghosttyvt.LiveTrackedRefs(); got != refBase {
		t.Fatalf("tracked refs leaked across attach/remove races: live=%d baseline=%d", got, refBase)
	}
}

func TestResizeRacesSnapshot(t *testing.T) {
	const cols, rows = 80, 24
	refBase := ghosttyvt.LiveTrackedRefs()

	for i := 0; i < 20; i++ {
		func() {
			s, w := newLifetimeRaceSession(t, fmt.Sprintf("resize-snapshot-%d", i), cols, rows)
			go s.readLoop(nil, func(string, ...any) {})

			for j := 0; j < 12; j++ {
				if _, err := fmt.Fprintf(w, "\x1b]133;A\x07MARK-%02d\r\nfiller-%02d\r\n", j, j); err != nil {
					t.Errorf("pipe write: %v", err)
					return
				}
			}

			var wg sync.WaitGroup
			start := make(chan struct{})

			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				widths := []uint16{40, 120, 60, 100, 30, 80}
				for _, cw := range widths {
					_, _ = s.resize(cw, rows, 0, 0)
				}
			}()

			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for k := 0; k < 30; k++ {
					info := s.info()
					for _, b := range info.GhosttyBlocks {
						if b.PromptRow < 0 || b.PromptRow >= int32(info.Rows) {
							t.Errorf("block %d row %d outside its own snapshot grid (%dx%d)",
								b.ID, b.PromptRow, info.Cols, info.Rows)
							return
						}
					}
				}
			}()

			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				s.closePTY()
			}()

			close(start)
			wg.Wait()
		}()
	}

	if got := ghosttyvt.LiveTrackedRefs(); got != refBase {
		t.Fatalf("tracked refs leaked across resize/snapshot races: live=%d baseline=%d", got, refBase)
	}
}
