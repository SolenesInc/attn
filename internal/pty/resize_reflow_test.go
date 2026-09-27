//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

func historyRows(t *testing.T, term *ghosttyvt.Terminal) int {
	t.Helper()
	ref := term.TrackCursor()
	if ref == nil {
		t.Fatal("TrackCursor() returned nil: the history depth cannot be derived")
	}
	defer ref.Free()
	_, fromTop, ok := ref.ScreenPoint()
	if !ok {
		t.Fatal("ScreenPoint() failed: the history depth cannot be derived")
	}
	_, viewportRow := term.CursorPos()
	return fromTop - viewportRow
}

func framesAgree(t *testing.T, worker, control *ghosttyvt.Terminal, when string) {
	t.Helper()
	if got, want := worker.PlainText(), control.PlainText(); got != want {
		t.Errorf("%s: the worker history diverged from a client frame\nworker:\n%s\nclient:\n%s", when, got, want)
	}
	if got, want := worker.ViewportText(), control.ViewportText(); got != want {
		t.Errorf("%s: the worker viewport diverged from a client frame\nworker:\n%s\nclient:\n%s", when, got, want)
	}
	wx, wy := worker.CursorPos()
	cx, cy := control.CursorPos()
	if wx != cx || wy != cy {
		t.Errorf("%s: cursor at (%d,%d) on the worker, (%d,%d) on a client frame", when, wx, wy, cx, cy)
	}
}

const wrappingPrompt = "~/projects/victor/attn/worktrees/a4-reflow $ echo hello wrapped world"

func TestAResizeWithoutReflowKeepsTheWorkerFrameEqualToAClientFrame(t *testing.T) {
	cases := []struct {
		name           string
		cols, rows     uint16
		toCols, toRows uint16
		chunks         []string
		wraparoundOff  bool
	}{
		{
			name: "widening with wrapped history",
			cols: 20, rows: 8, toCols: 40, toRows: 8,
			chunks: []string{wrappingPrompt + "\r\n", "second line\r\n", kittyPlaceRGB(90, 16, 32, ""), "tail"},
		},
		{
			name: "narrowing with wrapped history",
			cols: 40, rows: 8, toCols: 20, toRows: 8,
			chunks: []string{wrappingPrompt + "\r\n", "second line\r\n", kittyPlaceRGB(91, 16, 32, ""), "tail"},
		},
		{
			name: "widening while the alternate screen is active",
			cols: 20, rows: 8, toCols: 40, toRows: 8,
			chunks: []string{
				"primary " + wrappingPrompt + "\r\n",
				"\x1b[?1049h\x1b[2;1H" + wrappingPrompt,
				kittyPlaceRGB(92, 16, 32, ""),
			},
		},
		{
			name: "widening with wraparound disabled by the program",
			cols: 20, rows: 8, toCols: 40, toRows: 8,
			chunks:        []string{"\x1b[?7l", wrappingPrompt + "\r\n", "second line\r\n", kittyPlaceRGB(93, 16, 32, ""), "tail"},
			wraparoundOff: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			worker := newKittyTerminal(t, int(tc.cols), int(tc.rows), ghosttyvt.Options{
				KittyImageStorageLimit: mirrorStorageLimit,
			})
			control := newKittyTerminal(t, int(tc.cols), int(tc.rows), ghosttyvt.Options{
				KittyImageStorageLimit: mirrorStorageLimit,
			})
			framesAgree(t, worker, control, "before any output")

			for _, chunk := range tc.chunks {
				worker.Write([]byte(chunk))
				control.Write([]byte(chunk))
			}
			framesAgree(t, worker, control, "after the output, before the resize")

			worker.ResizeNoReflow(int(tc.toCols), int(tc.toRows))
			if tc.wraparoundOff {
				control.Resize(int(tc.toCols), int(tc.toRows))
			} else {
				control.Write([]byte("\x1b[?7l"))
				control.Resize(int(tc.toCols), int(tc.toRows))
				control.Write([]byte("\x1b[?7h"))
			}
			framesAgree(t, worker, control, fmt.Sprintf("after resizing to %dx%d", tc.toCols, tc.toRows))

			after := strings.Repeat("z", int(tc.toCols)+7) + "\r\nend"
			worker.Write([]byte(after))
			control.Write([]byte(after))
			framesAgree(t, worker, control, "after output that reaches the wrap column")
		})
	}
}

func TestAResizeWithoutReflowKeepsAPlacementsBufferRow(t *testing.T) {
	worker := newKittyTerminal(t, 40, 12, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	worker.Write([]byte(wrappingPrompt + "\r\n" + kittyPlaceRGB(94, 16, 32, ",q=2") + "PLACED"))

	bufferRow := func(when string) int {
		t.Helper()
		placements := worker.KittyPlacements()
		if len(placements) != 1 {
			t.Fatalf("%s: placements = %+v, want the one image", when, placements)
		}
		return historyRows(t, worker) + int(placements[0].ViewportRow)
	}

	placed := bufferRow("once the image is on the grid")
	worker.ResizeNoReflow(24, 12)
	if got := bufferRow("after the width change"); got != placed {
		t.Errorf("the image's buffer row moved from %d to %d across a resize: the client draws it %d rows off until something scrolls",
			placed, got, got-placed)
	}
	for i := 1; i <= 20; i++ {
		worker.Write([]byte(fmt.Sprintf("\r\n%d", i)))
	}
	if got := bufferRow("after the image scrolled into history"); got != placed {
		t.Errorf("the image's buffer row moved from %d to %d as the grid scrolled: scrolling must only move it between history and screen",
			placed, got)
	}
}
