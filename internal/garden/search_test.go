package garden

import (
	"strings"
	"testing"
)

func TestSnippet(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		spans     [][2]int
		check     func(snippet string) bool
		expect    string
		wantSpans []string
	}{
		{
			name:  "a long line is elided on both sides around the match within the budget",
			text:  "first line\n" + strings.Repeat("padding ", 40) + "NEEDLE" + strings.Repeat(" trailing", 40),
			spans: [][2]int{{331, 337}},
			check: func(s string) bool {
				return strings.Contains(s, "NEEDLE") && len([]rune(s)) <= SnippetChars+2 && strings.HasPrefix(s, "…") && strings.HasSuffix(s, "…")
			},
			expect:    "the match, cut on both sides, within the budget",
			wantSpans: []string{"NEEDLE"},
		},
		{
			name:      "a short line prints whole and unindented",
			text:      "  a note about the garden  ",
			spans:     [][2]int{{19, 25}},
			check:     func(s string) bool { return s == "a note about the garden" },
			expect:    "the whole line",
			wantSpans: []string{"garden"},
		},
		{
			name:      "case folding that changes byte length keeps the match intact",
			text:      strings.Repeat("İ", 30) + " the needle is here " + strings.Repeat("ü", 200),
			spans:     [][2]int{{65, 71}},
			check:     func(s string) bool { return strings.Contains(s, "needle is here") },
			expect:    "the match intact",
			wantSpans: []string{"needle"},
		},
		{
			name:  "left-elided unicode line projects byte spans past the ellipsis",
			text:  strings.Repeat("ü", 80) + " NEEDLE " + strings.Repeat("ü", 120),
			spans: [][2]int{{161, 167}},
			check: func(s string) bool {
				return strings.HasPrefix(s, "…") && strings.HasSuffix(s, "…") && strings.Contains(s, "NEEDLE")
			},
			expect:    "left- and right-elided unicode window around NEEDLE",
			wantSpans: []string{"NEEDLE"},
		},
		{
			name:      "the line carrying every term wins",
			text:      "# Finish the garden\n\n" + strings.Repeat("filler about plots and edges\n", 20) + "Search is its own verb, with snippets across the whole garden.\n",
			spans:     [][2]int{{13, 19}, {601, 607}, {656, 662}},
			check:     func(s string) bool { return strings.HasPrefix(s, "Search is its own verb") },
			expect:    "the line saying both terms",
			wantSpans: []string{"Search", "garden"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, spans := SnippetWithSpans(tc.text, tc.spans)
			if !tc.check(got) {
				t.Errorf("Snippet = %q, want %s", got, tc.expect)
			}
			if len(spans) != len(tc.wantSpans) {
				t.Fatalf("SnippetWithSpans returned %d spans %v on %q, want %v", len(spans), spans, got, tc.wantSpans)
			}
			for i, span := range spans {
				if span[0] < 0 || span[1] > len(got) || span[0] >= span[1] {
					t.Fatalf("SnippetWithSpans returned out-of-bounds span %v for %q", span, got)
				}
				if slice := got[span[0]:span[1]]; slice != tc.wantSpans[i] {
					t.Errorf("span[%d] = %q, want %q in %q", i, slice, tc.wantSpans[i], got)
				}
			}
		})
	}
}
