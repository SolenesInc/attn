package pty

import (
	"os/exec"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/ghosttyvt"
)

// A pty-worker outlives an install, so its bytes can reach a client whose decoder is a
// different build.
func TestInfoStampsSnapshotFormat(t *testing.T) {
	newSession := func() *Session {
		return &Session{
			id:          "snapshot-format",
			cols:        80,
			rows:        24,
			child:       &childProcess{cmd: &exec.Cmd{}},
			subscribers: make(map[string]*sessionSubscriber),
			running:     true,
			exited:      make(chan struct{}),
			startedAt:   time.Now(),
		}
	}

	gt, err := ghosttyvt.New(80, 24, ghosttyvt.Options{})
	if err != nil {
		t.Skipf("ghostty terminal unavailable on this platform: %v", err)
	}
	t.Cleanup(gt.Close)

	s := newSession()
	s.ghostty = gt
	info := s.info()
	if len(info.GhosttySnapshot) == 0 {
		t.Fatal("expected a snapshot from a session with a ghostty terminal")
	}
	if info.GhosttySnapshotFormat != buildinfo.SnapshotFormat {
		t.Fatalf("snapshot format = %q, want %q", info.GhosttySnapshotFormat, buildinfo.SnapshotFormat)
	}

	bare := newSession()
	if got := bare.info().GhosttySnapshotFormat; got != "" {
		t.Fatalf("snapshot format without a terminal = %q, want empty", got)
	}
}
