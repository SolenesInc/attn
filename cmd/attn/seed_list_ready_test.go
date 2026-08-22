package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSeedRowsNestByDefaultAndFlattenOnRequest(t *testing.T) {
	plot := protocol.Seed{ID: "s-plot11", Title: "the plot"}
	child := protocol.Seed{
		ID: "s-child1", Title: "the child",
		Edges: []protocol.SeedEdge{{Kind: "part-of", To: plot.ID}},
	}
	loose := protocol.Seed{ID: "s-loose1", Title: "loose"}
	seeds := []protocol.Seed{loose, child, plot}

	nested := seedRows(seeds, false)
	if got := []string{nested[0].seed.ID, nested[1].seed.ID, nested[2].seed.ID}; !sameStrings(got, []string{loose.ID, plot.ID, child.ID}) {
		t.Fatalf("default rows = %v", got)
	}
	if nested[2].depth != 1 {
		t.Fatalf("child depth = %d, want 1", nested[2].depth)
	}
	flat := seedRows(seeds, true)
	if got := []string{flat[0].seed.ID, flat[1].seed.ID, flat[2].seed.ID}; !sameStrings(got, []string{loose.ID, child.ID, plot.ID}) {
		t.Fatalf("flat rows = %v", got)
	}
}

func TestReadyPrintsPlotHierarchyBeforeLooseSeeds(t *testing.T) {
	stamp := "2026-08-22T12:00:00Z"
	firstPlot := protocol.Seed{ID: "s-plot11", Title: "first plot", CreatedAt: stamp}
	secondPlot := protocol.Seed{ID: "s-plot22", Title: "second plot", CreatedAt: stamp}
	firstChild := protocol.Seed{
		ID: "s-child1", Title: "first child", Status: "planted", CreatedAt: stamp,
		Edges: []protocol.SeedEdge{{Kind: "part-of", To: firstPlot.ID}},
	}
	secondChild := protocol.Seed{
		ID: "s-child2", Title: "second child", Status: "planted", CreatedAt: stamp,
		Edges: []protocol.SeedEdge{{Kind: "part-of", To: secondPlot.ID}},
	}
	loose := protocol.Seed{ID: "s-loose1", Title: "loose", Status: "planted", CreatedAt: stamp}
	var out bytes.Buffer
	fprintSeedReady(&out, &protocol.SeedReadyResult{
		Scope: "garden", Plots: []protocol.Seed{firstPlot, secondPlot},
		Seeds: []protocol.Seed{firstChild, secondChild, loose},
	})

	text := out.String()
	order := []string{firstPlot.ID, firstChild.ID, secondPlot.ID, secondChild.ID, loose.ID}
	last := -1
	for _, id := range order {
		at := strings.Index(text, id)
		if at <= last {
			t.Fatalf("%s is not after the previous row:\n%s", id, text)
		}
		last = at
	}
	if !strings.Contains(text, "s-plot11  plot") || !strings.Contains(text, "3 ready") {
		t.Fatalf("ready output does not distinguish headers from the three pickups:\n%s", text)
	}
}

func TestSeedHelpNamesFlatAndPlot(t *testing.T) {
	var out bytes.Buffer
	writeSeedHelp(&out)
	text := out.String()
	for _, want := range []string{"ls [--stale [--window <duration>]] [--flat]", "--plot <plot>", "--discovered-from <seed>", "--force"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help does not contain %q", want)
		}
	}
	for _, stale := range []string{"--confirm"} {
		if strings.Contains(text, stale) {
			t.Fatalf("help still contains %q", stale)
		}
	}
}

func TestSeedPlantParsesDiscoveredFromAfterTheTitle(t *testing.T) {
	f := newSeedFlags("plant")
	positionals := f.parse("plant", []string{"follow-up", "--discovered-from", "s-origin1"})
	if !sameStrings(positionals, []string{"follow-up"}) {
		t.Fatalf("plant positionals = %v", positionals)
	}
	if got := *f.discoveredFrom; got != "s-origin1" {
		t.Fatalf("--discovered-from = %q, want s-origin1", got)
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
