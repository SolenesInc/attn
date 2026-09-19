//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

import (
	"sync/atomic"
	"testing"
	"time"
)

func newHeldKittySpawn(t *testing.T, id, payload string) *kittySpawn {
	t.Helper()
	return newKittySpawnCmd(t, id, payload, "stty -echo; read release; cat %s; read hold")
}

func releaseAndPlace(t *testing.T, spawn *kittySpawn) PlacementUpdate {
	t.Helper()
	if err := spawn.manager.Input(spawn.id, []byte("\n")); err != nil {
		t.Fatalf("Input() error: %v", err)
	}
	select {
	case update := <-spawn.updates:
		if len(update.Placements) != 1 {
			t.Fatalf("placements = %+v, want the one image the payload emitted", update.Placements)
		}
		return update
	case <-time.After(10 * time.Second):
		t.Fatal("the image was never described")
		return PlacementUpdate{}
	}
}

func TestResizeDescribesPlacementsAfterTheResize(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	t.Setenv(kittyStorageLimitEnv, "16777216")

	const done = "PAYLOAD-END"
	spawn := newHeldKittySpawn(t, "kitty-resize", "\x1b[6;1H"+kittyPlaceRGB(82, 16, 32, "")+done)
	placed := releaseAndPlace(t, spawn)
	before := placed.Placements[0]
	watermark := spawn.waitForOutput(t, done)

	if _, err := spawn.manager.Resize(spawn.id, 40, 4, 0, 0); err != nil {
		t.Fatalf("Resize() error: %v", err)
	}

	var resized PlacementUpdate
	select {
	case resized = <-spawn.updates:
	default:
		t.Fatal("the resize described nothing: the client is left drawing at the old geometry")
	}
	if len(resized.Placements) != 1 {
		t.Fatalf("placements after the resize = %+v, want the image still described", resized.Placements)
	}
	after := resized.Placements[0]

	if after.ViewportRow >= before.ViewportRow {
		t.Errorf("viewport row after the resize = %d, want less than the %d it was placed at: the set was not re-read from the resized grid",
			after.ViewportRow, before.ViewportRow)
	}
	if after.ImageID != before.ImageID {
		t.Errorf("described image id = %d after the resize, want %d", after.ImageID, before.ImageID)
	}
	if resized.Seq != watermark {
		t.Errorf("resize update seq = %d, want the replay watermark %d", resized.Seq, watermark)
	}
}

func TestResizeCostsNothingWithoutPlacements(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	for _, tc := range []struct {
		name    string
		limit   string
		payload string
	}{
		{name: "images live and the program emits none", limit: "", payload: "\x1b[6;1Hplain"},
		{name: "images off and the program emits one", limit: "0", payload: "\x1b[6;1H" + kittyPlaceRGB(83, 16, 32, "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(kittyStorageLimitEnv, tc.limit)

			var reads atomic.Int32
			placementReadHook = func() { reads.Add(1) }
			t.Cleanup(func() { placementReadHook = nil })

			spawn := newHeldKittySpawn(t, "kitty-resize-"+tc.limit+"x", tc.payload)
			if err := spawn.manager.Input(spawn.id, []byte("\n")); err != nil {
				t.Fatalf("Input() error: %v", err)
			}
			if _, err := spawn.manager.Resize(spawn.id, 40, 4, 0, 0); err != nil {
				t.Fatalf("Resize() error: %v", err)
			}
			if _, err := spawn.manager.Resize(spawn.id, 100, 30, 0, 0); err != nil {
				t.Fatalf("Resize() error: %v", err)
			}

			select {
			case update := <-spawn.updates:
				t.Fatalf("a placement was described on a session with no image: %+v", update)
			default:
			}
			if got := reads.Load(); got != 0 {
				t.Errorf("the placement set was read %d times on a session with no images, want never", got)
			}
		})
	}
}
