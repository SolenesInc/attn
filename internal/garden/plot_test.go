package garden

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func readySet(seeds []Seed) map[string]bool {
	ready := map[string]bool{}
	for _, seed := range Ready(seeds, nil) {
		ready[seed.ID] = true
	}
	return ready
}

func child(id, crown, status string, blocks ...string) Seed {
	seed := Seed{ID: id, Title: id, Status: status, Edges: []Edge{{Kind: EdgePartOf, To: crown}}}
	for _, target := range blocks {
		seed.Edges = append(seed.Edges, Edge{Kind: EdgeBlocks, To: target})
	}
	return seed
}

func heldBy(seed Seed, member string) Seed {
	seed.TenderMember = member
	return seed
}

func TestPlotProgressCountsWhereTheChildrenStand(t *testing.T) {
	seeds := []Seed{
		{ID: "s-crown", Title: "crown", Status: StatusPlanted},
		child("s-done", "s-crown", StatusHarvested),
		child("s-gone", "s-crown", StatusWithered),
		heldBy(child("s-held", "s-crown", StatusGrowing), "trellis"),
		child("s-parked", "s-crown", StatusDormant),
		child("s-open", "s-crown", StatusPlanted),
		child("s-late", "s-crown", StatusPlanted),
		{ID: "s-elsewhere", Title: "elsewhere", Status: StatusPlanted},
	}
	seeds[5].Edges = append(seeds[5].Edges, Edge{Kind: EdgeBlocks, To: "s-late"})

	got := PlotProgress(seeds, "s-crown", readySet(seeds))
	want := Progress{Total: 6, Done: 1, Withered: 1, Growing: 1, Dormant: 1, Ready: 1, Blocked: 1}
	if got != want {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}
}

func TestPlotProgressDoesNotCountTheCrown(t *testing.T) {
	seeds := []Seed{{ID: "s-crown", Title: "crown", Status: StatusPlanted}}
	if got := PlotProgress(seeds, "s-crown", readySet(seeds)); got.Total != 0 {
		t.Fatalf("an empty plot counts %d, want 0: %+v", got.Total, got)
	}
}

func TestPlotProgressReachesTheWholeTree(t *testing.T) {
	seeds := []Seed{
		{ID: "s-crown", Title: "crown", Status: StatusPlanted},
		child("s-mid", "s-crown", StatusPlanted),
		child("s-leaf", "s-mid", StatusPlanted),
	}
	if got := PlotProgress(seeds, "s-crown", readySet(seeds)); got.Total != 2 {
		t.Fatalf("progress = %+v, want both descendants counted", got)
	}
}

func TestStaleNamesOnlyOpenSeedsPastTheWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		seeds := []Seed{
			{ID: "s-quiet", Status: StatusPlanted},
			{ID: "s-exactly", Status: StatusPlanted},
			{ID: "s-fresh", Status: StatusPlanted},
			{ID: "s-closed", Status: StatusHarvested},
			{ID: "s-unknown", Status: StatusPlanted},
		}
		moved := map[string]time.Time{
			"s-quiet":   now.Add(-30 * 24 * time.Hour),
			"s-exactly": now.Add(-DefaultStaleWindow),
			"s-fresh":   now.Add(-time.Hour),
			"s-closed":  now.Add(-30 * 24 * time.Hour),
		}

		got := Stale(seeds, moved, DefaultStaleWindow, now)
		if len(got) != 2 || got[0].ID != "s-quiet" || got[1].ID != "s-exactly" {
			t.Fatalf("stale = %+v, want the two quiet open seeds", got)
		}
	})
}

func TestStaleSkipsASeedItHasNoEvidenceFor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		seeds := []Seed{{ID: "s-unknown", Status: StatusPlanted}}
		if got := Stale(seeds, map[string]time.Time{}, time.Hour, time.Now()); len(got) != 0 {
			t.Fatalf("stale = %+v, want nothing: there is no log to judge", got)
		}
	})
}

func TestParsePlotSpecReadsAWholePlot(t *testing.T) {
	spec, err := ParsePlotSpec([]byte(`{
		"title": "ship the thing",
		"body": "# the plan",
		"children": [
			{"title": "first step", "body": "do it"},
			{"title": "second step", "blocks": []},
			{"title": "third step"}
		]
	}`))
	if err != nil {
		t.Fatalf("ParsePlotSpec: %v", err)
	}
	if spec.Title != "ship the thing" || len(spec.Children) != 3 {
		t.Fatalf("parsed = %+v", spec)
	}
	for _, child := range spec.Children {
		if len(child.Blocks) != 0 {
			t.Fatalf("a child with no blocks came back sequenced: %+v", child)
		}
	}
}

func TestParsePlotSpecRefusesWhatCannotBePlanted(t *testing.T) {
	cases := map[string]struct {
		payload string
		wants   []string
	}{
		"a typo'd key would silently drop the sequencing": {
			`{"title":"t","children":[{"title":"a","block":["b"]}]}`,
			[]string{"not a plot payload", "blocks"},
		},
		"no children is not a plot": {
			`{"title":"t","children":[]}`,
			[]string{"attn seed plant"},
		},
		"a blank crown title": {
			`{"title":"   ","children":[{"title":"a"}]}`,
			[]string{"crown"},
		},
		"two children deriving one slug": {
			`{"title":"t","children":[{"title":"Do the thing"},{"title":"do the THING"}]}`,
			[]string{"do-thing", "retitle"},
		},
		"blocks naming no sibling": {
			`{"title":"t","children":[{"title":"a","blocks":["nobody"]}]}`,
			[]string{"nobody", "no sibling's title or step slug", "a"},
		},
		"blocks that cycle": {
			`{"title":"t","children":[{"title":"a","blocks":["b"]},{"title":"b","blocks":["a"]}]}`,
			[]string{"cycle", "a", "b"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParsePlotSpec([]byte(tc.payload))
			if err == nil {
				t.Fatal("planted anyway")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

func TestParsePlotSpecAcceptsATitleAsABlocksTarget(t *testing.T) {
	spec, err := ParsePlotSpec([]byte(`{"title":"t","children":[{"title":"Draw the grid"},{"title":"Scroll the grid","blocks":["Draw the grid"]}]}`))
	if err != nil {
		t.Fatalf("a sibling's title is refused as a blocks target: %v", err)
	}
	if len(spec.Children) != 2 {
		t.Fatalf("children = %d", len(spec.Children))
	}
}

func TestValidatePlotSpecSeparatesAChainFromACycle(t *testing.T) {
	chain := PlotSpec{Title: "t", Children: []PlotChildSpec{
		{Title: "a", Blocks: []string{"b"}},
		{Title: "b", Blocks: []string{"c"}},
		{Title: "c"},
	}}
	if err := ValidatePlotSpec(chain); err != nil {
		t.Fatalf("a three-step chain was refused: %v", err)
	}
	cycle := chain
	cycle.Children = append([]PlotChildSpec{}, chain.Children...)
	cycle.Children[2] = PlotChildSpec{Title: "c", Blocks: []string{"a"}}
	if err := ValidatePlotSpec(cycle); err == nil {
		t.Fatal("a three-step cycle was accepted")
	}
}

func TestPlotSpecRoundTripsThroughItsPayload(t *testing.T) {
	want := PlotSpec{Title: "t", Body: "b", Children: []PlotChildSpec{
		{Title: "a", Body: "ab", Blocks: []string{"b"}},
		{Title: "b"},
	}}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := ParsePlotSpec(raw)
	if err != nil {
		t.Fatalf("ParsePlotSpec: %v", err)
	}
	if got.Title != want.Title || len(got.Children) != 2 || got.Children[0].Blocks[0] != "b" {
		t.Fatalf("round trip lost the plot: %+v", got)
	}
}
