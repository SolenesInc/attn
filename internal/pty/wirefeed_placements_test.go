//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

import (
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

type placementRecorder struct {
	feed     *wireFeeder
	term     *ghosttyvt.Terminal
	updates  []PlacementUpdate
	observed int
	seq      uint32
}

func newPlacementRecorder(t *testing.T, cols, rows int, limit uint64) *placementRecorder {
	t.Helper()
	term := newKittyTerminal(t, cols, rows, ghosttyvt.Options{KittyImageStorageLimit: limit})
	feed := newWireFeeder(term, 0, nil, 0)
	if feed == nil {
		t.Fatal("newWireFeeder returned nil for a live terminal")
	}
	t.Cleanup(feed.close)

	rec := &placementRecorder{feed: feed, term: term}
	placementReadHook = func() { rec.observed++ }
	t.Cleanup(func() { placementReadHook = nil })
	return rec
}

func (r *placementRecorder) write(chunk string) {
	r.seq++
	r.feed.feed([]byte(chunk))
	if set, changed := r.feed.changedPlacements(); changed {
		r.updates = append(r.updates, PlacementUpdate{Seq: r.seq, Placements: set})
	}
}

func (r *placementRecorder) last(t *testing.T) PlacementUpdate {
	t.Helper()
	if len(r.updates) == 0 {
		t.Fatal("no placement update was produced")
	}
	return r.updates[len(r.updates)-1]
}

func TestPlacementUpdateDescribesAPlacedImage(t *testing.T) {
	rec := newPlacementRecorder(t, 20, 8, mirrorStorageLimit)

	rec.write("\x1b[3;5Hbefore the image")
	rec.write(kittyPlaceRGB(40, 16, 32, ""))

	if len(rec.updates) != 1 {
		t.Fatalf("placement updates = %+v, want exactly the one the image produced", rec.updates)
	}
	update := rec.updates[0]
	if update.Seq != 2 {
		t.Errorf("update seq = %d, want 2: the set describes the grid the second chunk produced", update.Seq)
	}
	if len(update.Placements) != 1 {
		t.Fatalf("placements = %+v, want the one image", update.Placements)
	}
	if got := update.Placements[0].ImageID; got != 40 {
		t.Errorf("described image id = %d, want 40", got)
	}
	// Ghostty reports 0x0 cells until something makes it resolve a placement's
	// footprint, so assert pixel size, not GridCols/GridRows.
	if got := update.Placements[0].PixelHeight; got != 32 {
		t.Errorf("described height = %d px, want the 32 the image was transmitted at", got)
	}
	if got := update.Placements[0].PixelWidth; got != 16 {
		t.Errorf("described width = %d px, want the 16 the image was transmitted at", got)
	}
}

func TestPlacementUpdateFollowsAScrollOnPlainOutput(t *testing.T) {
	rec := newPlacementRecorder(t, 20, 8, mirrorStorageLimit)

	rec.write("\x1b[7;1H")
	rec.write(kittyPlaceRGB(41, 16, 32, ""))
	placed := rec.last(t)
	if len(placed.Placements) != 1 {
		t.Fatalf("placements after the image = %+v, want one", placed.Placements)
	}
	startRow := placed.Placements[0].ViewportRow

	rec.write("\r\nplain output\r\nthat scrolls\r\n")

	moved := rec.last(t)
	if len(rec.updates) != 2 {
		t.Fatalf("updates = %d, want the placement and the scroll that moved it", len(rec.updates))
	}
	if len(moved.Placements) != 1 {
		t.Fatalf("placements after the scroll = %+v, want the image still described", moved.Placements)
	}
	if got := moved.Placements[0].ViewportRow; got >= startRow {
		t.Errorf("viewport row after scrolling = %d, want less than the %d it was placed at", got, startRow)
	}
	if moved.Seq != 3 {
		t.Errorf("update seq = %d, want 3: the scroll's own chunk", moved.Seq)
	}
}

func TestPlacementUpdateIsSilentWhenNothingMoved(t *testing.T) {
	rec := newPlacementRecorder(t, 40, 12, mirrorStorageLimit)

	rec.write("\x1b[2;1H")
	rec.write(kittyPlaceRGB(42, 16, 32, ""))
	if len(rec.updates) != 1 {
		t.Fatalf("updates after the image = %d, want 1", len(rec.updates))
	}

	rec.write("\x1b[9;1Hstatus line")
	rec.write("\x1b[10;1Hanother line")

	if len(rec.updates) != 1 {
		t.Errorf("updates = %+v, want only the one the image produced: unchanged output describes nothing", rec.updates)
	}
}

func TestPlacementUpdateSendsTheEmptySetWhenTheLastImageGoes(t *testing.T) {
	rec := newPlacementRecorder(t, 20, 8, mirrorStorageLimit)

	rec.write("\x1b[2;2Hkeep")
	rec.write(kittyPlaceRGB(43, 16, 32, ""))
	rec.write("\x1b_Ga=d\x1b\\")

	if len(rec.updates) != 2 {
		t.Fatalf("updates = %+v, want the placement and its removal", rec.updates)
	}
	cleared := rec.updates[1]
	if len(cleared.Placements) != 0 {
		t.Errorf("placements after the delete = %+v, want the empty set", cleared.Placements)
	}
	if cleared.Seq != 3 {
		t.Errorf("update seq = %d, want the delete's own chunk (3)", cleared.Seq)
	}
}

func TestPlacementsCostNothingWhileKittyIsDisabled(t *testing.T) {
	rec := newPlacementRecorder(t, 20, 8, 0)

	rec.write("plain output\r\n")
	rec.write(kittyPlaceRGB(44, 16, 32, ""))
	rec.write("more output\r\nand more\r\n")

	if len(rec.updates) != 0 {
		t.Errorf("placement updates = %+v with images disabled, want none", rec.updates)
	}
	if rec.observed != 0 {
		t.Errorf("the placement set was read %d times with images disabled, want never", rec.observed)
	}
}

func TestPlacementSetTurnsOverOnAScreenSwitch(t *testing.T) {
	rec := newPlacementRecorder(t, 20, 8, mirrorStorageLimit)

	rec.write("\x1b[2;1H")
	rec.write(kittyPlaceRGB(45, 16, 32, ""))
	if got := rec.last(t).Placements; len(got) != 1 {
		t.Fatalf("placements on the primary screen = %+v, want the image", got)
	}

	rec.write("\x1b[?1049h")
	if got := rec.last(t).Placements; len(got) != 0 {
		t.Fatalf("placements on the alternate screen = %+v, want none: the image is on the other screen", got)
	}

	rec.write("\x1b[?1049l")
	back := rec.last(t)
	if len(back.Placements) != 1 {
		t.Fatalf("placements after returning = %+v, want the primary screen's image back", back.Placements)
	}
	if got := back.Placements[0].ImageID; got != 45 {
		t.Errorf("described image id = %d after returning, want 45", got)
	}
}
