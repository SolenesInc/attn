//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

// Pixel answers are the grid times the cell size: a placeholder made chafa assume
// an 8 x 11.4 px cell against a real 9 x 22.6 px one. XTWINOPS is unanswered.

import (
	"fmt"
	"strings"
	"testing"
)

func enableSizeReports(t *testing.T, term *Terminal) {
	t.Helper()
	term.Write([]byte("\x1b[?2048h"))
	term.DrainResponses()
}

func TestSetCellPixelSizeReportsTheNewPixelSizeImmediately(t *testing.T) {
	const cols, rows = 40, 12
	term := newT(t, cols, rows)
	term.Write([]byte("a line of text, so the grid is not empty when the cell moves\r\n"))
	enableSizeReports(t, term)

	// A real cell measured on a 2x display: 9 x 22.6 CSS px, rounded and doubled.
	const cellW, cellH = 18, 45
	term.SetCellPixelSize(cellW, cellH)

	want := fmt.Sprintf("\x1b[48;%d;%d;%d;%dt", rows, cols, rows*cellH, cols*cellW)
	if got := string(term.DrainResponses()); got != want {
		t.Fatalf("size report after SetCellPixelSize(%d,%d) = %q, want %q", cellW, cellH, got, want)
	}

	term.Resize(cols, rows+1)
	want = fmt.Sprintf("\x1b[48;%d;%d;%d;%dt", rows+1, cols, (rows+1)*cellH, cols*cellW)
	if got := string(term.DrainResponses()); got != want {
		t.Fatalf("size report after the following resize = %q, want %q", got, want)
	}
}

func TestSetCellPixelSizeIgnoresNonPositiveAndUnchangedGeometry(t *testing.T) {
	const cols, rows = 40, 12
	term := newT(t, cols, rows)
	term.SetCellPixelSize(18, 45)
	enableSizeReports(t, term)

	for _, size := range [][2]int{{0, 45}, {18, 0}, {-18, -45}, {0, 0}} {
		term.SetCellPixelSize(size[0], size[1])
		if got := string(term.DrainResponses()); got != "" {
			t.Fatalf("SetCellPixelSize(%d,%d) reported %q, want the last good geometry kept silently", size[0], size[1], got)
		}
	}
	term.Resize(cols, rows+1)
	want := fmt.Sprintf("\x1b[48;%d;%d;%d;%dt", rows+1, cols, (rows+1)*45, cols*18)
	if got := string(term.DrainResponses()); got != want {
		t.Fatalf("size report after the rejected sets = %q, want %q", got, want)
	}

	term.SetCellPixelSize(18, 45)
	if got := string(term.DrainResponses()); got != "" {
		t.Fatalf("re-setting the same cell size reported %q, want nothing", got)
	}
}

func TestNewTerminalReportsFromThePlaceholderCellUntilOneIsSet(t *testing.T) {
	// The placeholder is deliberate: a terminal reporting a zero pixel size would
	// make an emitter divide by zero.
	term := newT(t, 10, 4)
	enableSizeReports(t, term)
	term.Resize(10, 5)

	got := string(term.DrainResponses())
	if !strings.HasSuffix(got, fmt.Sprintf(";%d;%dt", 5*defaultCellHeightPx, 10*defaultCellWidthPx)) {
		t.Fatalf("a fresh terminal reported %q, want the %dx%d placeholder cell", got, defaultCellWidthPx, defaultCellHeightPx)
	}
}
