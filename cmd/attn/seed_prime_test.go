package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

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
