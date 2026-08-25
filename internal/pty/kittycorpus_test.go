//go:build cgo && ((darwin && arm64) || (linux && amd64) || (linux && arm64))

package pty

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

const kittyCorpusFileName = "kitty_rewrite_corpus.json"

const kittyCorpusDescription = "Cross-runtime parity corpus for the kitty wire rewrite (docs/plans/2026-08-02-terminal-kitty-images.md). " +
	"GENERATED — do not hand-edit. Inputs come from kittyCorpusInputs in internal/pty/kittycorpus_test.go; " +
	"regenerate with `go test ./internal/pty -run TestKittyWireRewriteCorpus -update`. " +
	"Each entry feeds `chunks` (base64, in order) through one real wireFeeder into a kitty-LIVE ghostty terminal. " +
	"`wire[i]` is what the fan-out carried for chunk i (base64; \"\" when the feeder held the chunk and the fan-out was skipped) " +
	"and `resync[i]` is the reason chunk i could not be expressed on the wire (\"\" when none). " +
	"`workerPlainText`, `workerViewportText`, `cursorCol` and `cursorRow` record the kitty-live terminal after the last chunk. " +
	"Replaying `wire` into a terminal that cannot parse kitty must reproduce exactly those three — that agreement is the no-desync property. " +
	"An entry with any nonempty `resync` is exempt: the wire deliberately carries nothing for that chunk and a snapshot re-push makes the client whole."

type kittyCorpusEntry struct {
	Name   string   `json:"name"`
	Cols   int      `json:"cols"`
	Rows   int      `json:"rows"`
	Chunks []string `json:"chunks"`

	Wire   []string `json:"wire"`
	Resync []string `json:"resync"`

	WorkerPlainText    string `json:"workerPlainText"`
	WorkerViewportText string `json:"workerViewportText"`
	CursorCol          int    `json:"cursorCol"`
	CursorRow          int    `json:"cursorRow"`
}

func (e kittyCorpusEntry) resynced() bool {
	for _, reason := range e.Resync {
		if reason != "" {
			return true
		}
	}
	return false
}

type kittyCorpusFile struct {
	Description string             `json:"description"`
	Entries     []kittyCorpusEntry `json:"entries"`
}

type kittyCorpusInput struct {
	name       string
	cols, rows int
	chunks     []string
}

func kittyCorpusPixels(w, h int) []byte {
	pix := make([]byte, w*h*3)
	for i := range pix {
		pix[i] = byte((i*7 + 13) % 251)
	}
	return pix
}

func kittyPlaceRGBChunked(id uint32, w, h, payloadChunk int) string {
	encoded := base64.StdEncoding.EncodeToString(kittyCorpusPixels(w, h))
	var out strings.Builder
	first := true
	for len(encoded) > 0 {
		take := min(payloadChunk, len(encoded))
		part := encoded[:take]
		encoded = encoded[take:]
		more := 0
		if len(encoded) > 0 {
			more = 1
		}
		if first {
			fmt.Fprintf(&out, "\x1b_Ga=T,i=%d,f=24,t=d,s=%d,v=%d,m=%d;%s\x1b\\", id, w, h, more, part)
			first = false
			continue
		}
		fmt.Fprintf(&out, "\x1b_Gm=%d;%s\x1b\\", more, part)
	}
	return out.String()
}

func kittyCorpusInputs() []kittyCorpusInput {
	return []kittyCorpusInput{
		{
			name: "image placed at a cursor in the middle of the screen",
			cols: 20, rows: 8,
			chunks: []string{"\x1b[4;6Hxy", kittyPlaceRGB(1, 16, 32, "")},
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
			// Probed: ghostty scrolls the REGION rather than letting the post-placement cursor
			// cross the bottom margin, so the synthesized CUU/CUD cannot be clamped by margins.
			name: "cursor on the bottom margin, image taller than the rows below it",
			cols: 20, rows: 8,
			chunks: []string{
				"one\r\ntwo\r\nthree\r\nfour\r\nfive\r\nsix\r\nseven",
				"\x1b[3;6r\x1b[6;1Hedge",
				kittyPlaceRGB(13, 16, 48, ""),
			},
		},
		{
			name: "cursor below the bottom margin of a scroll region",
			cols: 20, rows: 8,
			chunks: []string{
				"one\r\ntwo\r\nthree\r\nfour\r\nfive\r\nsix\r\nseven\r\neight",
				"\x1b[2;5r\x1b[8;1Hlast",
				kittyPlaceRGB(14, 16, 64, ""),
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
		},
		{
			name: "chunked transmission split across feed calls",
			cols: 20, rows: 8,
			chunks: splitEvery(kittyPlaceRGB(6, 16, 32, ""), 97),
		},
		{
			name: "the introducer split across three feed chunks",
			cols: 20, rows: 8,
			chunks: []string{"hi\x1b", "_", "G" + strings.TrimPrefix(kittyPlaceRGB(23, 8, 16, ""), "\x1b_G")},
		},
		{
			name: "multi-escape m=1 transmission split mid-payload across feed chunks",
			cols: 20, rows: 8,
			chunks: splitEvery("\x1b[2;2Hbefore"+kittyPlaceRGBChunked(15, 16, 48, 512)+"after", 137),
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
		},
		{
			name: "delete and re-place of the same image id in one chunk",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(16, 16, 32, ""),
				"\x1b_Ga=d,d=i,i=16\x1b\\" + kittyPlaceRGB(16, 16, 48, ""),
			},
		},
		{
			name: "two images with text between them in one chunk",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;1Hstart",
				kittyPlaceRGB(17, 16, 32, "") + " gap " + kittyPlaceRGB(18, 24, 32, "") + " tail",
			},
		},
		{
			name: "placement with the no-cursor-move flag",
			cols: 20, rows: 8,
			chunks: []string{"\x1b[3;4Hxy", kittyPlaceRGB(19, 16, 32, ",C=1")},
		},
		{
			name: "no-cursor-move placement on the bottom row",
			cols: 20, rows: 8,
			chunks: []string{"a\r\nb\r\nc\r\n\x1b[8;1Hbottom", kittyPlaceRGB(20, 16, 96, ",C=1")},
		},
		{
			name: "support query between text in one chunk",
			cols: 20, rows: 8,
			chunks: []string{"before \x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\ after"},
		},
		{
			name: "an APC abandoned by a stray escape mid-payload",
			cols: 20, rows: 8,
			chunks: []string{"A\x1b_Ga=T,i=21,f=24,t=d,s=16,v=32;AAAA\x1b[32mZ\x1b[0m done"},
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
		},
		{
			// PROBED: on a fresh PRIMARY screen a scrolling placement pushes the anchor cell into
			// retained history, where the pin still reports a real row, so the clamp guard is mute.
			name: "first line of a fresh primary screen, image tall enough to scroll",
			cols: 20, rows: 8,
			chunks: []string{kittyPlaceRGB(22, 16, 160, "")},
		},
		{
			name: "an apc pattern inside an sos string",
			cols: 20, rows: 8,
			chunks: []string{"\x1bX\x1b_G\x1b\\0 done"},
		},
		{
			name: "a c1 control ends an apc before its terminator",
			cols: 20, rows: 8,
			chunks: []string{"\x1b_Ga=T,i=41,f=24,t=d,s=8,v=16;\x840 done\x1b\\"},
		},
		{
			name: "an apc opened by the escape that abandoned the previous one",
			cols: 20, rows: 8,
			chunks: []string{"A" + kittyIntro + "a=T;AA" + kittyPlaceRGB(43, 8, 16, "") + "B"},
		},
		{
			name: "an apc that cancels an unfinished csi",
			cols: 20, rows: 8,
			chunks: []string{"\x1b[1" + kittyPlaceRGB(44, 8, 16, "") + " done"},
		},
		{
			name: "an undescribed image displayed and deleted in one chunk",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;2Hkeep",
				undescribed(kittyPlaceRGB(47, 16, 32, "")) + undescribed("\x1b_Ga=d\x1b\\"),
			},
		},
		{
			name: "an undescribed re-place of a live placement at a new position",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(52, 16, 32, ",p=7"),
				"\x1b[6;9Hmove" + undescribed("\x1b_Ga=p,i=52,p=7\x1b\\"),
			},
		},
		{
			name: "an undescribed retransmission under a live placement id",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(53, 16, 32, ""),
				undescribed(kittyTransmitRGB(53, 8, 16)),
			},
		},
		{
			name: "an undescribed image, then an extractable apc in the same chunk",
			cols: 20, rows: 8,
			chunks: []string{
				strings.TrimSuffix(kittyPlaceRGB(57, 16, 32, ""), "\x1b\\") + "\x1bi" + "\x1b_G\x1b\\",
			},
		},
		{
			name: "an undescribed placement and a described one in the same chunk",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;2Hkeep",
				undescribed(kittyPlaceRGB(58, 16, 32, ",C=1")) + kittyPlaceRGB(59, 16, 32, ""),
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
		},
		{
			// Left/right margins plus origin mode: the worker counts columns from the screen edge
			// while DECLRMM reads `CHA` from the LEFT MARGIN — worker 11 against client 13.
			name: "placement inside left and right margins under origin mode",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[?69h\x1b[4;14s\x1b[?6h\x1b[3;2Hxy",
				kittyPlaceRGB(62, 16, 32, ""),
			},
		},
		{
			// The same margins with origin mode OFF, measured: this one does NOT displace an
			// absolute column. Margins alone are not enough; it takes origin mode with them.
			name: "placement inside margins with origin mode off",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[?69h\x1b[4;14s\x1b[6;6Hxy",
				kittyPlaceRGB(63, 16, 32, ""),
			},
		},
		{
			name: "wide placement pushing the cursor right inside margins",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[?69h\x1b[2;18s\x1b[?6h\x1b[2;2Hxy",
				kittyPlaceRGB(64, 48, 32, ""),
			},
		},
		{
			name: "placement scrolling the box while left and right margins are set",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[1;1Htop\x1b[?69h\x1b[4;14s\x1b[32;5Hxy",
				kittyPlaceRGB(65, 16, 32, ""),
			},
		},
		{
			// kitty's `r=` makes a 2x2 image claim 15 rows on an 8-row screen. On this ghostty pin
			// the scroll stays inside the screen; kept as-is because it would trip the tripwire.
			name: "placement claiming far more rows than the screen holds",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b[2;2Hkeep",
				kittyPlaceRGB(66, 16, 32, ",r=15"),
				"\r\ntail",
			},
		},
		{
			// The pending-wrap tripwire, on the shape FuzzKittyWireMirror found (62f19a45d7a5c8c7):
			// exactly a screen width leaves the wrap deferred, consumed on the worker alone.
			name: "placement on a row that is already full",
			cols: 20, rows: 8,
			chunks: []string{
				strings.Repeat("0", 20),
				kittyPlaceRGB(67, 8, 16, ""),
				"0",
			},
		},
		{
			name: "a foreign string split across feed chunks around an apc pattern",
			cols: 20, rows: 8,
			chunks: []string{"\x1b]0;ti", "tle\x1b_Ga=T,i=42;AA", "\x07", kittyPlaceRGB(45, 8, 16, ""), " done"},
		},
		{
			name: "a marker cut short by a stray escape",
			cols: 20, rows: 8,
			chunks: []string{"\x1b]133;A\x1b0Z done"},
		},
		{
			name: "a marker whose introducer was never in ground",
			cols: 20, rows: 8,
			chunks: []string{"\x1b\x1b]133;A\x1b\\00 done"},
		},
		{
			// The permanent shape of the decoder leak: `\xe1` opens a character the APC's ESC ends
			// for the worker. LAST column deliberately, or a synthesized CHA's ESC hides it.
			name: "a character split around a stripped apc at the last column",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 19) + "\xe1", kittyDirectRGB, "\xa5 done"},
		},
		{
			name: "an incomplete character left pending by a stripped apc at the last column",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 19) + "\xe1", kittyDirectRGB},
		},
		{
			name: "the zeros ladder at 18, a character split around a stripped apc",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 18) + "\xe1", kittyDirectRGB, "\xa5 done"},
		},
		{
			name: "the zeros ladder at 19, a character split around a stripped apc",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 19) + "\xe1", kittyDirectRGB, "\xa5 done"},
		},
		{
			name: "the zeros ladder at 20, a character split around a stripped apc",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 20) + "\xe1", kittyDirectRGB, "\xa5 done"},
		},
		{
			name: "the zeros ladder at 21, a character split around a stripped apc",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 21) + "\xe1", kittyDirectRGB, "\xa5 done"},
		},
		{
			name: "a prompt marker splitting a character",
			cols: 20, rows: 8,
			chunks: []string{"000\xe1", "\x1b]133;A\x1b\\", "\xa5 done"},
		},
		{
			name: "a prompt marker splitting a character at the wrap column",
			cols: 20, rows: 8,
			chunks: []string{strings.Repeat("0", 20) + "\xe1", "\x1b]133;A\x1b\\", "\xa5 done"},
		},
		{
			// A C1-terminated APC: the worker consumes 0x9c as ST, but the wire replacement is
			// always the 7-bit form — 0x9c alone is a stray continuation byte to the client.
			name: "a c1-terminated apc still leaves the seven-bit st",
			cols: 20, rows: 8,
			chunks: []string{"ab", kittyIntro + "a=T,f=24,s=2,v=2;QUJDRA==\x9c", " done"},
		},
		{
			// `OSC 133;A` is not grid-inert: with the cursor mid-line it breaks the line, and worker
			// and app share a Ghostty source pin, so this tripwires that against the real WASM model.
			name: "a prompt marker after output with no trailing newline",
			cols: 20, rows: 8,
			chunks: []string{"out", "\x1b]133;A\x1b\\", "$ ls\r\n", "\x1b]133;D;0\x07"},
		},
		{
			name: "markers split across feed chunks around an image",
			cols: 20, rows: 8,
			chunks: []string{
				"\x1b]13", "3;A\x07$ \x1b]133;C;cmdline_url=ic",
				"at\x07", kittyPlaceRGB(46, 16, 32, ""),
				"\r\n\x1b]133;D;0\x1b", "\\done",
			},
		},
		{
			name: "image taller than an alternate screen that keeps no history",
			cols: 20, rows: 6,
			chunks: []string{
				"\x1b[?1049h",
				"alt0\r\nalt1\r\nalt2\r\nalt3\r\nalt4\r\n\x1b[6;1Halt5",
				kittyPlaceRGB(12, 16, 128, ""),
			},
		},
	}
}

func runKittyCorpusEntry(t *testing.T, in kittyCorpusInput) kittyCorpusEntry {
	t.Helper()
	baseline := ghosttyvt.LiveTrackedRefs()

	worker := newKittyTerminal(t, in.cols, in.rows, ghosttyvt.Options{KittyImageStorageLimit: mirrorStorageLimit})
	feeder := newWireFeeder(worker, 0, nil, 0)
	if feeder == nil {
		t.Fatalf("newWireFeeder returned nil for a live terminal")
	}

	entry := kittyCorpusEntry{Name: in.name, Cols: in.cols, Rows: in.rows}
	for _, chunk := range in.chunks {
		wire, resync := feeder.feed([]byte(chunk))
		entry.Chunks = append(entry.Chunks, base64.StdEncoding.EncodeToString([]byte(chunk)))
		encoded := ""
		if len(wire) > 0 {
			encoded = base64.StdEncoding.EncodeToString(wire)
		}
		entry.Wire = append(entry.Wire, encoded)
		entry.Resync = append(entry.Resync, resync)
	}

	entry.WorkerPlainText = worker.PlainText()
	entry.WorkerViewportText = worker.ViewportText()
	entry.CursorCol, entry.CursorRow = worker.CursorPos()

	feeder.close()
	if got := ghosttyvt.LiveTrackedRefs(); got != baseline {
		t.Errorf("LiveTrackedRefs() = %d after %q, want the %d it started at", got, in.name, baseline)
	}
	return entry
}

func writeAsClient(client *ghosttyvt.Terminal, wire []byte) {
	client.Write(wire)
}

func replayKittyWire(t *testing.T, entry kittyCorpusEntry) *ghosttyvt.Terminal {
	t.Helper()
	client := newKittyTerminal(t, entry.Cols, entry.Rows, ghosttyvt.Options{})
	for i, encoded := range entry.Wire {
		if encoded == "" {
			continue
		}
		wire, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode wire chunk %d of %q: %v", i, entry.Name, err)
		}
		writeAsClient(client, wire)
	}
	return client
}

func TestKittyWireRewriteCorpus(t *testing.T) {
	inputs := kittyCorpusInputs()
	recorded := make([]kittyCorpusEntry, 0, len(inputs))
	for _, in := range inputs {
		recorded = append(recorded, runKittyCorpusEntry(t, in))
	}

	if *updateGoldens {
		writeKittyCorpus(t, recorded)
		t.Logf("regenerated %s with %d entries", kittyCorpusFileName, len(recorded))
		return
	}

	stored := readKittyCorpus(t)
	if len(stored) != len(recorded) {
		t.Fatalf("%s holds %d entries, the inputs produce %d: re-run with -update",
			kittyCorpusFileName, len(stored), len(recorded))
	}

	for i, want := range stored {
		t.Run(want.Name, func(t *testing.T) {
			assertKittyCorpusEntryEqual(t, want, recorded[i])

			if want.resynced() {
				return
			}

			client := replayKittyWire(t, want)
			if got := client.PlainText(); got != want.WorkerPlainText {
				t.Errorf("replayed history diverged from the worker\nworker:\n%s\nclient:\n%s", want.WorkerPlainText, got)
			}
			if got := client.ViewportText(); got != want.WorkerViewportText {
				t.Errorf("replayed viewport diverged from the worker\nworker:\n%s\nclient:\n%s", want.WorkerViewportText, got)
			}
			if col, row := client.CursorPos(); col != want.CursorCol || row != want.CursorRow {
				t.Errorf("replayed cursor at (%d,%d), the worker's is at (%d,%d)", col, row, want.CursorCol, want.CursorRow)
			}
		})
	}
}

func assertKittyCorpusEntryEqual(t *testing.T, want, got kittyCorpusEntry) {
	t.Helper()
	const rerun = "re-run with -update once the change is intended"
	if want.Name != got.Name || want.Cols != got.Cols || want.Rows != got.Rows {
		t.Fatalf("entry identity moved: stored %q %dx%d, recorded %q %dx%d: %s",
			want.Name, want.Cols, want.Rows, got.Name, got.Cols, got.Rows, rerun)
	}
	assertBase64SliceEqual(t, "chunks", want.Chunks, got.Chunks, rerun)
	assertBase64SliceEqual(t, "wire", want.Wire, got.Wire, rerun)
	if strings.Join(want.Resync, "|") != strings.Join(got.Resync, "|") {
		t.Errorf("resync reasons = %q, stored %q: %s", got.Resync, want.Resync, rerun)
	}
	if want.WorkerPlainText != got.WorkerPlainText {
		t.Errorf("worker history moved\nstored:\n%s\nrecorded:\n%s\n%s", want.WorkerPlainText, got.WorkerPlainText, rerun)
	}
	if want.WorkerViewportText != got.WorkerViewportText {
		t.Errorf("worker viewport moved\nstored:\n%s\nrecorded:\n%s\n%s", want.WorkerViewportText, got.WorkerViewportText, rerun)
	}
	if want.CursorCol != got.CursorCol || want.CursorRow != got.CursorRow {
		t.Errorf("worker cursor = (%d,%d), stored (%d,%d): %s", got.CursorCol, got.CursorRow, want.CursorCol, want.CursorRow, rerun)
	}
}

func assertBase64SliceEqual(t *testing.T, field string, want, got []string, rerun string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s has %d chunks, stored %d: %s", field, len(got), len(want), rerun)
	}
	for i := range want {
		if want[i] == got[i] {
			continue
		}
		t.Errorf("%s[%d] = %q, stored %q: %s", field, i, decodeForMessage(got[i]), decodeForMessage(want[i]), rerun)
	}
}

func decodeForMessage(encoded string) string {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return encoded
	}
	return string(raw)
}

func kittyCorpusPath() string {
	return filepath.Join("testdata", kittyCorpusFileName)
}

func readKittyCorpus(t *testing.T) []kittyCorpusEntry {
	t.Helper()
	raw, err := os.ReadFile(kittyCorpusPath())
	if err != nil {
		t.Fatalf("read %s: %v", kittyCorpusFileName, err)
	}
	var file kittyCorpusFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse %s: %v", kittyCorpusFileName, err)
	}
	if len(file.Entries) == 0 {
		t.Fatalf("%s holds no entries", kittyCorpusFileName)
	}
	return file.Entries
}

func writeKittyCorpus(t *testing.T, entries []kittyCorpusEntry) {
	t.Helper()
	raw, err := json.MarshalIndent(kittyCorpusFile{Description: kittyCorpusDescription, Entries: entries}, "", "  ")
	if err != nil {
		t.Fatalf("encode %s: %v", kittyCorpusFileName, err)
	}
	if err := os.WriteFile(kittyCorpusPath(), append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", kittyCorpusFileName, err)
	}
}

var kittyGroundProbeBytes = []byte{
	0x00, 0x07, 0x18, 0x1a, 0x1b, 0x20, 0x28, 0x30, 0x47, 0x50, 0x5b, 0x5c,
	0x5d, 0x5e, 0x5f, 0x6d, 0x7f, 0x80, 0x84, 0x90, 0x98, 0x9b, 0x9c, 0x9d,
	0x9e, 0x9f, 0xff,
}

var kittyGroundNamedPrefixes = []string{
	"",
	"\x1b",
	"\x1b\x1b",
	"\x1b(",
	"\x1b[1",
	"\x1b]0;t",
	"\x1bP1$r",
	"\x1bXsos",
	"\x1b^pm",
	"\x1b_Zvendor",
	"\x1b_Ga=T,f=24;AA",
	"\x1b_Ga=T;AA\x1b",
	"\x1b_Ga=T;AA\x18",
	"\x1b]0;t\x1b",
	"\x1b[1\x1b",
	"\x1b]1",
	"\x1b]133",
	"\x1b]133;",
	"\x1b]133;A",
	"\x1b]133;C;cmdline_url=ma",
	"\x1b]133;A\x1b",
	"\x1b\x1b]133;A",
}

// ghosttyInGround reports whether ghostty's parser is in ground, by the only signal the API
// exposes: a printable advances the CURSOR there and nowhere else. The CR normalizes first.
func ghosttyInGround(t *testing.T, input string) bool {
	t.Helper()
	term, err := ghosttyvt.New(20, 4, ghosttyvt.Options{})
	if err != nil {
		t.Fatalf("ghosttyvt.New: %v", err)
	}
	defer term.Close()
	term.Write([]byte(input))
	term.Write([]byte("\r"))
	beforeCol, beforeRow := term.CursorPos()
	term.Write([]byte("Z"))
	afterCol, afterRow := term.CursorPos()
	return afterCol != beforeCol || afterRow != beforeRow
}

func segmenterInGround(t *testing.T, input string) bool {
	t.Helper()
	var seg feedSegmenter
	rebuilt := make([]byte, 0, len(input))
	seg.Feed([]byte(input), func(e feedSegment) {
		rebuilt = append(rebuilt, e.Bytes...)
	})
	if got := string(rebuilt) + string(seg.pending); got != input {
		t.Fatalf("emissions rebuild %q, want %q", got, input)
	}
	return seg.mode == kittySegGround && len(seg.pending) == 0
}

func assertGroundAgrees(t *testing.T, input string) {
	t.Helper()
	want := ghosttyInGround(t, input)
	if got := segmenterInGround(t, input); got != want {
		t.Errorf("after %q: segmenter ground=%v, ghostty ground=%v", input, got, want)
	}
}

// The falsification gate for every transition in kittyseg.go's machine. A pass is not "the
// segmenter is right": it cannot see which DISPOSITION a byte got; the battery pins that.
func TestKittySegmenterGroundMatchesGhostty(t *testing.T) {
	for _, prefix := range kittyGroundNamedPrefixes {
		for b := range 0x100 {
			assertGroundAgrees(t, prefix+string([]byte{byte(b)}))
		}
	}
	for _, prefix := range kittyGroundNamedPrefixes {
		for _, a := range kittyGroundProbeBytes {
			for _, b := range kittyGroundProbeBytes {
				assertGroundAgrees(t, prefix+string([]byte{a, b}))
			}
		}
	}
	for _, a := range kittyGroundProbeBytes {
		for _, b := range kittyGroundProbeBytes {
			for _, c := range kittyGroundProbeBytes {
				assertGroundAgrees(t, string([]byte{a, b, c}))
			}
		}
	}
	for _, a := range kittyGroundProbeBytes {
		for _, b := range kittyGroundProbeBytes {
			assertGroundAgrees(t, "\x1b_G"+string([]byte{a, b}))
			assertGroundAgrees(t, "\x1b_Ga=T;AA"+string([]byte{a, b}))
			assertGroundAgrees(t, string([]byte{a, b})+"\x1b_G")
		}
	}
}
