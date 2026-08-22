package main

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// Copy receipt for the text between BEGIN X and END X in
// /Users/victor/.attn/crew/trellis/working/2026-08-21-eval-r1/trellis-sections.md.
// Normalization converts CRLF to LF and removes only surrounding LF bytes; all
// prose, spacing, indentation, and internal blank lines remain byte-exact.
func TestSeedPrimeStandingCopyMatchesFinalMarkedBlock(t *testing.T) {
	const want = "8b96f6be9f5591e81a6bdfb8bea32e6a80df55918a2839bbde8ebde21ec1918e"
	if got := normalizedCopyHash(seedPrimeText); got != want {
		t.Fatalf("normalized BEGIN X copy SHA-256 = %s, want %s", got, want)
	}
}

// Copy receipts for the four marked BEGIN X tail blocks beside BEGIN X in the
// same source file. Each fixture uses the sample ids, titles, count, and handoff
// from its block; only the standing BEGIN X prefix is removed before hashing.
func TestSeedPrimeTailsMatchFinalMarkedBlocks(t *testing.T) {
	plotID := "s-" + "cro" + "wn1"
	plotProgress := &protocol.SeedPlotProgress{}
	tests := []struct {
		name  string
		ready protocol.SeedReadyResult
		hash  string
	}{
		{
			name:  "garden",
			ready: protocol.SeedReadyResult{Seeds: make([]protocol.Seed, 17)},
			hash:  "40d8a43d42d262a3493f2758fe9ddf27391a52e37a4609643871785dc54b409c",
		},
		{
			name: "plot",
			ready: protocol.SeedReadyResult{
				Crown:    &protocol.Seed{ID: plotID, Title: "Example plot", PlotProgress: plotProgress},
				Seeds:    []protocol.Seed{{ID: "s-child1", Title: "First child"}},
				Handoffs: []protocol.SeedNote{{SeedID: "s-child1", Body: "left it half done", AuthorMember: "alder"}},
			},
			hash: "19a05b1b4d8ef5ca602bf8f94b4116eaff0d37478f0432e924783ad95cf53391",
		},
		{
			name:  "seed",
			ready: protocol.SeedReadyResult{Crown: &protocol.Seed{ID: "s-leaf1", Title: "Example seed"}},
			hash:  "5efcce5bc014dbd6a7c00e0f2c13eb8e054c5c1e4484e6a4222ea45191598674",
		},
		{
			name:  "empty plot",
			ready: protocol.SeedReadyResult{Crown: &protocol.Seed{ID: plotID, Title: "Example plot", PlotProgress: plotProgress}},
			hash:  "095f5f44aa43e30b2bb11e7319b4953a0c2dff74259be0daaf4182b8c0d4381a",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prime := seedPrimeFromReady(&tc.ready)
			tail, ok := strings.CutPrefix(prime, seedPrimeText+"\n\n")
			if !ok {
				t.Fatal("prime does not begin with the standing BEGIN X copy")
			}
			if got := normalizedCopyHash(tail); got != tc.hash {
				t.Fatalf("normalized marked tail SHA-256 = %s, want %s", got, tc.hash)
			}
		})
	}
}

func TestSeedPrimeFromReadyTails(t *testing.T) {
	plotProgress := &protocol.SeedPlotProgress{}
	tests := []struct {
		name  string
		ready protocol.SeedReadyResult
		tail  string
	}{
		{
			name:  "garden",
			ready: protocol.SeedReadyResult{Seeds: []protocol.Seed{{ID: "s-one111"}, {ID: "s-two222"}}},
			tail:  "2 seeds are ready now.",
		},
		{
			name:  "seed",
			ready: protocol.SeedReadyResult{Crown: &protocol.Seed{ID: "s-leaf1", Title: "Example seed"}},
			tail:  "You were dispatched to work on seed `s-leaf1`, \"Example seed\". Read it with `attn seed show s-leaf1`.",
		},
		{
			name: "plot",
			ready: protocol.SeedReadyResult{
				Crown:    &protocol.Seed{ID: "s-plot01", Title: "Example plot", PlotProgress: plotProgress},
				Seeds:    []protocol.Seed{{ID: "s-child1", Title: "First child"}},
				Handoffs: []protocol.SeedNote{{SeedID: "s-child1", Body: "left it half done", AuthorMember: "alder"}},
			},
			tail: "You were dispatched to work at plot `s-plot01`, \"Example plot\". Read the plan with `attn seed show s-plot01`. Tend or plant anything, here or elsewhere.\n\nReady in this plot now, oldest first:\n\n- `s-child1` First child\n  handoff from Alder: left it half done",
		},
		{
			name:  "empty plot",
			ready: protocol.SeedReadyResult{Crown: &protocol.Seed{ID: "s-plot01", Title: "Example plot", PlotProgress: plotProgress}},
			tail:  "Nothing in this plot is ready now; its seeds are blocked, held, or done. `attn seed show s-plot01` shows what blocks what.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := seedPrimeText + "\n\n" + tc.tail + "\n"
			if got := seedPrimeFromReady(&tc.ready); got != want {
				t.Fatalf("prime differs:\n%s", firstDifference(got, want))
			}
		})
	}
}

func TestSeedPrimeGardenCountGrammar(t *testing.T) {
	for count, want := range map[int]string{0: "Nothing is ready now.", 1: "One seed is ready now."} {
		ready := &protocol.SeedReadyResult{Seeds: make([]protocol.Seed, count)}
		if got := seedPrimeFromReady(ready); !strings.HasSuffix(got, want+"\n") {
			t.Fatalf("count %d prime = %q", count, got)
		}
	}
}

func firstDifference(got, want string) string {
	limit := len(got)
	if len(want) < limit {
		limit = len(want)
	}
	for i := 0; i < limit; i++ {
		if got[i] != want[i] {
			return fmt.Sprintf("first byte differs at %d", i)
		}
	}
	return "length differs"
}

func normalizedCopyHash(copy string) string {
	copy = strings.ReplaceAll(copy, "\r\n", "\n")
	copy = strings.Trim(copy, "\n")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(copy)))
}
