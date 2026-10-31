package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func TestASlugNamesASeedWhereverAnIDDoes(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	caller, _, _ := w.RequestSpawn(app, shellHarness, w.Path("caller"))
	defer exitShells(app, string(caller.ID))
	other := createProfile(app, "Private")
	otherApp := w.AppOn(other.ID)
	stranger, _, _ := w.RequestSpawn(otherApp, shellHarness, w.Path("stranger"))
	defer exitShells(otherApp, string(stranger.ID))
	me := caller.ID
	cli := w.Client().WithGardenProfile("", me)

	trellis := plantSeedAs(t, cli, string(me), "Grow the trellis")
	vine := plantSeedAs(t, cli, string(me), "Water the vine")
	plantSeedAs(t, w.Client(), string(stranger.ID), "Water the vine")

	if shown, err := cli.SeedShow(me, "water-vine"); err != nil || shown.Seed.ID != vine {
		t.Fatalf("show water-vine = %+v, %v; want %s, not the other profile's seed of that slug", shown, err, vine)
	}
	if _, err := cli.SeedNote(me, "water-vine", "checked the soil", "", false, nil); err != nil {
		t.Fatalf("note water-vine: %v", err)
	}
	if notes, err := cli.SeedNotes(me, "water-vine", client.SeedNotesQuery{}); err != nil || !slices.ContainsFunc(notes.Notes, func(n protocol.SeedNote) bool { return n.Body == "checked the soil" }) {
		t.Fatalf("notes water-vine = %+v, %v; want the note written by slug", notes, err)
	}
	if moved, err := cli.SeedTransition(me, "water-vine", "tend", "", false, client.SeedTransitionOptions{}); err != nil || moved.Seed.ID != vine || moved.Seed.Status != garden.StatusGrowing {
		t.Fatalf("tend water-vine = %+v, %v; want %s growing", moved, err, vine)
	}
	if linked, err := cli.SeedLink("water-vine", garden.EdgePartOf, "grow-trellis", false); err != nil || linked.Seed.ID != vine || !slices.Contains(linked.Seed.Edges, protocol.SeedEdge{Kind: garden.EdgePartOf, To: trellis}) {
		t.Fatalf("link water-vine part-of grow-trellis = %+v, %v; want the edge between the ids", linked, err)
	}
	if ready, err := cli.SeedReady(me, "grow-trellis", false); err != nil || ready.Crown == nil || ready.Crown.ID != trellis {
		t.Fatalf("ready --plot grow-trellis = %+v, %v; want the plot %s", ready, err, trellis)
	}

	planted, err := cli.SeedPlot(me, protocol.SeedPlotMessage{
		Title: "Prune the hedge", PartOf: protocol.Ptr("grow-trellis"), DiscoveredFrom: protocol.Ptr("water-vine"),
		Children: []protocol.SeedPlotChild{{Title: "Sharpen shears"}, {Title: "Cut back", After: []string{"sharpen-shears"}}},
	})
	if err != nil {
		t.Fatalf("plot under grow-trellis, discovered from water-vine: %v", err)
	}
	wantEdges := []protocol.SeedEdge{{Kind: garden.EdgePartOf, To: trellis}, {Kind: garden.EdgeDiscoveredFrom, To: vine}}
	if !slices.Equal(planted.Crown.Edges, wantEdges) {
		t.Errorf("the plot's crown has edges %+v, want %+v", planted.Crown.Edges, wantEdges)
	}

	twin := plantSeedAs(t, cli, string(me), "Water the vine")
	if _, err := cli.SeedTransition(me, vine, "harvest", "watered", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	for name, refuse := range map[string]func() error{
		"show": func() error { _, err := cli.SeedShow(me, "water-vine"); return err },
		"link": func() error {
			_, err := cli.SeedLink("grow-trellis", garden.EdgeBlocks, "water-vine", false)
			return err
		},
	} {
		err := refuse()
		if err == nil {
			t.Fatalf("%s of a slug two seeds share went ahead", name)
		}
		for _, want := range []string{"water-vine names 2 seeds", vine + "  harvested  Water the vine", twin + "  planted  Water the vine"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s refusal %q does not list %q", name, err, want)
			}
		}
	}
	if _, err := cli.SeedShow(me, "no-such-seed"); err == nil || !strings.Contains(err.Error(), `"no-such-seed" is neither a seed id nor the slug of a seed`) {
		t.Errorf("show of an unknown slug = %v, want a refusal naming it", err)
	}
}
