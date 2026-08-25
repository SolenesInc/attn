//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

import (
	"fmt"
	"strings"
	"testing"
)

// PlainText emits one line per grid row (unwrap=false), so the slice index IS
// the SCREEN-space y coordinate.
func screenLines(t *Terminal) []string {
	lines := strings.Split(t.PlainText(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

func feedLines(t *Terminal, from, to int) {
	var b strings.Builder
	for i := from; i < to; i++ {
		fmt.Fprintf(&b, "line-%04d\r\n", i)
	}
	t.Write([]byte(b.String()))
}

func TestTrackedRefLeakAccounting(t *testing.T) {
	base := LiveTrackedRefs()
	term, err := New(80, 10, Options{ScrollbackBytes: 50})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()

	r1 := term.TrackCursor()
	r2 := term.TrackCursor()
	if r1 == nil || r2 == nil {
		t.Fatal("TrackCursor returned nil")
	}
	if got := LiveTrackedRefs(); got != base+2 {
		t.Fatalf("after 2 pins: live=%d want %d", got, base+2)
	}
	r1.Free()
	r1.Free()
	if got := LiveTrackedRefs(); got != base+1 {
		t.Fatalf("after freeing one ref (twice): live=%d want %d", got, base+1)
	}
	r2.Free()
	if got := LiveTrackedRefs(); got != base {
		t.Fatalf("after freeing all: live=%d want %d", got, base)
	}
}

func TestSpikeTrackedRefFollowsScrollPruneReflow(t *testing.T) {
	term, err := New(80, 10, Options{ScrollbackBytes: 50})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()

	feedLines(term, 0, 5)
	term.Write([]byte("MARK-PROMPT $ echo hello\r\n"))
	ref := term.TrackCursor()
	if ref == nil {
		t.Fatal("TrackCursor returned nil")
	}
	defer ref.Free()
	term.Write([]byte("MARK-OUTPUT-START\r\n"))

	x, y0, ok := ref.ScreenPoint()
	if !ok {
		t.Fatal("ref lost immediately")
	}
	if x != 0 {
		t.Fatalf("expected x=0 at pin time, got %d", x)
	}
	if got := screenLines(term)[y0]; got != "MARK-OUTPUT-START" {
		t.Fatalf("pin row mismatch: y=%d text=%q", y0, got)
	}

	feedLines(term, 100, 130)
	_, y1, ok := ref.ScreenPoint()
	if !ok {
		t.Fatal("ref lost after in-cap scroll")
	}
	if got := screenLines(term)[y1]; got != "MARK-OUTPUT-START" {
		t.Fatalf("after scroll: y=%d text=%q", y1, got)
	}

	term.Resize(40, 10)
	_, y2, ok := ref.ScreenPoint()
	if !ok {
		t.Fatal("ref lost after reflow")
	}
	if got := screenLines(term)[y2]; !strings.HasPrefix(got, "MARK-OUTPUT-START") {
		t.Fatalf("after reflow: y=%d text=%q", y2, got)
	}

	// Pruning is page-granular and lazy (probe: with cap=50 it fires between
	// ~1k and ~5k rows), so feed enough to guarantee the marked page is gone.
	feedLines(term, 200, 8000)
	if _, y3, ok := ref.ScreenPoint(); ok {
		got := screenLines(term)[y3]
		t.Fatalf("expected ref discarded after prune, still resolves: y=%d text=%q", y3, got)
	}
}

func TestSpikeScreenCoordsAlignAcrossRestore(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines int
	}{
		{name: "within_cap", lines: 20},
		{name: "pruned_at_cap", lines: 8000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, err := New(80, 10, Options{ScrollbackBytes: 50})
			if err != nil {
				t.Fatalf("New src: %v", err)
			}
			defer src.Close()

			feedLines(src, 0, 3)
			ref := src.TrackCursor()
			if ref == nil {
				t.Fatal("TrackCursor returned nil")
			}
			defer ref.Free()
			src.Write([]byte("BLOCK-START marker row\r\n"))
			feedLines(src, 1000, 1000+tc.lines)

			_, y, ok := ref.ScreenPoint()
			if tc.name == "pruned_at_cap" {
				if ok {
					t.Fatalf("expected pruned ref to report no value, got y=%d", y)
				}
				ref2 := src.TrackCursor()
				defer ref2.Free()
				src.Write([]byte("BLOCK-START late marker\r\n"))
				var ok2 bool
				_, y, ok2 = ref2.ScreenPoint()
				if !ok2 {
					t.Fatal("late ref lost")
				}
			} else if !ok {
				t.Fatal("ref lost within cap")
			}

			srcLines := screenLines(src)
			if !strings.HasPrefix(srcLines[y], "BLOCK-START") {
				t.Fatalf("src row y=%d is %q, want BLOCK-START*", y, srcLines[y])
			}

			snap := src.Serialize()
			restored, err := Restore(snap.Payload, Options{ScrollbackBytes: 50})
			if err != nil {
				t.Fatalf("Restore: %v", err)
			}
			defer restored.Close()

			gotLines := screenLines(restored)
			if y >= len(gotLines) {
				t.Fatalf("restored terminal has %d rows, serialize-time y=%d out of range", len(gotLines), y)
			}
			if gotLines[y] != srcLines[y] {
				t.Fatalf("row misalignment at y=%d: src=%q restored=%q", y, srcLines[y], gotLines[y])
			}
			if len(gotLines) != len(srcLines) {
				t.Fatalf("row count mismatch: src=%d restored=%d", len(srcLines), len(gotLines))
			}
		})
	}
}

func TestSpikeTrackedRefResolvesWhileAltScreenActive(t *testing.T) {
	src, err := New(80, 10, Options{ScrollbackBytes: 50})
	if err != nil {
		t.Fatalf("New src: %v", err)
	}
	defer src.Close()

	feedLines(src, 0, 12)
	ref := src.TrackCursor()
	if ref == nil {
		t.Fatal("TrackCursor returned nil")
	}
	defer ref.Free()
	src.Write([]byte("BLOCK-START before vim\r\n"))

	src.Write([]byte("\x1b[?1049h\x1b[2J\x1b[HALT-SCREEN-CONTENT"))

	_, y, ok := ref.ScreenPoint()
	if !ok {
		t.Fatal("primary ref lost while alt active")
	}

	snap := src.Serialize()
	restored, err := Restore(snap.Payload, Options{ScrollbackBytes: 50})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	defer restored.Close()

	src.Write([]byte("\x1b[?1049l"))
	restored.Write([]byte("\x1b[?1049l"))

	srcLines, gotLines := screenLines(src), screenLines(restored)
	if !strings.HasPrefix(srcLines[y], "BLOCK-START") {
		t.Fatalf("src primary row y=%d is %q, want BLOCK-START*", y, srcLines[y])
	}
	if y >= len(gotLines) || gotLines[y] != srcLines[y] {
		got := "<out of range>"
		if y < len(gotLines) {
			got = gotLines[y]
		}
		t.Fatalf("alt-screen restore misalignment at y=%d: src=%q restored=%q", y, srcLines[y], got)
	}
}

func TestTrackPointPinsAScreenRow(t *testing.T) {
	base := LiveTrackedRefs()
	term := newT(t, 80, 10)
	feedLines(term, 0, 30)

	lines := screenLines(term)
	const want = "line-0007"
	row := -1
	for i, l := range lines {
		if l == want {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("fixture line %q not on screen", want)
	}

	ref := term.TrackPoint(3, row)
	if ref == nil {
		t.Fatalf("TrackPoint(3, %d) returned nil", row)
	}
	defer ref.Free()
	x, y, ok := ref.ScreenPoint()
	if !ok || x != 3 || y != row {
		t.Fatalf("ScreenPoint = (%d,%d,%v), want (3,%d,true)", x, y, ok, row)
	}

	feedLines(term, 30, 40)
	_, y, ok = ref.ScreenPoint()
	if !ok {
		t.Fatal("the pin stopped resolving after more output")
	}
	if got := screenLines(term)[y]; got != want {
		t.Errorf("the pin drifted: row %d now reads %q, want %q", y, got, want)
	}

	ref.Free()
	if got := LiveTrackedRefs(); got != base {
		t.Errorf("live refs = %d, want %d", got, base)
	}
}

func TestTrackPointRejectsARowThatIsNotThere(t *testing.T) {
	term := newT(t, 80, 10)
	term.Write([]byte("one line\r\n"))
	if ref := term.TrackPoint(0, 5000); ref != nil {
		ref.Free()
		t.Error("TrackPoint pinned a row far past the end of the screen")
	}
	if ref := term.TrackPoint(0, -1); ref != nil {
		ref.Free()
		t.Error("TrackPoint pinned a negative row")
	}
}
