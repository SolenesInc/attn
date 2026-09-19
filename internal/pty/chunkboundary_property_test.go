package pty

import (
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

var streamFragments = []string{
	"hello",
	"a",
	"",
	"\r\n",
	"é",
	"⠀",
	"🙂",
	"\xe1",
	"\xa5",
	"\x07",
	"\x18", "\x1a",
	"\x80", "\x9c",
	"\x90", "\x9b", "\x9d", "\x9e", "\x9f",
	"\x1b",
	"\x1b[",
	"\x1b]",
	"\x1b_",
	"\x1b_G",
	"\x1b(B",
	"\x1b\\",
	"\x1b[0m",
	"\x1b]0;window title\x07",
	"\x1b]0;title with \x1b in it\x07",
	"\x1b]777;notify;Claude Code;waiting\x07",
	"\x1b]13",
	"\x1b]133",
	"\x1b]133;",
	"\x1b]134;x\x07",
	"\x1b]133;A\x07",
	"\x1b]133;B\x1b\\",
	"\x1b]133;C;cmdline=ls -la\x07",
	"\x1b]133;D;0\x07",
	"\x1b]133;D;127\x1b\\",
	"\x1b]133;A",
	"\x1b]133;A\x1b[",
	kittyIntro + "a=T,f=24,s=1,v=1;QQ==" + kittyST,
	kittyIntro + "a=T,f=100,m=1;iVBORw0K" + kittyST,
	kittyIntro + "a=T,f=24;AA\x07BB" + kittyST,
	kittyIntro + "m=1;GgoAAABJ",
	kittyIntro + "i=1;AA\x9c",
	kittyIntro + "i=2;AA\x18",
	kittyST,
}

func drawStream(t *rapid.T) string {
	var b strings.Builder
	for range rapid.IntRange(0, 8).Draw(t, "fragments") {
		b.WriteString(rapid.SampledFrom(streamFragments).Draw(t, "fragment"))
	}
	return b.String()
}

func drawChunking(t *rapid.T, input string) []string {
	if len(input) < 2 {
		return []string{input}
	}
	cuts := rapid.SliceOfNDistinct(
		rapid.IntRange(1, len(input)-1),
		0, min(6, len(input)),
		func(i int) int { return i },
	).Draw(t, "cuts")
	slices.Sort(cuts)

	chunks := make([]string, 0, len(cuts)+1)
	prev := 0
	for _, cut := range cuts {
		chunks = append(chunks, input[prev:cut])
		prev = cut
	}
	return append(chunks, input[prev:])
}

func TestFeedSegmenterIsChunkBoundaryInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		input := drawStream(t)
		wantEmissions, wantPending := runKittySegmenter(t, []string{input})

		chunks := drawChunking(t, input)
		gotEmissions, gotPending := runKittySegmenter(t, chunks)

		if !kittyEmissionsEqual(gotEmissions, wantEmissions) {
			t.Fatalf("chunked as %q:\n got: %s\nwant: %s",
				chunks, formatKittyEmissions(gotEmissions), formatKittyEmissions(wantEmissions))
		}
		if gotPending != wantPending {
			t.Fatalf("chunked as %q: holds %q, want %q", chunks, gotPending, wantPending)
		}

		var rebuilt strings.Builder
		for _, e := range gotEmissions {
			rebuilt.WriteString(e.bytes)
		}
		rebuilt.WriteString(gotPending)
		if rebuilt.String() != input {
			t.Fatalf("chunked as %q: emissions rebuild %q, want %q", chunks, rebuilt.String(), input)
		}
	})
}

func TestOSCScannerIsChunkBoundaryInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		input := drawStream(t)
		if len(input) > oscScanMaxPending/2 {
			t.Fatalf("a generated stream is %d bytes, past half the scanner's %d-byte abandon tripwire; "+
				"this property does not hold up there, so the fragment pool has to stay under it",
				len(input), oscScanMaxPending)
		}
		want := scanAll(input)

		chunks := drawChunking(t, input)
		got := scanAll(chunks...)

		if len(got) != len(want) {
			t.Fatalf("chunked as %q: got %+v, want %+v", chunks, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("chunked as %q, sequence %d: got %+v, want %+v", chunks, i, got[i], want[i])
			}
		}
	})
}
