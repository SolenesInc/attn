package garden

import (
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

func TestWindowInvariantsAcrossArbitraryGardens(t *testing.T) {
	statuses := []string{StatusPlanted, StatusGrowing, StatusParked, StatusHarvested, StatusWithered}
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 60).Draw(t, "n")
		limit := rapid.IntRange(0, 40).Draw(t, "limit")
		seeds := make([]Seed, n)
		for i := range n {
			seeds[i] = Seed{
				ID:     fmt.Sprintf("s-%03d", i),
				Status: rapid.SampledFrom(statuses).Draw(t, fmt.Sprintf("status-%d", i)),
			}
		}
		for i := range n {
			if i > 0 && rapid.Bool().Draw(t, fmt.Sprintf("has-parent-%d", i)) {
				parentIdx := rapid.IntRange(0, i-1).Draw(t, fmt.Sprintf("parent-%d", i))
				seeds[i].Edges = []Edge{{Kind: EdgePartOf, To: seeds[parentIdx].ID}}
			}
		}

		got := Window(seeds, limit)

		pos := make(map[string]int, n)
		byID := make(map[string]Seed, n)
		for i, s := range seeds {
			pos[s.ID] = i
			byID[s.ID] = s
		}
		gotSet := make(map[string]bool, len(got))
		prevPos := -1
		for _, s := range got {
			if gotSet[s.ID] {
				t.Fatalf("duplicate seed %s in Window output", s.ID)
			}
			gotSet[s.ID] = true
			p, ok := pos[s.ID]
			if !ok {
				t.Fatalf("unknown seed %s in Window output", s.ID)
			}
			if p <= prevPos {
				t.Fatalf("order inverted at %s (%d <= %d)", s.ID, p, prevPos)
			}
			prevPos = p
		}

		mustKeep := make(map[string]bool)
		for _, s := range seeds {
			if Closed(s.Status) {
				continue
			}
			for cur := s; !mustKeep[cur.ID]; {
				mustKeep[cur.ID] = true
				parentID, ok := ParentOf(cur)
				if !ok {
					break
				}
				parent, exists := byID[parentID]
				if !exists {
					break
				}
				cur = parent
			}
		}
		for id := range mustKeep {
			if !gotSet[id] {
				t.Fatalf("open seed or ancestor %s missing from Window output", id)
			}
		}

		wantLen := min(n, max(limit, len(mustKeep)))
		if len(got) != wantLen {
			t.Fatalf("len(Window) = %d, want %d (n=%d limit=%d mustKeep=%d)", len(got), wantLen, n, limit, len(mustKeep))
		}

		extraClosed := 0
		for _, s := range seeds {
			if mustKeep[s.ID] {
				continue
			}
			wantIncluded := len(mustKeep)+extraClosed < wantLen
			if gotSet[s.ID] != wantIncluded {
				t.Fatalf("closed seed %s included=%v, want %v", s.ID, gotSet[s.ID], wantIncluded)
			}
			if wantIncluded {
				extraClosed++
			}
		}
	})
}
