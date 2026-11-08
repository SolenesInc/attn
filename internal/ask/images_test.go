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
		"not inside code":                     {"`![s](span.png)` ![r](real.png)\n\n```\n![f](fence.png)\n```\n\n    ![i](indented.png)", []string{"/work/real.png"}},
		"inside lists and quotes":             {"- ![l](list.png)\n  - ![n](nested.png)\n\n> ![q](quote.png)", []string{"/work/list.png", "/work/nested.png", "/work/quote.png"}},
		"a reference image":                   {"![a][Logo] ![logo][]\n\n[logo]: <logo two.png> \"Logo\"", []string{"/work/logo two.png"}},
		"an escaped bracket and parentheses":  {`\![no](no.png) ![a\]b](esc.png) ![p](paren\(1\).png)`, []string{"/work/esc.png", "/work/paren(1).png"}},
		"an image inside a link":              {"[![a](linked.png)](https://example.com)", []string{"/work/linked.png"}},
		"table cells":                         {"| a | b |\n|---|---|\n| ![c](cell.png) | <img src=\"html.png\"> |\n| ![p](a\\|b.png) | `![s](span.png)` |", []string{"/work/cell.png", "/work/html.png", "/work/a|b.png"}},
		"no span across table cells":          {"| Key | Shot | Fallback |\n| --- | --- | --- |\n| ` | ![s](shot.png) | `Esc` |\n| ` | ![e](extra.png) |", []string{"/work/shot.png", "/work/extra.png"}},
		"a table after a paragraph line":      {"Keys:\n| a | b |\n| - | - |\n| ` | ![t](after.png) ` |", []string{"/work/after.png"}},
		"no cell past the delimiter row":      {"| a | b |\n| :-: | --: |\n| x | y | ![e](excess.png) |", nil},
		"an html img across lines":            {"<img\n  src=\"wrapped.png\">", []string{"/work/wrapped.png"}},
		"markdown inside an html block":       {"<details>\n\n![d](details.png)\n\n</details>", []string{"/work/details.png"}},
	} {
		t.Run(name, func(t *testing.T) {
			parts := []protocol.AskPart{{Markdown: protocol.Ptr(tc.markdown)}}
			if got := ask.ImagePaths(parts, "/work"); !slices.Equal(got, tc.want) {
				t.Fatalf("ImagePaths(%q) = %q, want %q", tc.markdown, got, tc.want)
			}
		})
	}
}
