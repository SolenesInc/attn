//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

import (
	"strings"
	"testing"
)

func rowsHeld(term *Terminal) int {
	lines := strings.Split(term.PlainText(), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return len(lines)
}

// Write and Resize each take the terminal's lock, so a ResizeNoReflow built from the
// public methods deadlocks on its first write; this test hangs rather than fails.
func TestResizeNoReflowKeepsTheRowsWithWraparoundEnabled(t *testing.T) {
	term := newT(t, 40, 12)
	term.Write([]byte(strings.Repeat("p", 70) + "\r\ntail"))
	before := rowsHeld(term)

	term.ResizeNoReflow(24, 12)

	if cols, rows := term.Size(); cols != 24 || rows != 12 {
		t.Fatalf("Size() = %dx%d after ResizeNoReflow(24,12), want 24x12", cols, rows)
	}
	if got := rowsHeld(term); got != before {
		t.Errorf("the grid holds %d rows after narrowing, want the %d it held: the content was re-wrapped", got, before)
	}
	// The mode has to come back exactly as the program left it, or every later
	// line stops wrapping.
	term.Write([]byte("\r\n" + strings.Repeat("w", 30)))
	if got, want := rowsHeld(term), before+2; got != want {
		t.Errorf("a 30-column line on a 24-column grid left %d rows, want %d: wraparound was not restored", got, want)
	}
}

// With DECAWM already off ghostty does not reflow, so writing the mode back on
// would enable wrapping the program deliberately disabled.
func TestResizeNoReflowLeavesWraparoundDisabled(t *testing.T) {
	term := newT(t, 40, 12)
	term.Write([]byte("\x1b[?7l" + strings.Repeat("p", 70) + "\r\ntail"))
	before := rowsHeld(term)

	term.ResizeNoReflow(24, 12)

	if got := rowsHeld(term); got != before {
		t.Errorf("the grid holds %d rows after narrowing, want the %d it held", got, before)
	}
	term.Write([]byte("\r\n" + strings.Repeat("w", 30)))
	if got, want := rowsHeld(term), before+1; got != want {
		t.Errorf("a 30-column line on a 24-column grid left %d rows, want %d: the resize turned wraparound back on", got, want)
	}
}
