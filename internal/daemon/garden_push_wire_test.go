package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestGardenPushSendsOnlyWhatChanged(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	older := plantSeedAs(t, cli, "", "older")
	newer := plantSeedAs(t, cli, "", "newer")

	app := w.App()
	if got := lifeSeedIDs(app.Initial.Seeds); !slices.Equal(got, []string{newer, older}) {
		t.Fatalf("initial state carries %v, want the whole garden newest first", got)
	}

	lifeMove(t, cli, "", older, "wither", "", "")
	changed := nextGardenChange(app)
	if !slices.Equal(changed.Order, []string{newer, older}) {
		t.Errorf("the change after a wither orders %v, want the whole window newest first", changed.Order)
	}
	if len(changed.Seeds) != 1 || changed.Seeds[0].ID != older || changed.Seeds[0].Status != "withered" {
		t.Fatalf("the change after a wither carries %+v, want only the withered seed", changed.Seeds)
	}

	if _, err := cli.SeedNote("", newer, "a note changes no seed", "", "", false, nil); err != nil {
		t.Fatal(err)
	}
	if changed = nextGardenChange(app); len(changed.Seeds) != 0 || !slices.Equal(changed.Order, []string{newer, older}) {
		t.Fatalf("the change after a note carries %v in %v, want no seed in the same window", lifeSeedIDs(changed.Seeds), changed.Order)
	}

	newest := plantSeedAs(t, cli, "", "newest")
	changed = nextGardenChange(app)
	if !slices.Equal(changed.Order, []string{newest, newer, older}) || changed.Total != 3 {
		t.Errorf("the change after planting orders %v of %d, want the new seed first of 3", changed.Order, changed.Total)
	}
	if got := lifeSeedIDs(changed.Seeds); !slices.Equal(got, []string{newest}) {
		t.Errorf("the change after planting carries %v, want only the new seed", got)
	}

	late := w.App()
	if got := lifeSeedIDs(late.Initial.Seeds); !slices.Equal(got, []string{newest, newer, older}) {
		t.Errorf("a client connecting later starts from %v, want the whole current garden", got)
	}
}

func TestGardenPushCarriesWhatAHarvestChangesElsewhere(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	blocker := plantSeedAs(t, cli, "", "blocker")
	dependent := plantSeedAs(t, cli, "", "dependent")
	bystander := plantSeedAs(t, cli, "", "bystander")
	plot := plantSeedAs(t, cli, "", "plot")
	edgeLink(t, cli, blocker, "blocks", dependent)
	edgeLink(t, cli, blocker, "part-of", plot)
	edgeLink(t, cli, dependent, "part-of", plot)
	app := w.App()

	lifeMove(t, cli, "", blocker, "harvest", "done", "")
	changed := nextGardenChange(app)
	seeds := map[string]protocol.Seed{}
	for _, seed := range changed.Seeds {
		seeds[seed.ID] = seed
	}
	if _, ok := seeds[bystander]; ok || len(seeds) != 3 {
		t.Errorf("the change after the harvest carries %v, want the blocker, the dependent and the plot", lifeSeedIDs(changed.Seeds))
	}
	if seeds[blocker].Status != "harvested" {
		t.Errorf("the blocker arrives %q, want harvested", seeds[blocker].Status)
	}
	if !seeds[dependent].Ready {
		t.Error("the dependent the harvest unblocked arrives not ready")
	}
	if progress := seeds[plot].PlotProgress; progress == nil || progress.Done != 1 || progress.Ready != 1 {
		t.Errorf("the plot arrives with progress %+v, want one done and one ready", progress)
	}
}

func nextGardenChange(app *testworld.Peer) protocol.GardenSeedsChangedMessage {
	app.T.Helper()
	return testworld.Await[protocol.GardenSeedsChangedMessage](app, protocol.EventGardenSeedsChanged, nil)
}
