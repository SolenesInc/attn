//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

const mirrorStorageLimit = 10 << 20

// Cells are 8x16 px in ghosttyvt, so every image size below is an exact cell
// count: 16x32 is 2x2 cells, 16x96 is 2x6.
func kittyPlaceRGB(id uint32, w, h int, extra string) string {
	pix := make([]byte, w*h*3)
	for i := range pix {
		pix[i] = byte((i*7 + 13) % 251)
	}
	return fmt.Sprintf("\x1b_Ga=T,i=%d,f=24,t=d,s=%d,v=%d%s;%s\x1b\\",
		id, w, h, extra, base64.StdEncoding.EncodeToString(pix))
}

func kittyTransmitRGB(id uint32, w, h int) string {
	return fmt.Sprintf("\x1b_Ga=t,i=%d,f=24,t=d,s=%d,v=%d;%s\x1b\\",
		id, w, h, base64.StdEncoding.EncodeToString(kittyCorpusPixels(w, h)))
}

// An unfinished CSI in front of the APC makes its leading ESC that CSI's exit, so the
// segmenter cannot cut the APC out: the bytes reach the wire verbatim.
func undescribed(apc string) string { return "\x1b[1" + apc }

type mirror struct {
	worker     *ghosttyvt.Terminal
	client     *ghosttyvt.Terminal
	feed       *wireFeeder
	lastWire   []byte
	lastResync string
}

// Placement geometry is resolved in cells, and cells only have a size after a
// resize — hence the Resize below.
func newKittyTerminal(t *testing.T, cols, rows int, opts ghosttyvt.Options) *ghosttyvt.Terminal {
	t.Helper()
	term, err := ghosttyvt.New(cols, rows, opts)
	if err != nil {
		t.Fatalf("ghosttyvt.New(%d,%d,%+v): %v", cols, rows, opts, err)
	}
	t.Cleanup(term.Close)
	term.Resize(cols, rows)
	return term
}

func newMirror(t *testing.T, cols, rows int, opts ghosttyvt.Options) *mirror {
	t.Helper()
	worker := newKittyTerminal(t, cols, rows, opts)
	client := newKittyTerminal(t, cols, rows, ghosttyvt.Options{ScrollbackBytes: opts.ScrollbackBytes})

	feed := newWireFeeder(worker, 0, nil, 0)
	if feed == nil {
		t.Fatalf("newWireFeeder returned nil for a live terminal")
	}
	t.Cleanup(feed.close)
	return &mirror{worker: worker, client: client, feed: feed}
}

func (m *mirror) write(chunk string) {
	wire, resync := m.feed.feed([]byte(chunk))
	m.lastWire = append([]byte(nil), wire...)
	m.lastResync = resync
	writeAsClient(m.client, wire)
}

func (m *mirror) agree(t *testing.T, when string) {
	t.Helper()
	if got, want := m.client.PlainText(), m.worker.PlainText(); got != want {
		t.Errorf("%s: the client history diverged from the worker history\nworker:\n%s\nclient:\n%s",
			when, want, got)
	}
	if got, want := m.client.ViewportText(), m.worker.ViewportText(); got != want {
		t.Errorf("%s: the client viewport diverged from the worker viewport\nworker:\n%s\nclient:\n%s",
			when, want, got)
	}
	wx, wy := m.worker.CursorPos()
	cx, cy := m.client.CursorPos()
	if wx != cx || wy != cy {
		t.Errorf("%s: cursor at (%d,%d) on the client, (%d,%d) on the worker", when, cx, cy, wx, wy)
	}
}

type mirrorCase struct {
	name       string
	cols, rows int
	chunks     []string
	check      func(t *testing.T, m *mirror)
}

var mirrorCases = []mirrorCase{
	{
		name: "image placed at a cursor in the middle of the screen",
		cols: 20, rows: 8,
		chunks: []string{"\x1b[4;6Hxy", kittyPlaceRGB(1, 16, 32, "")},
		check: func(t *testing.T, m *mirror) {
			if len(m.feed.deltas) != 1 || len(m.feed.deltas[0].Added) != 1 {
				t.Fatalf("placement deltas = %+v, want one added placement", m.feed.deltas)
			}
			if got := m.feed.deltas[0].Added[0].ImageID; got != 1 {
				t.Errorf("added placement image id = %d, want 1", got)
			}
		},
	},
	{
		name: "image on the bottom row scrolls the screen",
		cols: 20, rows: 8,
		chunks: []string{"top\r\nsecond\r\n\x1b[8;1Hbottom", kittyPlaceRGB(2, 16, 32, "")},
	},
	{
		name: "image taller than the rows left below the cursor",
		cols: 20, rows: 8,
		chunks: []string{"\x1b[6;3Hhere", kittyPlaceRGB(3, 16, 96, "")},
	},
	{
		name: "image placed inside a scroll region",
		cols: 20, rows: 8,
		chunks: []string{
			"one\r\ntwo\r\nthree\r\nfour\r\nfive\r\nsix",
			"\x1b[3;6r\x1b[6;1Hin-region",
			kittyPlaceRGB(4, 16, 32, ""),
		},
	},
	{
		name: "image placed on the alternate screen, then back",
		cols: 20, rows: 8,
		chunks: []string{
			"primary line\r\n",
			"\x1b[?1049h\x1b[3;3Halt",
			kittyPlaceRGB(5, 16, 32, ""),
			"\x1b[?1049l",
		},
		check: func(t *testing.T, m *mirror) {
			if len(m.feed.deltas) != 1 || !onlyRemovals(m.feed.deltas[0]) {
				t.Fatalf("leaving the alternate screen produced deltas %+v, want one pure removal: the exemption is not the thing being exercised", m.feed.deltas)
			}
		},
	},
	{
		name: "an undescribed delete of a live placement",
		cols: 20, rows: 8,
		chunks: []string{
			"\x1b[2;2Hkeep",
			kittyPlaceRGB(54, 16, 32, ""),
			undescribed("\x1b_Ga=d,d=i,i=54\x1b\\") + " tail",
		},
		check: func(t *testing.T, m *mirror) {
			if len(m.feed.deltas) != 1 || !onlyRemovals(m.feed.deltas[0]) {
				t.Fatalf("the undescribed delete produced deltas %+v, want one pure removal", m.feed.deltas)
			}
			if len(m.feed.placements) != 0 {
				t.Errorf("observed placements after the delete = %+v, want none", m.feed.placements)
			}
		},
	},
	{
		name: "chunked transmission split across feed calls",
		cols: 20, rows: 8,
		chunks: splitEvery(kittyPlaceRGB(6, 16, 32, ""), 11),
	},
	{
		name: "a held escape, then text and a half image in one chunk",
		cols: 20, rows: 8,
		chunks: []string{
			"abc\x1b",
			"[1mmore" + halfOf(kittyPlaceRGB(9, 16, 32, "")),
			restOf(kittyPlaceRGB(9, 16, 32, "")) + "end",
		},
	},
	{
		name: "delete removes the placement without touching the grid",
		cols: 20, rows: 8,
		chunks: []string{"\x1b[2;2Hkeep", kittyPlaceRGB(7, 16, 32, ""), "\x1b_Ga=d\x1b\\"},
		check: func(t *testing.T, m *mirror) {
			if got, want := string(m.lastWire), string(wireST); got != want {
				t.Errorf("the delete produced wire bytes %q, want just the ST %q: it moved nothing on the grid, and the ST is not about the grid", got, want)
			}
			if len(m.feed.deltas) != 1 || len(m.feed.deltas[0].Removed) != 1 {
				t.Fatalf("delete deltas = %+v, want one removed placement", m.feed.deltas)
			}
			if got := m.feed.deltas[0].Removed[0].ImageID; got != 7 {
				t.Errorf("removed placement image id = %d, want 7", got)
			}
			if len(m.feed.placements) != 0 {
				t.Errorf("observed placements after the delete = %+v, want none", m.feed.placements)
			}
		},
	},
	{
		name: "a character split around an apc on the last column",
		cols: 20, rows: 8,
		chunks: []string{strings.Repeat("0", 19) + "\xe1", "\x1b_Ga=d\x1b\\", "\xa5 done"},
	},
	{
		name: "a prompt marker splitting a character on the wrap column",
		cols: 20, rows: 8,
		chunks: []string{strings.Repeat("0", 20) + "\xe1", "\x1b]133;A\x1b\\", "\xa5 done"},
	},
	{
		name: "osc 133 markers interleaved with images",
		cols: 40, rows: 10,
		chunks: []string{
			"\x1b]133;A\x1b\\$ ",
			"\x1b]133;B\x1b\\icat pic.png",
			"\r\n\x1b]133;C;cmdline_url=icat%20pic.png\x1b\\",
			kittyPlaceRGB(8, 16, 32, ""),
			"\r\n\x1b]133;D;0\x1b\\\x1b]133;A\x1b\\$ ",
		},
		check: func(t *testing.T, m *mirror) {
			blocks := m.feed.snapshotBlocks()
			if len(blocks) != 2 {
				t.Fatalf("snapshotBlocks() = %+v, want the finished block and the new prompt", blocks)
			}
			done := blocks[0]
			if done.Command == nil || *done.Command != "icat pic.png" {
				t.Errorf("command = %v, want the cmdline the C marker carried", done.Command)
			}
			if done.ExitCode == nil || *done.ExitCode != 0 {
				t.Errorf("exit code = %v, want 0", done.ExitCode)
			}
			if done.EndRow == nil || *done.EndRow <= done.PromptRow {
				t.Errorf("block rows = prompt %d end %v: the image's scroll was not carried into the block rows",
					done.PromptRow, done.EndRow)
			}
		},
	},
}

func onlyRemovals(delta kittyPlacementDelta) bool {
	return len(delta.Removed) > 0 && len(delta.Added) == 0 && len(delta.Updated) == 0
}

func onlyAdditions(delta kittyPlacementDelta) bool {
	return len(delta.Added) > 0 && len(delta.Removed) == 0 && len(delta.Updated) == 0
}

func onlyUpdates(delta kittyPlacementDelta) bool {
	return len(delta.Updated) > 0 && len(delta.Added) == 0 && len(delta.Removed) == 0
}

func halfOf(s string) string { return s[:len(s)/2] }
func restOf(s string) string { return s[len(s)/2:] }

func splitEvery(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}

func TestWireFeedKeepsTheClientGridEqualToTheWorkerGrid(t *testing.T) {
	for _, tc := range mirrorCases {
		t.Run(tc.name, func(t *testing.T) {
			baseline := ghosttyvt.LiveTrackedRefs()
			m := newMirror(t, tc.cols, tc.rows, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})

			for i, chunk := range tc.chunks {
				m.write(chunk)
				if m.lastResync != "" {
					t.Fatalf("chunk %d forced a resync (%s); every case here is synthesizable", i, m.lastResync)
				}
				m.agree(t, fmt.Sprintf("after chunk %d", i))
			}
			if tc.check != nil {
				tc.check(t, m)
			}

			m.feed.close()
			if got := ghosttyvt.LiveTrackedRefs(); got != baseline {
				t.Errorf("LiveTrackedRefs() = %d after the case, want the %d it started at", got, baseline)
			}
		})
	}
}

func TestWireFeedStripsAPCsWithKittyDisabled(t *testing.T) {
	m := newMirror(t, 20, 8, ghosttyvt.Options{})

	head := "before "
	tail := " after"
	m.write(head + kittyPlaceRGB(9, 16, 32, "") + kittyPlaceRGB(10, 8, 16, "") + tail)

	want := head + string(wireST) + string(wireST) + tail
	if got := string(m.lastWire); got != want {
		t.Errorf("wire = %q, want the plain bytes with an ST per stripped APC (%q)", got, want)
	}
	if m.lastResync != "" {
		t.Errorf("resync = %q with the protocol disabled, want none", m.lastResync)
	}
	if len(m.feed.deltas) != 0 {
		t.Errorf("placement deltas = %+v with the protocol disabled, want none", m.feed.deltas)
	}
	m.agree(t, "with kitty disabled")
}

func TestWireFeedHoldsAnUnterminatedAPCOffTheWire(t *testing.T) {
	m := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	full := kittyPlaceRGB(11, 16, 32, "")
	cut := len(full) - 6

	m.write(full[:cut])
	if len(m.lastWire) != 0 {
		t.Fatalf("a half-transmitted image put %q on the wire", m.lastWire)
	}
	m.agree(t, "mid-transmission")

	m.write(full[cut:])
	m.agree(t, "after the terminator")
	if len(m.feed.deltas) != 1 || len(m.feed.deltas[0].Added) != 1 {
		t.Errorf("deltas after the terminator = %+v, want the placement", m.feed.deltas)
	}
}

func TestWireFeedPassesAPlainChunkThroughByIdentity(t *testing.T) {
	m := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	chunk := []byte("\x1b[32mgreen\x1b[0m\r\n\x1b[2;3Hmoved")

	wire, resync := m.feed.feed(chunk)

	if resync != "" {
		t.Errorf("resync = %q for plain output", resync)
	}
	if len(wire) != len(chunk) || &wire[0] != &chunk[0] {
		t.Fatalf("plain output was rewritten: got %q, want the input slice itself", wire)
	}
}

func TestWireFeedResyncsWhenTheAnchorHitsTheTopOfHistory(t *testing.T) {
	m := newMirror(t, 20, 6, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})

	m.write("\x1b[?1049h")
	m.write("alt0\r\nalt1\r\nalt2\r\nalt3\r\nalt4\r\n\x1b[6;1Halt5")
	m.agree(t, "with the alternate screen filled")

	m.write(kittyPlaceRGB(12, 16, 16*8, ""))

	if m.lastResync != kittyResyncAnchorClamped {
		t.Fatalf("resync = %q, want %q: an 8-row image on a 6-row alternate screen destroys the anchor",
			m.lastResync, kittyResyncAnchorClamped)
	}
	if got, want := string(m.lastWire), string(wireST); got != want {
		t.Errorf("wire = %q for an unsynthesizable chunk, want just the ST %q: the snapshot re-push carries the truth", got, want)
	}
	if worker := m.worker.PlainText(); strings.TrimSpace(worker) != "" {
		t.Errorf("worker screen = %q, want it scrolled clear: the case does not exercise a lost anchor otherwise", worker)
	}
}

func TestWireFeedResyncsOnEveryUnaccountedMutationButAPureRemoval(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   string
		shape  func(kittyPlacementDelta) bool
	}{
		{
			name: "a placement that appears and dies inside one chunk",
			chunks: []string{
				"\x1b[2;2Hkeep",
				undescribed(kittyPlaceRGB(47, 16, 32, "")) + undescribed("\x1b_Ga=d\x1b\\"),
			},
			want: kittyResyncStampWithoutDelta,
		},
		{
			name: "a live placement re-placed at a new position",
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(52, 16, 32, ",p=7"),
				"\x1b[6;9Hmove" + undescribed("\x1b_Ga=p,i=52,p=7\x1b\\"),
			},
			want:  kittyResyncUndescribedImage,
			shape: onlyUpdates,
		},
		{
			name: "an image retransmitted under a live placement id",
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(53, 16, 32, ""),
				undescribed(kittyTransmitRGB(53, 8, 16)),
			},
			want:  "",
			shape: onlyRemovals,
		},
		{
			name:   "a placement created by an undescribed apc",
			chunks: []string{"\x1b[2;2Hkeep", undescribed(kittyPlaceRGB(55, 16, 32, ""))},
			want:   kittyResyncUndescribedImage,
			shape:  onlyAdditions,
		},
		{
			name:   "a placement created by a described apc",
			chunks: []string{"\x1b[2;2Hkeep", kittyPlaceRGB(60, 16, 32, "")},
			want:   "",
			shape:  onlyAdditions,
		},
		{
			name: "a live placement deleted by an undescribed apc",
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(54, 16, 32, ""),
				undescribed("\x1b_Ga=d,d=i,i=54\x1b\\") + " tail",
			},
			want:  "",
			shape: onlyRemovals,
		},
		{
			name: "live placements pruned by leaving the alternate screen",
			chunks: []string{
				"primary line\r\n",
				"\x1b[?1049h\x1b[3;3Halt",
				kittyPlaceRGB(56, 16, 32, ""),
				"\x1b[?1049l",
			},
			want:  "",
			shape: onlyRemovals,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
			for i, chunk := range tc.chunks[:len(tc.chunks)-1] {
				m.write(chunk)
				if m.lastResync != "" {
					t.Fatalf("chunk %d resynced (%s) before the chunk under test", i, m.lastResync)
				}
			}
			m.write(tc.chunks[len(tc.chunks)-1])

			if m.lastResync != tc.want {
				t.Fatalf("resync = %q, want %q", m.lastResync, tc.want)
			}
			if tc.shape == nil {
				if len(m.feed.deltas) != 0 {
					t.Fatalf("deltas = %+v, want none: the diff has to be blind here, or the stamp is not what fired", m.feed.deltas)
				}
			} else if len(m.feed.deltas) != 1 || !tc.shape(m.feed.deltas[0]) {
				t.Fatalf("deltas = %+v: not the shape this row is named for", m.feed.deltas)
			}
			if tc.want != "" {
				return
			}
			m.agree(t, "after a chunk that only retired placements")
		})
	}
}

func TestWireFeedStillDescribesTheAPCThatSettlesAnUndescribedOne(t *testing.T) {
	const prefix = "\x1b[2;2Hkeep"
	described := kittyPlaceRGB(59, 16, 32, "")
	silent := undescribed(kittyPlaceRGB(58, 16, 32, ",C=1"))

	control := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	control.write(prefix)
	control.write(described)
	if control.lastResync != "" {
		t.Fatalf("the control resynced (%s); it places one ordinary image", control.lastResync)
	}
	control.agree(t, "with one described placement")
	describedWire := string(control.lastWire)

	m := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	m.write(prefix)
	m.write(silent + described)

	if m.lastResync != kittyResyncUndescribedImage {
		t.Fatalf("resync = %q, want %q: the first placement reached the terminal on bytes the wire carried verbatim",
			m.lastResync, kittyResyncUndescribedImage)
	}
	if got, want := string(m.lastWire), silent+describedWire; got != want {
		t.Errorf("wire = %q,\nwant the undescribed bytes verbatim followed by exactly the description that placement gets on its own (%q)", got, want)
	}
}

func TestWireFeedCarriesTheScrollOfAnOverTallPlacement(t *testing.T) {
	for _, rows := range []int{8, 12} {
		t.Run(fmt.Sprintf("%d rows", rows), func(t *testing.T) {
			for _, r := range []int{1, 2, 7, 8, 12, 13, 14, 15, 22, 23, 40} {
				m := newMirror(t, 20, rows, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
				m.write("\x1b[2;2Hkeep")
				m.write(kittyPlaceRGB(uint32(80+r), 16, 32, fmt.Sprintf(",r=%d", r)))
				resync := m.lastResync
				m.write("\r\ntail")

				if resync != "" {
					t.Errorf("r=%d resynced (%s) on a %d-row screen: one SU carries that scroll",
						r, resync, rows)
					continue
				}
				m.agree(t, fmt.Sprintf("after an r=%d placement on a %d-row screen", r, rows))
			}
		})
	}
}

func TestWireFeedResyncsWhileLeftRightMarginsAreSet(t *testing.T) {
	const bottom = "\x1b[32;5Hxy"
	place := kittyPlaceRGB(65, 16, 32, "")

	control := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	control.write("\x1b[1;1Htop" + bottom)
	control.write(place)
	if control.lastResync != "" {
		t.Fatalf("the control resynced (%s): without margins this is an ordinary bottom-row placement", control.lastResync)
	}
	control.agree(t, "with no margins set")

	m := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	m.write("\x1b[1;1Htop\x1b[?69h\x1b[4;14s" + bottom)
	m.write(place)
	if m.lastResync != kittyResyncMarginMode {
		t.Fatalf("resync = %q, want %q: the placement scrolled the margin box and nothing measured it",
			m.lastResync, kittyResyncMarginMode)
	}
	// Receipts against the pinned ghostty; a bump moves them. What must survive is the
	// difference: the control carries an SU, the tripwire the same moves without it.
	if got, want := string(control.lastWire), string(wireST)+"\x1b[1S\x1b[2C"; got != want {
		t.Fatalf("control wire = %q, want %q: without margins the same placement scrolls a row and says so", got, want)
	}
	if got, want := string(m.lastWire), string(wireST)+"\x1b[2C"; got != want {
		t.Errorf("wire = %q, want the control's cursor moves without its SU (%q): a resync is not a stop order",
			got, want)
	}
	if worker, client := m.worker.ViewportText(), m.client.ViewportText(); worker == client {
		t.Errorf("the grids agree, so the case no longer exercises an unmeasurable margin scroll:\n%s", worker)
	}
}

func TestWireFeedResyncsWithACursorInTheLastColumn(t *testing.T) {
	const cols, rows = 20, 8
	place := kittyPlaceRGB(70, 8, 16, "")

	control := newMirror(t, cols, rows, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	control.write(strings.Repeat("x", 5))
	control.write(place)
	if control.lastResync != "" {
		t.Fatalf("the control resynced (%s): mid-row this is an ordinary one-cell placement", control.lastResync)
	}
	if got, want := string(control.lastWire), string(wireST)+"\x1b[1C"; got != want {
		t.Fatalf("control wire = %q, want %q: mid-row the placement is described by its cursor move alone", got, want)
	}
	control.write("y")
	control.agree(t, "with the cursor mid-row")

	m := newMirror(t, cols, rows, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	m.write(strings.Repeat("x", cols))
	m.write(place)
	if m.lastResync != kittyResyncPendingWrap {
		t.Fatalf("resync = %q, want %q: the placement consumed a pending wrap nothing could measure",
			m.lastResync, kittyResyncPendingWrap)
	}
	// At the pinned ghostty the measurement catches the wrap itself: the cursor
	// moves down a row and back to column 0.
	if got, want := string(m.lastWire), string(wireST)+"\x1b[1B\x1b[19D"; got != want {
		t.Errorf("wire = %q, want %q: the dispatch is still described, wrap included", got, want)
	}

	// Both sides converge at this pin. The resync stays: no accessor exposes the
	// pending-wrap bit, so one case converging at one pin proves nothing.
	m.write("y")
	wx, wy := m.worker.CursorPos()
	cx, cy := m.client.CursorPos()
	if wx != cx || wy != cy {
		t.Errorf("cursors = worker (%d,%d), client (%d,%d): the wire described the wrap, so they must agree", wx, wy, cx, cy)
	}
	m.agree(t, "after a placement consumed the pending wrap")
}

func TestWireFeedLogsATransmissionTheStorageLimitRefused(t *testing.T) {
	// 64x64 RGBA is 16,384 bytes stored: one limit under it, one over.
	const refuses, accepts = 4096, 1 << 20

	feedUnder := func(limit uint64, apc string) []string {
		term := newKittyTerminal(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: limit})
		var logs []string
		feeder := newWireFeeder(term, 0, func(format string, args ...interface{}) {
			logs = append(logs, fmt.Sprintf(format, args...))
		}, limit)
		if feeder == nil {
			t.Fatalf("newWireFeeder returned nil for a live terminal")
		}
		t.Cleanup(feeder.close)
		feeder.feed([]byte(apc))
		return logs
	}

	oversized := kittyPlaceRGB(90, 64, 64, "")
	logs := feedUnder(refuses, oversized)
	if len(logs) != 1 {
		t.Fatalf("logs = %q, want exactly one line for a refused transmission", logs)
	}
	for _, want := range []string{kittyStorageLimitEnv, fmt.Sprint(refuses), fmt.Sprint(64 * 64 * 4)} {
		if !strings.Contains(logs[0], want) {
			t.Errorf("refusal log %q does not name %q", logs[0], want)
		}
	}

	if logs := feedUnder(accepts, oversized); len(logs) != 0 {
		t.Errorf("logs = %q for a transmission that was stored, want silence", logs)
	}
}

func TestWireFeedKeepsQuietForEverythingThatIsNotARefusal(t *testing.T) {
	const limit = 1 << 20
	place := kittyPlaceRGB(91, 16, 32, ",p=7")

	for _, tc := range []struct {
		name   string
		chunks []string
	}{
		{name: "a transmission split across m=1 escapes", chunks: []string{kittyPlaceRGBChunked(92, 16, 32, 64)}},
		{name: "a support query", chunks: []string{"\x1b_Ga=q,i=31,f=24,t=d,s=1,v=1;AAAA\x1b\\"}},
		{name: "a re-place of a live placement", chunks: []string{place, "\x1b_Ga=p,i=91,p=8\x1b\\"}},
		{name: "a delete of an image that is not there", chunks: []string{"\x1b_Ga=d,d=i,i=404\x1b\\"}},
		{name: "an eviction under a limit that holds one image", chunks: []string{
			kittyTransmitRGB(93, 32, 32), kittyTransmitRGB(94, 32, 32), kittyTransmitRGB(95, 32, 32),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage := uint64(limit)
			if tc.name == "an eviction under a limit that holds one image" {
				storage = 8192
			}
			term := newKittyTerminal(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: storage})
			var logs []string
			feeder := newWireFeeder(term, 0, func(format string, args ...interface{}) {
				logs = append(logs, fmt.Sprintf(format, args...))
			}, storage)
			if feeder == nil {
				t.Fatalf("newWireFeeder returned nil for a live terminal")
			}
			t.Cleanup(feeder.close)
			for _, chunk := range tc.chunks {
				feeder.feed([]byte(chunk))
			}
			if len(logs) != 0 {
				t.Errorf("logs = %q, want silence", logs)
			}
		})
	}
}

func TestWireFeedKeepsKittyResponsesFlowingToTheProgram(t *testing.T) {
	m := newMirror(t, 20, 8, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	m.worker.DrainResponses()

	m.write("\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\")

	resp := string(m.worker.DrainResponses())
	if !strings.Contains(resp, "\x1b_Gi=31;OK") {
		t.Errorf("support query response = %q, want ghostty's OK for image 31", resp)
	}
	if got, want := string(m.lastWire), string(wireST); got != want {
		t.Errorf("wire = %q for the query APC, want just the ST %q: the query is answered to the program, never to the client", got, want)
	}
}

func TestWireFeedPreSTOnlyEndsTheDecode(t *testing.T) {
	for _, pending := range []string{"", "\xe1", "\xc2", "\xf0\x9f"} {
		for n := 17; n <= 21; n++ {
			prefix := strings.Repeat("0", n) + pending

			plain := newKittyTerminal(t, 20, 6, ghosttyvt.Options{})
			plain.Write([]byte(prefix))

			withST := newKittyTerminal(t, 20, 6, ghosttyvt.Options{})
			withST.Write([]byte(prefix))
			withST.Write(wireST)

			px, py := plain.CursorPos()
			sx, sy := withST.CursorPos()
			sameGrid := plain.PlainText() == withST.PlainText() && px == sx && py == sy

			if pending == "" {
				if !sameGrid {
					t.Errorf("n=%d, nothing pending: the ST moved the grid\nplain:  %q (%d,%d)\nwithST: %q (%d,%d)",
						n, plain.PlainText(), px, py, withST.PlainText(), sx, sy)
				}
				continue
			}
			if sameGrid {
				t.Errorf("n=%d, %q pending: the ST changed nothing, so the decode was never ended", n, pending)
				continue
			}
			if got, want := len([]rune(strings.ReplaceAll(withST.PlainText(), "\n", ""))),
				len([]rune(strings.ReplaceAll(plain.PlainText(), "\n", "")))+1; got != want {
				t.Errorf("n=%d, %q pending: grid gained %d cells, want exactly 1 replacement character\nplain:  %q\nwithST: %q",
					n, pending, got-want+1, plain.PlainText(), withST.PlainText())
			}
		}
	}
}

func TestWireFeedPinsTheCursorAfterTheDecodeEnds(t *testing.T) {
	m := newMirror(t, 20, 8, ghosttyvt.Options{})

	m.write(strings.Repeat("0", 20) + "\xe1")
	m.write(kittyDirectRGB)

	if got, want := string(m.lastWire), string(wireST); got != want {
		t.Errorf("wire = %q, want the ST alone (%q): the APC moved nothing, so nothing should be described", got, want)
	}
	if m.lastResync != "" {
		t.Errorf("resync = %q, want none: the early exit handles this", m.lastResync)
	}
	if x, y := m.worker.CursorPos(); x != 1 || y != 1 {
		t.Errorf("worker cursor = (%d,%d), want (1,1): the abort commits the pending wrap", x, y)
	}
	m.agree(t, "after an APC on the wrap column")
}

func TestWireFeedPinsTheBlockAfterTheDecodeEnds(t *testing.T) {
	m := newMirror(t, 20, 8, ghosttyvt.Options{})

	m.write(strings.Repeat("0", 20) + "\xe1")
	m.write("\x1b]133;A\x1b\\")

	blocks := m.feed.snapshotBlocks()
	if len(blocks) != 1 {
		t.Fatalf("snapshotBlocks() = %+v, want the one open prompt", blocks)
	}
	if got := blocks[0].PromptRow; got != 2 {
		t.Errorf("prompt row = %d, want 2: the pin must observe Ghostty's post-marker cursor", got)
	}
	m.agree(t, "after a marker on the wrap column")
}
