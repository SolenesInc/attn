package crew

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func primingHolding(seeds int, handoff string) Priming {
	p := Priming{Name: "Trellis", HomeDir: "/homes/trellis", HandoffName: "2026-08-13T22-20Z-trellis.md", Handoff: "Where I left off.", GardenRead: true}
	for i := range seeds {
		p.Claims = append(p.Claims, ClaimedSeed{ID: fmt.Sprintf("s-held%02d", i), Slug: fmt.Sprintf("held-seed-%d", i), Title: fmt.Sprintf("Held seed %d", i), Handoff: handoff})
	}
	p.ClaimedTotal = seeds
	return p
}

func TestPrimingBudgets(t *testing.T) {
	letterAtTheLimit := strings.Repeat("x", MaxHandoffBytes)
	noteAtTheBudget := strings.Repeat("x", MaxClaimedHandoffBytes)
	cases := []struct {
		name     string
		priming  Priming
		want     []string
		unwanted []string
	}{
		{
			name:     "a letter at the filing limit is inlined whole",
			priming:  Priming{Name: "Keel", HomeDir: "/homes/keel", HandoffName: "2026-08-13T22-20Z-keel.md", Handoff: letterAtTheLimit},
			want:     []string{letterAtTheLimit},
			unwanted: []string{"Before responding to the user, read the whole file"},
		},
		{
			name:    "a hand-edited oversize letter is cut on a rune and asks for a full read",
			priming: Priming{Name: "Keel", HomeDir: "/homes/keel", HandoffName: "2026-08-13T22-20Z-keel.md", Handoff: strings.Repeat("日", handoffInlineLimit)},
			want:    []string{"hand-edited letter", "Before responding to the user, read the whole file", "2026-08-13T22-20Z-keel.md"},
		},
		{
			name:     "a held seed's note at the budget is carried whole",
			priming:  primingHolding(1, noteAtTheBudget),
			want:     []string{noteAtTheBudget},
			unwanted: []string{"Trimmed at"},
		},
		{
			name:    "an oversize held seed's note is cut on a rune and points at the whole note",
			priming: primingHolding(1, strings.Repeat("日", MaxClaimedHandoffBytes)),
			want:    []string{fmt.Sprintf("[Trimmed at %d bytes of %d; `attn seed notes s-held00` has the whole note.]", MaxClaimedHandoffBytes, MaxClaimedHandoffBytes*3)},
		},
		{
			name:     "a held list that fits says nothing about the tripwire",
			priming:  primingHolding(3, "Where it stands."),
			want:     []string{"`s-held02` held-seed-2 — Held seed 2"},
			unwanted: []string{"attn seed ls --flat"},
		},
		{
			name:     "a garden that could not be read is no section at all",
			priming:  Priming{Name: "Trellis", HomeDir: "/homes/trellis"},
			unwanted: []string{"## What you claim in the garden", "You claim no seeds in the garden"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block := tc.priming.Block()
			if !utf8.ValidString(block) {
				t.Fatal("the block is not valid UTF-8")
			}
			for _, want := range tc.want {
				if !strings.Contains(block, want) {
					t.Errorf("the block does not carry %.80q", want)
				}
			}
			for _, unwanted := range tc.unwanted {
				if strings.Contains(block, unwanted) {
					t.Errorf("the block carries %q", unwanted)
				}
			}
		})
	}
}
