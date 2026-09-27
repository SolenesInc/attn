package garden

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestStepSlug(t *testing.T) {
	cases := map[string]string{
		"Plant and see":                         "plant-see",
		"  Edges & ready!  ":                    "edges-ready",
		"The plan lives in the garden":          "plan-lives-garden",
		"attn seed plant (one line)":            "attn-seed-plant-one-line",
		"...":                                   "seed",
		"Slice 5 — plots and dispatch":          "slice-5-plots-dispatch",
		"CamelCase Title With 123 Numbers":      "camelcase-title-123-numbers",
		"Mermaid rendered in the grid, in Rust": "mermaid-rendered-grid-rust",
		"Spike 1: cells on the GPU":             "spike-1-cells-gpu",
		"The One":                               "one",
		"Of the":                                "of-the",
	}
	for title, want := range cases {
		if got := StepSlug(title); got != want {
			t.Fatalf("StepSlug(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestStepSlugShapeHolds(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		title := rapid.String().Draw(t, "title")
		slug := StepSlug(title)
		if slug == "" {
			t.Fatalf("StepSlug(%q) produced an empty slug", title)
		}
		if len([]rune(slug)) > MaxSlugChars {
			t.Fatalf("StepSlug(%q) produced %d characters, past the %d cap", title, len([]rune(slug)), MaxSlugChars)
		}
		if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
			t.Fatalf("StepSlug(%q) = %q, which is edged with a dash", title, slug)
		}
		if strings.Contains(slug, "--") {
			t.Fatalf("StepSlug(%q) = %q, which doubles a dash", title, slug)
		}
		for _, r := range slug {
			isLower := r >= 'a' && r <= 'z'
			isDigit := r >= '0' && r <= '9'
			if !isLower && !isDigit && r != '-' {
				t.Fatalf("StepSlug(%q) = %q, which holds %q", title, slug, string(r))
			}
		}
	})
}
