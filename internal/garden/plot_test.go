package garden

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParsePlotSpecReadsAWholePlot(t *testing.T) {
	spec, err := ParsePlotSpec([]byte(`{
		"title": "ship the thing",
		"body": "# the plan",
		"children": [
			{"title": "first step", "body": "do it"},
			{"title": "second step", "after": []},
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
		if len(child.After) != 0 {
			t.Fatalf("a child waiting for nothing came back sequenced: %+v", child)
		}
	}
}

func TestParsePlotSpecRefusesWhatCannotBePlanted(t *testing.T) {
	cases := map[string]struct {
		payload string
		wants   []string
	}{
		"a typo'd key would silently drop the sequencing": {
			`{"title":"t","children":[{"title":"a","aftr":["b"]}]}`,
			[]string{"not a plot payload", "after"},
		},
		"blocks, which reads in the opposite direction": {
			`{"title":"t","children":[{"title":"Draw grid","blocks":["Scroll grid"]},{"title":"Scroll grid"}]}`,
			[]string{"blocks", "after"},
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
		"after naming no sibling": {
			`{"title":"t","children":[{"title":"a","after":["nobody"]}]}`,
			[]string{"nobody", "no sibling's title or step slug", "a"},
		},
		"after that cycles": {
			`{"title":"t","children":[{"title":"a","after":["b"]},{"title":"b","after":["a"]}]}`,
			[]string{"wait for each other", "a", "b"},
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

func TestParsePlotSpecAcceptsATitleAsAnAfterTarget(t *testing.T) {
	spec, err := ParsePlotSpec([]byte(`{"title":"t","children":[{"title":"Draw the grid"},{"title":"Scroll the grid","after":["Draw the grid"]}]}`))
	if err != nil {
		t.Fatalf("a sibling's title is refused as an after target: %v", err)
	}
	if len(spec.Children) != 2 {
		t.Fatalf("children = %d", len(spec.Children))
	}
}

func TestValidatePlotSpecSeparatesAChainFromACycle(t *testing.T) {
	chain := PlotSpec{Title: "t", Children: []PlotChildSpec{
		{Title: "a"},
		{Title: "b", After: []string{"a"}},
		{Title: "c", After: []string{"b"}},
	}}
	if err := ValidatePlotSpec(chain); err != nil {
		t.Fatalf("a three-step chain was refused: %v", err)
	}
	cycle := chain
	cycle.Children = append([]PlotChildSpec{}, chain.Children...)
	cycle.Children[0] = PlotChildSpec{Title: "a", After: []string{"c"}}
	if err := ValidatePlotSpec(cycle); err == nil {
		t.Fatal("a three-step cycle was accepted")
	}
}

func TestPlotSpecRoundTripsThroughItsPayload(t *testing.T) {
	want := PlotSpec{Title: "t", Body: "b", Children: []PlotChildSpec{
		{Title: "a", Body: "ab", After: []string{"b"}},
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
	if got.Title != want.Title || len(got.Children) != 2 || got.Children[0].After[0] != "b" {
		t.Fatalf("round trip lost the plot: %+v", got)
	}
}
