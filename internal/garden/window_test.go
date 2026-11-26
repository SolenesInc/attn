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

func TestLiveWindowInvariantsAcrossArbitraryGardens(t *testing.T) {
	statuses := []string{StatusPlanted, StatusGrowing, StatusParked, StatusHarvested, StatusWithered}
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 60).Draw(t, "n")
		seeds := make([]Seed, n)
		pinned := make(map[string]bool)
		for i := range n {
			id := fmt.Sprintf("s-%03d", i)
			seeds[i] = Seed{
				ID:     id,
				Status: rapid.SampledFrom(statuses).Draw(t, fmt.Sprintf("status-%d", i)),
			}
			if rapid.IntRange(0, 9).Draw(t, fmt.Sprintf("pin-%d", i)) == 0 {
				pinned[id] = true
			}
		}
		for i := range n {
			if i > 0 && rapid.Bool().Draw(t, fmt.Sprintf("has-parent-%d", i)) {
				parentIdx := rapid.IntRange(0, i-1).Draw(t, fmt.Sprintf("parent-%d", i))
				seeds[i].Edges = []Edge{{Kind: EdgePartOf, To: seeds[parentIdx].ID}}
			}
		}

		plots := NewPlotTree(seeds)
		got := LiveWindow(seeds, plots, pinned)

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
				t.Fatalf("duplicate seed %s in LiveWindow output", s.ID)
			}
			gotSet[s.ID] = true
			p, ok := pos[s.ID]
			if !ok {
				t.Fatalf("unknown seed %s in LiveWindow output", s.ID)
			}
			if p <= prevPos {
				t.Fatalf("order inverted at %s (%d <= %d)", s.ID, p, prevPos)
			}
			prevPos = p
		}

		wantKeep := make(map[string]bool)
		for _, s := range seeds {
			if Closed(s.Status) && !pinned[s.ID] {
				continue
			}
			for cur := s; !wantKeep[cur.ID]; {
				wantKeep[cur.ID] = true
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
		if len(got) != len(wantKeep) {
			t.Fatalf("len(LiveWindow) = %d, want %d", len(got), len(wantKeep))
		}
		for _, s := range seeds {
			if gotSet[s.ID] != wantKeep[s.ID] {
				t.Fatalf("seed %s (status=%s pinned=%v) included=%v, want %v", s.ID, s.Status, pinned[s.ID], gotSet[s.ID], wantKeep[s.ID])
			}
		}
	})
}

func TestLiveWindowHasNoCapOnOpenSeeds(t *testing.T) {
	const openCount = 1205
	const closedCount = 250
	seeds := make([]Seed, 0, openCount+closedCount)
	for i := range openCount {
		seeds = append(seeds, Seed{ID: fmt.Sprintf("s-o%04d", i), Status: StatusPlanted})
	}
	for i := range closedCount {
		seeds = append(seeds, Seed{ID: fmt.Sprintf("s-c%04d", i), Status: StatusHarvested})
	}
	plots := NewPlotTree(seeds)
	got := LiveWindow(seeds, plots, map[string]bool{"s-c0007": true})
	if len(got) != openCount+1 {
		t.Fatalf("len(LiveWindow) = %d, want %d (all open + 1 pinned)", len(got), openCount+1)
	}
	if got[len(got)-1].ID != "s-c0007" {
		t.Fatalf("pinned seed = %s, want s-c0007", got[len(got)-1].ID)
	}
}
