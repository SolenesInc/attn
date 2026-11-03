package ask_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/protocol"
)

func TestImagePathsResolveEachImageOnceAsTheAppShowsIt(t *testing.T) {
	for name, tc := range map[string]struct {
		markdown string
		want     []string
	}{
		"a relative markdown image":           {"![a](shots/board.png)", []string{"/work/shots/board.png"}},
		"an escaped space":                    {"![a](my%20board.png)", []string{"/work/my board.png"}},
		"an angle-bracketed space":            {"![a](<my board.png>)", []string{"/work/my board.png"}},
		"a markdown entity decoded once":      {"![a](a&amp;copy;.png)", []string{"/work/a&copy;.png"}},
		"an html img entity decoded once":     {`<img src="a&amp;copy;.png">`, []string{"/work/a&copy;.png"}},
		"an html img with a space":            {`<p><img src="my board.png" alt="b"></p>`, []string{"/work/my board.png"}},
		"an absolute path":                    {"![a](/tmp/x.webp)", []string{"/tmp/x.webp"}},
		"each path once, in reading order":    {"![b](b.png) ![a](a.png) ![b](./b.png)", []string{"/work/b.png", "/work/a.png"}},
		"no remote, anchor or non-image file": {"![r](https://x.dev/r.png) ![h](#top) ![n](notes.txt)", nil},
	} {
		t.Run(name, func(t *testing.T) {
			parts := []protocol.AskPart{{Markdown: protocol.Ptr(tc.markdown)}}
			if got := ask.ImagePaths(parts, "/work"); !slices.Equal(got, tc.want) {
				t.Fatalf("ImagePaths(%q) = %q, want %q", tc.markdown, got, tc.want)
			}
		})
	}
}
