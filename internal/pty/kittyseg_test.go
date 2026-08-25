package pty

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

const (
	kittyIntro = "\x1b_G"
	kittyST    = "\x1b\\"
)

var (
	kittyDirectRGB = kittyIntro + "a=T,f=24,s=2,v=2;QUJDRA==" + kittyST
	kittyPNGChunk1 = kittyIntro + "a=T,f=100,m=1;iVBORw0K" + kittyST
	kittyPNGChunk2 = kittyIntro + "m=1;GgoAAABJ" + kittyST
	kittyPNGFinal  = kittyIntro + "m=0;" + kittyST
	kittyQuery     = kittyIntro + "a=q,i=31,s=1,v=1,f=24;AAAA" + kittyST
	kittyEmpty     = kittyIntro + kittyST
	kittyBig       = kittyIntro + "a=T,f=32,s=8,v=8;" + strings.Repeat("QUJD", 64) + kittyST
	kittyWithBEL   = kittyIntro + "a=T,f=24;AA\x07BB" + kittyST
	kittyRecovered = kittyIntro + "i=9;BB" + kittyST
)

type kittyEmission struct {
	kind  feedSegKind
	bytes string
}

func plainEmission(s string) kittyEmission  { return kittyEmission{kind: feedSegPlain, bytes: s} }
func apcEmission(s string) kittyEmission    { return kittyEmission{kind: feedSegKittyAPC, bytes: s} }
func markerEmission(s string) kittyEmission { return kittyEmission{kind: feedSegOSC133, bytes: s} }

func (e kittyEmission) String() string {
	switch e.kind {
	case feedSegKittyAPC:
		return fmt.Sprintf("apc(%q)", e.bytes)
	case feedSegOSC133:
		return fmt.Sprintf("marker(%q)", e.bytes)
	}
	return fmt.Sprintf("plain(%q)", e.bytes)
}

type kittySegCase struct {
	name    string
	input   string
	want    []kittyEmission
	pending string
}

var kittySegBattery = []kittySegCase{
	{
		name:  "plain output holds no kitty",
		input: "hello\r\nworld ✓",
		want:  []kittyEmission{plainEmission("hello\r\nworld ✓")},
	},
	{
		name:  "one complete apc alone",
		input: kittyDirectRGB,
		want:  []kittyEmission{apcEmission(kittyDirectRGB)},
	},
	{
		name:  "apc surrounded by text",
		input: "before" + kittyDirectRGB + "after",
		want: []kittyEmission{
			plainEmission("before"),
			apcEmission(kittyDirectRGB),
			plainEmission("after"),
		},
	},
	{
		name:  "back to back apcs of a chunked transmission",
		input: kittyPNGChunk1 + kittyPNGChunk2 + kittyPNGFinal,
		want: []kittyEmission{
			apcEmission(kittyPNGChunk1),
			apcEmission(kittyPNGChunk2),
			apcEmission(kittyPNGFinal),
		},
	},
	{
		name:  "apcs separated by text",
		input: "a" + kittyDirectRGB + "b" + kittyQuery + "c",
		want: []kittyEmission{
			plainEmission("a"),
			apcEmission(kittyDirectRGB),
			plainEmission("b"),
			apcEmission(kittyQuery),
			plainEmission("c"),
		},
	},
	{
		name:  "apc with an empty payload",
		input: "x" + kittyEmpty + "y",
		want: []kittyEmission{
			plainEmission("x"),
			apcEmission(kittyEmpty),
			plainEmission("y"),
		},
	},
	{
		name:  "apc with a payload longer than any introducer scan window",
		input: "head" + kittyBig + "tail",
		want: []kittyEmission{
			plainEmission("head"),
			apcEmission(kittyBig),
			plainEmission("tail"),
		},
	},
	{
		// BEL ends an OSC, not an APC: treating it as a terminator would cut the escape in half.
		name:  "bel inside the payload is an ordinary byte",
		input: kittyWithBEL + "tail",
		want: []kittyEmission{
			apcEmission(kittyWithBEL),
			plainEmission("tail"),
		},
	},
	{
		name:  "backslash inside the payload does not terminate",
		input: kittyIntro + "a=T,f=24;A\\B" + kittyST,
		want:  []kittyEmission{apcEmission(kittyIntro + "a=T,f=24;A\\B" + kittyST)},
	},
	{
		name:  "the glyph apc is not ours",
		input: "x\x1b_25a1;GLYPH\x1b\\y",
		want:  []kittyEmission{plainEmission("x\x1b_25a1;GLYPH\x1b\\y")},
	},
	{
		name:  "an unknown apc protocol byte is not ours",
		input: "\x1b_Zwhatever\x1b\\",
		want:  []kittyEmission{plainEmission("\x1b_Zwhatever\x1b\\")},
	},
	{
		name:  "ordinary osc and csi sequences stay plain",
		input: "\x1b]0;title\x07\x1b[1;31mred\x1b[0m" + kittyQuery + "\x1b]133;A\x07",
		want: []kittyEmission{
			plainEmission("\x1b]0;title\x07\x1b[1;31mred\x1b[0m"),
			apcEmission(kittyQuery),
			markerEmission("\x1b]133;A\x07"),
		},
	},
	{
		name:  "esc st with no introducer stays plain",
		input: "a\x1b\\b",
		want:  []kittyEmission{plainEmission("a\x1b\\b")},
	},
	{
		// Measured: ESC ESC leaves ghostty mid-escape, so extracting the APC that follows leaves the client's parser one escape behind.
		name:  "an esc before the introducer keeps the apc on the wire",
		input: "\x1b" + kittyRecovered,
		want:  []kittyEmission{plainEmission("\x1b" + kittyRecovered)},
	},
	{
		name:  "a lone esc between two apcs suppresses the second",
		input: kittyDirectRGB + "\x1b" + kittyRecovered,
		want: []kittyEmission{
			apcEmission(kittyDirectRGB),
			plainEmission("\x1b" + kittyRecovered),
		},
	},
	{
		name:  "a stray esc abandons the apc",
		input: kittyIntro + "a=T,f=24;AAAA\x1b[0mback to text",
		want:  []kittyEmission{plainEmission(kittyIntro + "a=T,f=24;AAAA\x1b[0mback to text")},
	},
	{
		// The ESC that abandons the first APC is also what opens the second.
		name:  "a stray esc that starts a new apc keeps both on the wire",
		input: kittyIntro + "a=T;AA" + kittyRecovered + "tail",
		want:  []kittyEmission{plainEmission(kittyIntro + "a=T;AA" + kittyRecovered + "tail")},
	},
	{
		name:  "an abandoned apc does not hide a later one",
		input: kittyIntro + "a=T;AA\x1b[0m" + kittyRecovered + "tail",
		want: []kittyEmission{
			plainEmission(kittyIntro + "a=T;AA\x1b[0m"),
			apcEmission(kittyRecovered),
			plainEmission("tail"),
		},
	},
	{
		name:    "the stream ends on a lone esc",
		input:   "tail\x1b",
		want:    []kittyEmission{plainEmission("tail")},
		pending: "\x1b",
	},
	{
		name:    "the stream ends on a partial introducer",
		input:   "tail\x1b_",
		want:    []kittyEmission{plainEmission("tail")},
		pending: "\x1b_",
	},
	{
		name:    "the stream ends inside an apc payload",
		input:   "text" + kittyIntro + "a=T,f=24;AAAA",
		want:    []kittyEmission{plainEmission("text")},
		pending: kittyIntro + "a=T,f=24;AAAA",
	},
	{
		name:    "the stream ends on a partial terminator",
		input:   kittyIntro + "a=T;AA\x1b",
		pending: kittyIntro + "a=T;AA\x1b",
	},
	{
		name:  "an abandoned apc followed by a partial introducer holds nothing",
		input: kittyIntro + "a=T;AA\x1b_",
		want:  []kittyEmission{plainEmission(kittyIntro + "a=T;AA\x1b_")},
	},

	{
		name:  "a marker terminated by bel",
		input: "out\x1b]133;A\x07$ ",
		want: []kittyEmission{
			plainEmission("out"),
			markerEmission("\x1b]133;A\x07"),
			plainEmission("$ "),
		},
	},
	{
		name:  "a marker terminated by st",
		input: "\x1b]133;D;0" + kittyST + "tail",
		want: []kittyEmission{
			markerEmission("\x1b]133;D;0" + kittyST),
			plainEmission("tail"),
		},
	},
	{
		name:  "a marker with an unknown subtype is still consumed",
		input: "a\x1b]133;Z;stuff\x07b",
		want: []kittyEmission{
			plainEmission("a"),
			markerEmission("\x1b]133;Z;stuff\x07"),
			plainEmission("b"),
		},
	},
	{
		// Fuzz reproducer. Measured: ghostty ends the OSC on this ESC and executes what follows.
		name:  "a marker cut short by a stray esc stays plain",
		input: "\x1b]133;A\x1b0Z",
		want:  []kittyEmission{plainEmission("\x1b]133;A\x1b0Z")},
	},
	{
		// Fuzz reproducer. The marker's ESC ] is not in ground, so cutting it takes the opened escape's bytes with it.
		name:  "a marker whose introducer was never in ground stays plain",
		input: "\x1b\x1b]133;A" + kittyST + "00",
		want:  []kittyEmission{plainEmission("\x1b\x1b]133;A" + kittyST + "00")},
	},
	{
		// Measured: CAN and SUB dispatch the marker for ghostty, but the client's parser knows only BEL and ST.
		name:  "a marker cut short by can stays plain",
		input: "\x1b]133;A\x18tail",
		want:  []kittyEmission{plainEmission("\x1b]133;A\x18tail")},
	},
	{
		name:  "a marker cut short by sub stays plain",
		input: "\x1b]133;B\x1atail",
		want:  []kittyEmission{plainEmission("\x1b]133;B\x1atail")},
	},
	{
		// Measured: C1 ST does not end an OSC.
		name:  "c1 st inside a marker payload is ordinary payload",
		input: "\x1b]133;C;cmdline_url=a\x9cb\x07",
		want:  []kittyEmission{markerEmission("\x1b]133;C;cmdline_url=a\x9cb\x07")},
	},
	{
		// Measured to dispatch, but osc133Prefix is the only introducer this segmenter recognises.
		name:  "a c1-introduced marker stays plain",
		input: "\x9d133;A\x07",
		want:  []kittyEmission{plainEmission("\x9d133;A\x07")},
	},
	{
		name:  "an osc that diverges from the marker prefix stays plain",
		input: "\x1b]13;x\x07\x1b]1;y\x07\x1b]134;z\x07",
		want:  []kittyEmission{plainEmission("\x1b]13;x\x07\x1b]1;y\x07\x1b]134;z\x07")},
	},
	{
		name:  "two markers around output in one run",
		input: "\x1b]133;A\x07prompt output\x1b]133;D;0\x07",
		want: []kittyEmission{
			markerEmission("\x1b]133;A\x07"),
			plainEmission("prompt output"),
			markerEmission("\x1b]133;D;0\x07"),
		},
	},
	{
		name:    "the stream ends inside a marker prefix",
		input:   "tail\x1b]13",
		want:    []kittyEmission{plainEmission("tail")},
		pending: "\x1b]13",
	},
	{
		name:    "the stream ends inside a marker payload",
		input:   "\x1b]133;C;cmdline_url=ma",
		pending: "\x1b]133;C;cmdline_url=ma",
	},
	{
		name:    "the stream ends on a marker's partial terminator",
		input:   "\x1b]133;A\x1b",
		pending: "\x1b]133;A\x1b",
	},
	{
		// ESC _ is not in an OSC's terminating byte set: the ESC ends the OSC and the APC opens from escape state.
		name:  "an apc pattern inside a marker payload keeps both on the wire",
		input: "\x1b]133;C;cmdline_url=x" + kittyRecovered + "tail",
		want:  []kittyEmission{plainEmission("\x1b]133;C;cmdline_url=x" + kittyRecovered + "tail")},
	},

	// An APC pattern inside a foreign string is text to ghostty; cutting it would take the string's own bytes.
	{
		// Fuzz reproducer: the ESC \ ends the SOS, not an APC.
		name:  "an apc pattern inside an sos string stays plain",
		input: "\x1bX" + kittyIntro + kittyST + "0",
		want:  []kittyEmission{plainEmission("\x1bX" + kittyIntro + kittyST + "0")},
	},
	{
		name:  "an apc pattern inside an osc stays plain",
		input: "\x1b]0;title" + kittyIntro + "a=T;AA\x07tail",
		want:  []kittyEmission{plainEmission("\x1b]0;title" + kittyIntro + "a=T;AA\x07tail")},
	},
	{
		name:  "an apc pattern inside a dcs stays plain",
		input: "\x1bP1$r" + kittyIntro + "a=T;AA" + kittyST + "tail",
		want:  []kittyEmission{plainEmission("\x1bP1$r" + kittyIntro + "a=T;AA" + kittyST + "tail")},
	},
	{
		name:  "an apc pattern inside a pm string stays plain",
		input: "\x1b^note" + kittyIntro + "a=T;AA" + kittyST + "tail",
		want:  []kittyEmission{plainEmission("\x1b^note" + kittyIntro + "a=T;AA" + kittyST + "tail")},
	},
	{
		name:  "an apc pattern inside a foreign apc stays plain",
		input: "\x1b_Zvendor" + kittyIntro + "a=T;AA" + kittyST + "tail",
		want:  []kittyEmission{plainEmission("\x1b_Zvendor" + kittyIntro + "a=T;AA" + kittyST + "tail")},
	},
	{
		name:  "an apc after a terminated sos is extracted",
		input: "\x1bXsos" + kittyST + kittyDirectRGB + "tail",
		want: []kittyEmission{
			plainEmission("\x1bXsos" + kittyST),
			apcEmission(kittyDirectRGB),
			plainEmission("tail"),
		},
	},
	{
		name:  "an apc after a bel-terminated osc is extracted",
		input: "\x1b]0;title\x07" + kittyDirectRGB + "tail",
		want: []kittyEmission{
			plainEmission("\x1b]0;title\x07"),
			apcEmission(kittyDirectRGB),
			plainEmission("tail"),
		},
	},
	{
		// Measured: C1 ST ends every string type EXCEPT an OSC.
		name:  "c1 st ends an sos but not an osc",
		input: "\x1bXsos\x9c" + kittyDirectRGB + "\x1b]0;t\x9c" + kittyRecovered + "\x07" + kittyQuery,
		want: []kittyEmission{
			plainEmission("\x1bXsos\x9c"),
			apcEmission(kittyDirectRGB),
			plainEmission("\x1b]0;t\x9c" + kittyRecovered + "\x07"),
			apcEmission(kittyQuery),
		},
	},
	{
		// A raw C1 introducer opens a string from escape state but is ordinary text in ground.
		name:  "a c1 introducer opens a string only after an esc",
		input: "\x1b\x9e" + kittyIntro + "a=T;AA" + kittyST + "\x9e" + kittyDirectRGB,
		want: []kittyEmission{
			plainEmission("\x1b\x9e" + kittyIntro + "a=T;AA" + kittyST + "\x9e"),
			apcEmission(kittyDirectRGB),
		},
	},

	// Ghostty ends a kitty APC on each of these bytes, and each has its own effect on the grid.
	{
		// Fuzz reproducer: 0x84 (IND) ends the APC and scrolls.
		name:  "an ind inside the payload ends the apc",
		input: kittyIntro + "a=T;AA\x840" + kittyST,
		want:  []kittyEmission{plainEmission(kittyIntro + "a=T;AA\x840" + kittyST)},
	},
	{
		name:  "a can inside the payload ends the apc",
		input: kittyIntro + "a=T;AA\x18text",
		want:  []kittyEmission{plainEmission(kittyIntro + "a=T;AA\x18text")},
	},
	{
		name:  "a sub inside the payload ends the apc",
		input: kittyIntro + "a=T;AA\x1atext",
		want:  []kittyEmission{plainEmission(kittyIntro + "a=T;AA\x1atext")},
	},
	{
		name:  "an apc right after an aborting byte is extracted",
		input: kittyIntro + "a=T;AA\x18" + kittyDirectRGB + "tail",
		want: []kittyEmission{
			plainEmission(kittyIntro + "a=T;AA\x18"),
			apcEmission(kittyDirectRGB),
			plainEmission("tail"),
		},
	},
	{
		// Measured by dispatch, not by spec: 98, 9e and 9f are payload inside a string.
		name:  "c1 sos, pm and apc bytes are ordinary payload",
		input: kittyIntro + "a=T;A\x98B\x9eC\x9fD" + kittyST + "tail",
		want: []kittyEmission{
			apcEmission(kittyIntro + "a=T;A\x98B\x9eC\x9fD" + kittyST),
			plainEmission("tail"),
		},
	},
	{
		// The APC dispatches truncated at the byte and a DCS takes over, so the ST closes THAT.
		name:  "a c1 dcs byte ends the apc and opens a string",
		input: kittyIntro + "a=T;A\x90B" + kittyST + "tail",
		want:  []kittyEmission{plainEmission(kittyIntro + "a=T;A\x90B" + kittyST + "tail")},
	},
	{
		// 9b opens a CSI, which ends at its own final byte rather than at ST.
		name:  "a c1 csi byte ends the apc and the next apc is extracted",
		input: kittyIntro + "a=T;A\x9b0m" + kittyDirectRGB,
		want: []kittyEmission{
			plainEmission(kittyIntro + "a=T;A\x9b0m"),
			apcEmission(kittyDirectRGB),
		},
	},
	{
		// 9d opens an OSC, which ends on BEL and not on C1 ST.
		name:  "a c1 osc byte ends the apc and swallows to bel",
		input: kittyIntro + "a=T;A\x9dtitle\x07" + kittyDirectRGB,
		want: []kittyEmission{
			plainEmission(kittyIntro + "a=T;A\x9dtitle\x07"),
			apcEmission(kittyDirectRGB),
		},
	},
	{
		// C1 ST terminates a kitty APC exactly as ESC \ does.
		name:  "c1 st terminates and is stripped with the apc",
		input: "head" + kittyIntro + "a=T,f=24;AAAA\x9ctail",
		want: []kittyEmission{
			plainEmission("head"),
			apcEmission(kittyIntro + "a=T,f=24;AAAA\x9c"),
			plainEmission("tail"),
		},
	},

	{
		name:  "an apc that cancels a csi stays plain",
		input: "\x1b[1" + kittyRecovered + "tail",
		want:  []kittyEmission{plainEmission("\x1b[1" + kittyRecovered + "tail")},
	},
	{
		name:  "an apc after a finished csi is extracted",
		input: "\x1b[1;31m" + kittyDirectRGB + "tail",
		want: []kittyEmission{
			plainEmission("\x1b[1;31m"),
			apcEmission(kittyDirectRGB),
			plainEmission("tail"),
		},
	},
	{
		name:  "a can ends a csi and the next apc is extracted",
		input: "\x1b[1\x18" + kittyDirectRGB,
		want: []kittyEmission{
			plainEmission("\x1b[1\x18"),
			apcEmission(kittyDirectRGB),
		},
	},
	{
		// An escape with intermediates reaches ground only at its final byte.
		name:  "an apc after a charset designation is extracted",
		input: "\x1b(B" + kittyDirectRGB,
		want: []kittyEmission{
			plainEmission("\x1b(B"),
			apcEmission(kittyDirectRGB),
		},
	},
	{
		name:    "the stream ends mid-escape and the next chunk decides",
		input:   "\x1b[1;31m\x1b",
		want:    []kittyEmission{plainEmission("\x1b[1;31m")},
		pending: "\x1b",
	},
	{
		name:  "an unterminated foreign string holds nothing",
		input: "\x1bXsos and more",
		want:  []kittyEmission{plainEmission("\x1bXsos and more")},
	},
}

type segTB interface {
	Helper()
	Fatalf(format string, args ...any)
}

func runKittySegmenter(t segTB, chunks []string) ([]kittyEmission, string) {
	t.Helper()
	seg := &feedSegmenter{}
	var out []kittyEmission
	for _, chunk := range chunks {
		out = feedKitty(t, seg, chunk, out)
	}
	return out, string(seg.pending)
}

func feedKitty(t segTB, seg *feedSegmenter, chunk string, out []kittyEmission) []kittyEmission {
	t.Helper()
	input := []byte(chunk)
	seg.Feed(input, func(s feedSegment) {
		switch {
		case len(s.Bytes) == 0:
			t.Fatalf("emit called with an empty run")
		case s.Kind != feedSegPlain:
			out = append(out, kittyEmission{kind: s.Kind, bytes: string(s.Bytes)})
		default:
			if n := len(out); n > 0 && out[n-1].kind == feedSegPlain {
				out[n-1].bytes += string(s.Bytes)
				return
			}
			out = append(out, plainEmission(string(s.Bytes)))
		}
	})
	if string(input) != chunk {
		t.Fatalf("the segmenter modified the chunk: %q, want %q", input, chunk)
	}
	return out
}

func formatKittyEmissions(e []kittyEmission) string {
	parts := make([]string, 0, len(e))
	for _, item := range e {
		parts = append(parts, item.String())
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func kittyEmissionsEqual(got, want []kittyEmission) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type kittyChunking struct {
	label  string
	chunks []string
}

func splitCycling(s string, sizes ...int) []string {
	var chunks []string
	for i := 0; len(s) > 0; i++ {
		n := min(sizes[i%len(sizes)], len(s))
		chunks = append(chunks, s[:n])
		s = s[n:]
	}
	return chunks
}

func kittyChunkings(input string) []kittyChunking {
	out := []kittyChunking{{label: "whole", chunks: []string{input}}}
	for split := range len(input) + 1 {
		out = append(out, kittyChunking{
			label:  fmt.Sprintf("split at %d", split),
			chunks: []string{input[:split], input[split:]},
		})
	}
	byteAtATime := make([]string, 0, len(input))
	for i := range len(input) {
		byteAtATime = append(byteAtATime, input[i:i+1])
	}
	out = append(out, kittyChunking{label: "one byte per call", chunks: byteAtATime})
	for _, sizes := range [][]int{{1, 2, 3, 5, 8, 13}, {7, 1, 4, 2}, {2, 11, 3}} {
		out = append(out, kittyChunking{
			label:  fmt.Sprintf("cycling %v", sizes),
			chunks: splitCycling(input, sizes...),
		})
	}
	return out
}

func TestKittyAPCSegmenterBatteryUnderEveryChunking(t *testing.T) {
	for _, c := range kittySegBattery {
		t.Run(c.name, func(t *testing.T) {
			for _, chunking := range kittyChunkings(c.input) {
				got, pending := runKittySegmenter(t, chunking.chunks)
				if !kittyEmissionsEqual(got, c.want) {
					t.Fatalf("%s:\n got: %s\nwant: %s",
						chunking.label, formatKittyEmissions(got), formatKittyEmissions(c.want))
				}
				if pending != c.pending {
					t.Fatalf("%s: pending %q, want %q", chunking.label, pending, c.pending)
				}
			}
		})
	}
}

func TestKittyAPCSegmenterEmitsEveryByteExactlyOnce(t *testing.T) {
	for _, c := range kittySegBattery {
		t.Run(c.name, func(t *testing.T) {
			for _, chunking := range kittyChunkings(c.input) {
				got, pending := runKittySegmenter(t, chunking.chunks)
				var rebuilt strings.Builder
				for _, e := range got {
					rebuilt.WriteString(e.bytes)
				}
				rebuilt.WriteString(pending)
				if rebuilt.String() != c.input {
					t.Fatalf("%s: emissions rebuild %q, want %q",
						chunking.label, rebuilt.String(), c.input)
				}
			}
		})
	}
}

func TestKittyAPCSegmenterJoinsAnAPCSplitAcrossChunks(t *testing.T) {
	apc := kittyIntro + "a=T,f=24,s=1,v=1;QQ==" + kittyST
	cases := []struct {
		name   string
		chunks []string
	}{
		{"mid introducer", []string{"text\x1b", apc[1:] + "tail"}},
		{"mid payload", []string{"text" + apc[:12], apc[12:] + "tail"}},
		{"mid st", []string{"text" + apc[:len(apc)-1], apc[len(apc)-1:] + "tail"}},
	}
	want := []kittyEmission{plainEmission("text"), apcEmission(apc), plainEmission("tail")}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, pending := runKittySegmenter(t, c.chunks)
			if !kittyEmissionsEqual(got, want) {
				t.Fatalf("got %s, want %s", formatKittyEmissions(got), formatKittyEmissions(want))
			}
			if pending != "" {
				t.Fatalf("pending %q, want nothing held", pending)
			}
		})
	}
}

func TestKittyAPCSegmenterFastPathPassesTheChunkThrough(t *testing.T) {
	var seg feedSegmenter
	chunk := []byte("plain output with no escapes\r\n")
	calls := 0
	seg.Feed(chunk, func(s feedSegment) {
		calls++
		if s.Kind != feedSegPlain {
			t.Fatalf("a plain chunk produced a %v emission: %q", s.Kind, s.Bytes)
		}
		if len(s.Bytes) != len(chunk) || &s.Bytes[0] != &chunk[0] {
			t.Fatalf("a plain chunk was copied: emitted %q, want the input slice itself", s.Bytes)
		}
	})
	if calls != 1 {
		t.Fatalf("a plain chunk produced %d emissions, want 1", calls)
	}
	if seg.pending != nil {
		t.Fatalf("a plain chunk left %q pending", seg.pending)
	}
}

func TestKittyAPCSegmenterAbandonsAnOversizedAPC(t *testing.T) {
	const head = kittyIntro + "a=T,f=24,s=4096,v=4096;"
	flood := make([]byte, len(head)+kittySegMaxPendingBytes+1)
	copy(flood, head)
	for i := len(head); i < len(flood); i++ {
		flood[i] = 'A'
	}

	var seg feedSegmenter
	consumed := 0
	seg.Feed(flood, func(s feedSegment) {
		if s.Kind != feedSegPlain {
			t.Fatalf("an oversized apc was extracted (%d bytes)", len(s.Bytes))
		}
		if !bytes.Equal(s.Bytes, flood[consumed:consumed+len(s.Bytes)]) {
			t.Fatalf("plain run at %d does not match the input", consumed)
		}
		consumed += len(s.Bytes)
	})
	if consumed != len(flood) {
		t.Fatalf("flushed %d bytes of the oversized apc, want all %d", consumed, len(flood))
	}
	if seg.pending != nil {
		t.Fatalf("the oversized apc left %d bytes pending, want none", len(seg.pending))
	}

	// Flushing does not put the stream back in ground: ghostty is still inside the unterminated APC.
	emissions := feedKitty(t, &seg, kittyRecovered, nil)
	if !kittyEmissionsEqual(emissions, []kittyEmission{plainEmission(kittyRecovered)}) {
		t.Fatalf("while the flooded apc is still open, got %s, want it all plain", formatKittyEmissions(emissions))
	}
	emissions = feedKitty(t, &seg, kittyDirectRGB, nil)
	if !kittyEmissionsEqual(emissions, []kittyEmission{apcEmission(kittyDirectRGB)}) {
		t.Fatalf("after recovering, got %s, want the next apc", formatKittyEmissions(emissions))
	}
	if seg.pending != nil {
		t.Fatalf("pending %q, want nothing held", seg.pending)
	}
}

func BenchmarkKittyAPCSegmenterUnterminatedAPC(b *testing.B) {
	const chunkSize = 4096
	head := []byte(kittyIntro + "a=T,f=24,s=4096,v=4096;")
	chunk := bytes.Repeat([]byte("A"), chunkSize)

	for _, size := range []int{4 << 20, 16 << 20, 64 << 20} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				var seg feedSegmenter
				var emitted int
				sink := func(s feedSegment) { emitted += len(s.Bytes) }
				seg.Feed(head, sink)
				for fed := 0; fed < size; fed += chunkSize {
					seg.Feed(chunk, sink)
				}
				if emitted != 0 {
					b.Fatalf("an unterminated apc emitted %d bytes before the tripwire", emitted)
				}
			}
		})
	}
}
