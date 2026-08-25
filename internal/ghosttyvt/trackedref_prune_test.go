//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ghosttyvt

import (
	"strings"
	"testing"
)

func TestTrackedRefDropsWhenPruned(t *testing.T) {
	term, err := New(80, 10, Options{ScrollbackBytes: 50})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()

	ref := term.TrackCursor()
	defer ref.Free()
	term.Write([]byte("MARK-EARLY\r\n"))

	feedLines(term, 0, 20)
	if _, y, ok := ref.ScreenPoint(); ok {
		rows := len(strings.Split(term.PlainText(), "\n"))
		if y < 0 || y >= rows {
			t.Fatalf("retained ref y=%d out of range [0,%d)", y, rows)
		}
	}

	for _, milestone := range []int{1000, 5000, 20000, 60000} {
		feedLines(term, 0, milestone)
		if _, _, ok := ref.ScreenPoint(); !ok {
			return
		}
	}
	t.Fatal("pinned ref never dropped after 60000 lines past a 50-row scrollback cap")
}
