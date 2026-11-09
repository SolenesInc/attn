package garden

import (
	"testing"
)

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
