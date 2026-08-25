//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// kittySpawn's release handshake exists because a child that writes at spawn time can be
// finished before the test attaches, and a sleep would close that gap on machine speed.
type kittySpawn struct {
	manager *Manager
	id      string
	updates chan PlacementUpdate
	exited  chan struct{}

	mu      sync.Mutex
	output  []byte
	arrived chan struct{}
}

func newKittySpawn(t *testing.T, id, payload string) *kittySpawn {
	t.Helper()
	return newKittySpawnCmd(t, id, payload, "read release; cat %s")
}

func newKittySpawnCmd(t *testing.T, id, payload, script string) *kittySpawn {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	m := NewManager(nil)
	t.Cleanup(m.Shutdown)
	spawn := &kittySpawn{
		manager: m,
		id:      id,
		updates: make(chan PlacementUpdate, 16),
		exited:  make(chan struct{}),
		arrived: make(chan struct{}, 1),
	}
	var once sync.Once
	m.SetExitHandler(func(ExitInfo) { once.Do(func() { close(spawn.exited) }) })

	if err := m.Spawn(SpawnOptions{
		ID:              id,
		CWD:             t.TempDir(),
		Agent:           "probe-kitty",
		ExternalCommand: []string{"/bin/sh", "-c", fmt.Sprintf(script, path)},
		Cols:            40,
		Rows:            12,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	if _, err := m.Attach(id, "test-client",
		func(data []byte, seq uint32) bool {
			spawn.mu.Lock()
			spawn.output = append(spawn.output, data...)
			spawn.mu.Unlock()
			select {
			case spawn.arrived <- struct{}{}:
			default:
			}
			return true
		},
		nil,
		OnPlacements(func(update PlacementUpdate) { spawn.updates <- update }),
	); err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	return spawn
}

// A payload can span several chunks, so "the image was described" is not "the
// child is done writing": a stable watermark needs a marker at the end of it.
func (k *kittySpawn) waitForOutput(t *testing.T, marker string) uint32 {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		k.mu.Lock()
		seen := bytes.Contains(k.output, []byte(marker))
		k.mu.Unlock()
		if seen {
			break
		}
		select {
		case <-k.arrived:
		case <-deadline:
			t.Fatalf("timed out waiting for %q in the session output", marker)
		}
	}

	session, err := k.manager.getSession(k.id)
	if err != nil {
		t.Fatalf("getSession() error: %v", err)
	}
	session.replayMu.Lock()
	defer session.replayMu.Unlock()
	return session.lastReplaySeq
}

// release returns once the child has exited, which is the read loop's own statement
// that every byte it produced has been fed and fanned.
func (k *kittySpawn) release(t *testing.T) {
	t.Helper()
	if err := k.manager.Input(k.id, []byte("\n")); err != nil {
		t.Fatalf("Input() error: %v", err)
	}
	select {
	case <-k.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("the child never exited after being released")
	}
}

func TestSpawnedSessionDescribesImagesByDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	t.Setenv(kittyStorageLimitEnv, "")

	spawn := newKittySpawn(t, "kitty-default", kittyPlaceRGB(80, 16, 32, ""))
	spawn.release(t)

	select {
	case update := <-spawn.updates:
		if len(update.Placements) != 1 || update.Placements[0].ImageID != 80 {
			t.Fatalf("placements = %+v, want the one image the program drew", update.Placements)
		}
	default:
		t.Fatal("no placement was described for an image the program emitted")
	}
	if _, err := spawn.manager.KittyImage(spawn.id, 80); err != nil {
		t.Errorf("KittyImage() error = %v, want the stored pixels", err)
	}
}

func TestSpawnedSessionDescribesNoImagesWhenTheLimitIsZero(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	t.Setenv(kittyStorageLimitEnv, "0")

	spawn := newKittySpawn(t, "kitty-disabled", kittyPlaceRGB(82, 16, 32, ""))
	spawn.release(t)

	select {
	case update := <-spawn.updates:
		t.Fatalf("a placement was described with images disabled: %+v", update)
	default:
	}
	if _, err := spawn.manager.KittyImage(spawn.id, 82); !errors.Is(err, ErrKittyImageNotFound) {
		t.Errorf("KittyImage() error = %v, want ErrKittyImageNotFound: nothing was stored", err)
	}
}

func TestSpawnedSessionDescribesImagesUnderTheStorageOverride(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	t.Setenv(kittyStorageLimitEnv, "16777216")

	spawn := newKittySpawn(t, "kitty-enabled", kittyPlaceRGB(81, 16, 32, ""))
	spawn.release(t)

	var update PlacementUpdate
	select {
	case update = <-spawn.updates:
	default:
		t.Fatal("no placement was described for an image the program emitted")
	}
	if len(update.Placements) != 1 {
		t.Fatalf("placements = %+v, want the one image", update.Placements)
	}
	placement := update.Placements[0]
	if placement.ImageID != 81 {
		t.Fatalf("described image id = %d, want 81", placement.ImageID)
	}

	img, err := spawn.manager.KittyImage(spawn.id, placement.ImageID)
	if err != nil {
		t.Fatalf("KittyImage(%d) error: %v", placement.ImageID, err)
	}
	if img.Width != 16 || img.Height != 32 {
		t.Errorf("fetched image = %dx%d, want 16x32", img.Width, img.Height)
	}
	// Stored images are always raw pixels, so the payload is the full
	// width*height*bpp regardless of how the program encoded it.
	if got, want := len(img.Data), 16*32*3; got != want {
		t.Errorf("fetched pixels = %d bytes, want %d (16x32 RGB, decoded)", got, want)
	}
	if img.Generation != placement.ImageGeneration {
		t.Errorf("fetched generation = %d, want the %d the placement referenced", img.Generation, placement.ImageGeneration)
	}

	if _, err := spawn.manager.KittyImage(spawn.id, 999); !errors.Is(err, ErrKittyImageNotFound) {
		t.Errorf("KittyImage(999) error = %v, want ErrKittyImageNotFound", err)
	}
	if _, err := spawn.manager.KittyImage("no-such-session", 81); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("KittyImage() on an unknown session = %v, want ErrSessionNotFound", err)
	}
}
