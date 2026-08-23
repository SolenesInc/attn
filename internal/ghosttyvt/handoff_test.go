//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

import (
	"fmt"
	"strings"
	"testing"
)

// A worker hands its screen to its replacement as plain VT because that is the
// one currency a different libghostty-vt still reads. These pin what the dump
// carries, what it drops, and the one correction it needs on top of the
// upstream formatter. Receipts, including the cross-version run:
// docs/plans/2026-08-22-worker-inplace-upgrade.md.

func handoffInto(t *testing.T, source *Terminal) *Terminal {
	t.Helper()
	cols, rows := source.Size()
	replayed := newT(t, cols, rows)
	replayed.Write(source.HandoffVT().VTDump)
	return replayed
}

func assertSameScreen(t *testing.T, want, got *Terminal) {
	t.Helper()
	if w, g := want.TotalRows(), got.TotalRows(); w != g {
		t.Errorf("total grid rows = %d, want %d (the replay is %d rows short)", g, w, w-g)
	}
	wx, wy := want.CursorPos()
	gx, gy := got.CursorPos()
	if wx != gx || wy != gy {
		t.Errorf("cursor at (%d,%d), want (%d,%d)", gx, gy, wx, wy)
	}
	if w, g := want.CursorVisible(), got.CursorVisible(); w != g {
		t.Errorf("cursor visible = %v, want %v", g, w)
	}
	if w, g := want.AltScreenActive(), got.AltScreenActive(); w != g {
		t.Errorf("alt screen active = %v, want %v", g, w)
	}
	if w, g := want.ViewportText(), got.ViewportText(); w != g {
		gr, wr := firstDifferingRow(g, w)
		t.Errorf("viewport differs:\n got %q\nwant %q", gr, wr)
	}
	if w, g := want.PlainText(), got.PlainText(); w != g {
		gr, wr := firstDifferingRow(g, w)
		t.Errorf("screen text (scrollback included) differs:\n got %q\nwant %q", gr, wr)
	}
}

func firstDifferingRow(got, want string) (string, string) {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gr, wr string
		if i < len(g) {
			gr = g[i]
		}
		if i < len(w) {
			wr = w[i]
		}
		if gr != wr {
			return gr, wr
		}
	}
	return got, want
}

func writeLines(term *Terminal, n int) {
	for i := 1; i <= n; i++ {
		term.Write(fmt.Appendf(nil, "line %d of the session\r\n", i))
	}
}

func TestHandoffVTReplaysTheScreen(t *testing.T) {
	// Grid shapes a session actually reaches, plus the ones that broke an
	// earlier cursor-based correction: a screen that has not scrolled yet, one
	// scrolled by exactly one row, and a cursor parked above the content.
	cases := []struct {
		name  string
		write func(*Terminal)
	}{
		{"empty", func(*Terminal) {}},
		{"unscrolled", func(term *Terminal) { writeLines(term, 10) }},
		{"exactly full", func(term *Terminal) { writeLines(term, 23) }},
		{"scrolled by one", func(term *Terminal) { writeLines(term, 24) }},
		{"deep scrollback", func(term *Terminal) { writeLines(term, 500) }},
		{"prompt with no trailing newline", func(term *Terminal) {
			writeLines(term, 30)
			term.Write([]byte("$ "))
		}},
		{"cursor parked above the content", func(term *Terminal) {
			writeLines(term, 30)
			term.Write([]byte("\x1b[5;3H"))
		}},
		{"cursor hidden off in a corner", func(term *Terminal) {
			writeLines(term, 4)
			term.Write([]byte("\x1b[?25l\x1b[13;44H"))
		}},
		{"styles and hyperlinks", func(term *Terminal) {
			term.Write([]byte("\x1b[1;31mbold red\x1b[0m \x1b[3;4;32mitalic underline\x1b[0m\r\n"))
			term.Write([]byte("\x1b[38;5;208m256\x1b[0m \x1b[38;2;12;250;7mtruecolor\x1b[48;2;40;40;40m on grey\x1b[0m\r\n"))
			term.Write([]byte("\x1b]8;;https://example.com\x07hyperlink\x1b]8;;\x07\r\n"))
		}},
		{"wide chars and emoji", func(term *Terminal) {
			term.Write([]byte("日本語のテキスト\r\nemoji: \U0001F41B \U0001F468‍\U0001F4BB\r\ncombining: é\r\n"))
		}},
		{"soft wrap", func(term *Terminal) {
			term.Write([]byte(strings.Repeat("x", 117) + "\r\n"))
		}},
		{"modes and tabstops", func(term *Terminal) {
			term.Write([]byte("\x1b[?2004h\x1b[?1002h\x1b[?1006h\x1b[?2027h\x1b[?7l"))
			term.Write([]byte("\x1b[3g\x1b[1;10H\x1bH\x1b[1;40H\x1bH\x1b[2;1Ha\tb\tc\r\n"))
		}},
		{"full screen app", func(term *Terminal) {
			for row := 1; row <= 24; row++ {
				term.Write(fmt.Appendf(nil, "\x1b[%d;1H\x1b[4%dm %2d \x1b[0m row %d filled", row, row%8, row, row))
			}
			term.Write([]byte("\x1b[12;40H"))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := newT(t, 80, 24)
			tc.write(source)
			// Read the source's state before HandoffVT consumes it; for a
			// primary-screen session it changes nothing, but the assertions
			// must not depend on that.
			assertSameScreen(t, source, handoffInto(t, source))
		})
	}
}

func TestHandoffVTPutsTheCursorBelowTheLastLine(t *testing.T) {
	// The upstream formatter stops at the last non-blank row, so a replay of
	// its output alone leaves the cursor ON the last line instead of below it,
	// and the child's next line overwrites one it already printed. This is the
	// bug the row-deficit correction exists for: remove the correction and
	// this fails.
	source := newT(t, 80, 24)
	writeLines(source, 60)

	replayed := handoffInto(t, source)
	replayed.Write([]byte("the next line the child prints\r\n"))

	text := replayed.PlainText()
	if !strings.Contains(text, "line 60 of the session") {
		t.Error("the last line before the handoff was overwritten by the first line after it")
	}
	if want, got := "line 60 of the session\nthe next line the child prints", text; !strings.Contains(got, want) {
		row, _ := firstDifferingRow(got, want)
		t.Errorf("the new line did not land under the old one; got %q around it", row)
	}
}

func TestHandoffVTCarriesThePrimaryScreenUnderAnAltScreenApp(t *testing.T) {
	source := newT(t, 80, 24)
	source.Write([]byte("shell history that must survive\r\n"))
	source.Write([]byte("\x1b[?1049h\x1b[2J\x1b[Hfull-screen app drawing\r\n"))

	replayed := handoffInto(t, source)
	if !replayed.AltScreenActive() {
		t.Fatal("the replay landed on the primary screen; the session was on the alternate one")
	}
	if got := replayed.ViewportText(); !strings.Contains(got, "full-screen app drawing") {
		t.Errorf("alternate screen lost: %q", got)
	}

	// HandoffVT already dropped the source to the primary screen to reach it,
	// which is what makes it destructive. Drop the replay too and compare.
	replayed.Write([]byte("\x1b[?1049l"))
	assertSameScreen(t, source, replayed)
	if got := replayed.ViewportText(); !strings.Contains(got, "shell history that must survive") {
		t.Errorf("primary screen lost under the app: %q", got)
	}
}

func TestHandoffVTDropsKittyImages(t *testing.T) {
	// A known, deliberate loss: the dump has no image data, and an image
	// scrolled into history has no cell left to be placed at. The user sees a
	// blank where an image was until the program redraws.
	source := newKittyT(t, 80, 24, 64<<20)
	source.Write([]byte("\x1b_Ga=T,i=32,f=24,s=1,v=1;/wAA\x1b\\"))
	if len(source.KittyPlacements()) != 1 {
		t.Fatalf("fixture did not place an image: %d placements", len(source.KittyPlacements()))
	}
	if got := len(handoffInto(t, source).KittyPlacements()); got != 0 {
		t.Fatalf("kitty placements after the handoff = %d, want 0 — if this now works, say so in the plan doc", got)
	}
}

func TestHandoffVTKeepsItsShortfallInsideAScrollingRegion(t *testing.T) {
	// The documented limit: the correction's line feeds land outside the
	// scrolling region and scroll nothing, so a scrolled screen that also has
	// a region set and a blank bottom row stays one row short. Programs that
	// set a region keep the screen full, so a real session does not reach it.
	// Pinned so a future fix shows up here as a failure rather than a surprise.
	source := newT(t, 80, 24)
	writeLines(source, 30)
	source.Write([]byte("\x1b[5;20r"))

	replayed := handoffInto(t, source)
	if source.TotalRows() == replayed.TotalRows() {
		t.Fatal("the shortfall inside a scrolling region is gone — good; update the plan doc and drop this test")
	}
}
