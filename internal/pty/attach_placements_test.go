//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

func placementSession(t *testing.T, id string, cols, rows int) (*Session, *os.File, chan PlacementUpdate) {
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

	updates := make(chan PlacementUpdate, 16)
	s.addSubscriber("client",
		func([]byte, uint32) bool { return true },
		nil,
		OnPlacements(func(update PlacementUpdate) { updates <- update }),
	)
	return s, peer, updates
}

func awaitPlacement(t *testing.T, updates <-chan PlacementUpdate, id uint32) PlacementUpdate {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case update := <-updates:
			for _, p := range update.Placements {
				if p.ImageID == id {
					return update
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for image %d to be described", id)
		}
	}
}

func TestAttachPlacementsAreCapturedWithTheWatermark(t *testing.T) {
	defer func() { infoSnapshotHook = nil }()

	s, peer, updates := placementSession(t, "attach-placements", 40, 12)

	if _, err := peer.Write([]byte("\x1b[3;1H" + kittyPlaceRGB(70, 16, 32, ""))); err != nil {
		t.Fatalf("peer write: %v", err)
	}
	placed := awaitPlacement(t, updates, 70)

	injected := make(chan struct{})
	infoSnapshotHook = func() {
		infoSnapshotHook = nil
		if _, err := peer.Write([]byte("\x1b[6;1H" + kittyPlaceRGB(71, 16, 32, ""))); err != nil {
			t.Errorf("peer write inside the snapshot window: %v", err)
			close(injected)
			return
		}
		awaitPlacement(t, updates, 71)
		close(injected)
	}

	info := s.info()
	<-injected

	if len(info.GhosttyPlacements) != 1 {
		t.Fatalf("attach placements = %+v, want only the image the snapshot covers", info.GhosttyPlacements)
	}
	if got := info.GhosttyPlacements[0].ImageID; got != 70 {
		t.Errorf("attach placement image id = %d, want 70", got)
	}
	if info.LastSeq < placed.Seq {
		t.Errorf("attach LastSeq = %d, want at least the %d that placed the image", info.LastSeq, placed.Seq)
	}
	if info.LastSeq >= s.seqCounter.Load() {
		t.Errorf("attach LastSeq = %d claims the chunk injected after the capture (seq counter %d)",
			info.LastSeq, s.seqCounter.Load())
	}

	if _, err := peer.Write([]byte("\x1b[12;1Hscroll\r\n\r\n\r\n")); err != nil {
		t.Fatalf("peer write: %v", err)
	}
	after := awaitPlacement(t, updates, 71)
	if len(after.Placements) != 2 {
		t.Errorf("live placements = %+v, want both images", after.Placements)
	}
}
