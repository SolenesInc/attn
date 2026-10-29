package garden

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParsePlotSpecReadsAWholePlot(t *testing.T) {
	spec, err := ParsePlotSpec([]byte(`{"body":"# the plan","children":[{"body":"do it","title":"first step"},{"blocks":[],"body":"Complete the assigned work.","title":"second step"},{"body":"Complete the assigned work.","title":"third step"}],"title":"ship the thing"}`))
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
			`{"body":"Complete the assigned work.","children":[{"block":["b"],"body":"Complete the assigned work.","title":"a"}],"title":"t"}`,
			[]string{"not a plot payload", "blocks"},
		},
		"no children is not a plot": {
			`{"body":"Complete the assigned work.","children":[],"title":"t"}`,
			[]string{"attn seed plant"},
		},
		"a blank crown title": {
			`{"body":"Complete the assigned work.","children":[{"body":"Complete the assigned work.","title":"a"}],"title":"   "}`,
			[]string{"crown"},
		},
		"two children deriving one slug": {
			`{"body":"Complete the assigned work.","children":[{"body":"Complete the assigned work.","title":"Do the thing"},{"body":"Complete the assigned work.","title":"do the THING"}],"title":"t"}`,
			[]string{"do-thing", "retitle"},
		},
		"blocks naming no sibling": {
			`{"body":"Complete the assigned work.","children":[{"blocks":["nobody"],"body":"Complete the assigned work.","title":"a"}],"title":"t"}`,
			[]string{"nobody", "no sibling's title or step slug", "a"},
		},
		"blocks that cycle": {
			`{"body":"Complete the assigned work.","children":[{"blocks":["b"],"body":"Complete the assigned work.","title":"a"},{"blocks":["a"],"body":"Complete the assigned work.","title":"b"}],"title":"t"}`,
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
	spec, err := ParsePlotSpec([]byte(`{"body":"Complete the assigned work.","children":[{"body":"Complete the assigned work.","title":"Draw the grid"},{"blocks":["Draw the grid"],"body":"Complete the assigned work.","title":"Scroll the grid"}],"title":"t"}`))
	if err != nil {
		t.Fatalf("a sibling's title is refused as a blocks target: %v", err)
	}
	if len(spec.Children) != 2 {
		t.Fatalf("children = %d", len(spec.Children))
	}
}

func TestValidatePlotSpecSeparatesAChainFromACycle(t *testing.T) {
	chain := PlotSpec{Body: "Complete the assigned work.", Title: "t", Children: []PlotChildSpec{
		{Body: "Complete the assigned work.", Title: "a", Blocks: []string{"b"}},
		{Body: "Complete the assigned work.", Title: "b", Blocks: []string{"c"}},
		{Body: "Complete the assigned work.", Title: "c"},
	}}
	if err := ValidatePlotSpec(chain); err != nil {
		t.Fatalf("a three-step chain was refused: %v", err)
	}
	cycle := chain
	cycle.Children = append([]PlotChildSpec{}, chain.Children...)
	cycle.Children[2] = PlotChildSpec{Body: "Complete the assigned work.", Title: "c", Blocks: []string{"a"}}
	if err := ValidatePlotSpec(cycle); err == nil {
		t.Fatal("a three-step cycle was accepted")
	}
}

func TestPlotSpecRoundTripsThroughItsPayload(t *testing.T) {
	want := PlotSpec{Title: "t", Body: "b", Children: []PlotChildSpec{
		{Title: "a", Body: "ab", Blocks: []string{"b"}},
		{Body: "Complete the assigned work.", Title: "b"},
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
