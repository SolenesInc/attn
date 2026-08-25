//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

import (
	"testing"
)

// Bounds where an epoched identity may land: past any stamp a process reaches, and
// inside what a JS Number keys exactly.
const (
	kittyEpochFloor   = uint64(1) << 32
	kittyEpochCeiling = uint64(1) << 53
)

func TestKittyIdentityIsTheSameAtEveryExit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	t.Setenv(kittyStorageLimitEnv, "16777216")

	const done = "PAYLOAD-END"
	spawn := newHeldKittySpawn(t, "kitty-identity", "\x1b[6;1H"+kittyPlaceRGB(84, 16, 32, "")+done)
	placed := releaseAndPlace(t, spawn)
	live := placed.Placements[0].ImageGeneration
	spawn.waitForOutput(t, done)

	if live < kittyEpochFloor || live >= kittyEpochCeiling {
		t.Fatalf("described generation = %d, want an epoched identity in [2^32, 2^53): the fold never happened", live)
	}

	attached, err := spawn.manager.Attach(spawn.id, "identity-client",
		func([]byte, uint32) bool { return true }, nil)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	if len(attached.GhosttyPlacements) != 1 {
		t.Fatalf("attach placements = %+v, want the one image", attached.GhosttyPlacements)
	}
	if got := attached.GhosttyPlacements[0].ImageGeneration; got != live {
		t.Errorf("attach snapshot generation = %d, want the %d the live description carried", got, live)
	}

	if _, err := spawn.manager.Resize(spawn.id, 40, 4, 0, 0); err != nil {
		t.Fatalf("Resize() error: %v", err)
	}
	var resized PlacementUpdate
	select {
	case resized = <-spawn.updates:
	default:
		t.Fatal("the resize described nothing")
	}
	if len(resized.Placements) != 1 {
		t.Fatalf("placements after the resize = %+v, want the image still described", resized.Placements)
	}
	if got := resized.Placements[0].ImageGeneration; got != live {
		t.Errorf("resize generation = %d, want the %d the live description carried", got, live)
	}

	img, err := spawn.manager.KittyImage(spawn.id, 84)
	if err != nil {
		t.Fatalf("KittyImage(84) error: %v", err)
	}
	if img.Generation != live {
		t.Errorf("served image generation = %d, want the %d its placement named: the pull can never be correlated back", img.Generation, live)
	}
}

// Ghostty's stamps are unique process-wide, so two terminals in ONE test binary never
// collide (measured: with the fold removed, these sessions still describe 3 and 5).
func TestKittyIdentitiesFromDifferentWorkersNeverCollide(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	t.Setenv(kittyStorageLimitEnv, "16777216")

	const imageID = 85
	payload := "\x1b[3;1H" + kittyPlaceRGB(imageID, 16, 32, "")

	describe := func(sessionID string) (generation, epoch uint64) {
		t.Helper()
		spawn := newKittySpawn(t, sessionID, payload)
		spawn.release(t)
		session, err := spawn.manager.getSession(sessionID)
		if err != nil {
			t.Fatalf("%s: getSession() error: %v", sessionID, err)
		}
		select {
		case update := <-spawn.updates:
			if len(update.Placements) != 1 {
				t.Fatalf("%s: placements = %+v, want the one image", sessionID, update.Placements)
			}
			return update.Placements[0].ImageGeneration, session.kittyEpoch
		default:
			t.Fatalf("%s: the image was never described", sessionID)
			return 0, 0
		}
	}

	first, firstEpoch := describe("kitty-worker-one")
	second, secondEpoch := describe("kitty-worker-two")

	if firstEpoch == secondEpoch {
		t.Errorf("both workers minted epoch %d: a replacement worker would describe the dead one's identities",
			firstEpoch)
	}
	for _, got := range []uint64{first, second} {
		if got < kittyEpochFloor || got >= kittyEpochCeiling {
			t.Errorf("described generation = %d, want an epoched identity in [2^32, 2^53)", got)
		}
	}
}
