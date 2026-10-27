package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
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

	if _, err := cli.SeedNote("", newer, "a note changes no seed", "", false, nil); err != nil {
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

func TestGardenPushCarriesASeedWrittenTogetherWithANote(t *testing.T) {
	github := newHarvestGitHub(t)
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	shipper := spawnPanes(w, app, w.Path("shipper"))[0].session
	url := github.open(72, "Ship the push")
	seed := plantSeedAs(t, cli, shipper, "ship the push")
	lifeMove(t, cli, shipper, seed, "tend", "", "")

	harvestArmAs(t, cli, shipper, "", seed, url)
	changed := testworld.Await(app, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
		return len(m.Seeds) == 1 && m.Seeds[0].ID == seed && m.Seeds[0].Status == "dormant"
	})
	if changed.Seeds[0].HarvestWhen == nil {
		t.Fatalf("the change after arming carries %+v, want the armed seed waiting on its pull request", changed.Seeds[0])
	}
	if notes := lifeNoteBodies(t, cli, seed); !slices.Contains(notes, "harvests when acme/shop#72 merges") {
		t.Errorf("the log = %q, want the arming note committed with the seed", notes)
	}
}

func TestGardenPushMarksASeedReadyWhenItsTenderSessionCloses(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	holder := spawnPanes(w, app, w.Path("holder"))[0]
	seed := plantSeedAs(t, cli, holder.session, "held by a live session")
	lifeMove(t, cli, holder.session, seed, "tend", "", "")
	watcher := w.App()
	if held := watcher.Initial.Seeds[0]; held.ID != seed || held.Ready {
		t.Fatalf("initial state carries %s ready=%v, want %s not ready while its tender lives", held.ID, held.Ready, seed)
	}

	closePane(app, holder)
	changed := testworld.Await(watcher, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool { return len(m.Seeds) > 0 })
	if len(changed.Seeds) != 1 || changed.Seeds[0].ID != seed || !changed.Seeds[0].Ready {
		t.Fatalf("the change after the tender session closed carries %+v, want the seed, ready", changed.Seeds)
	}
}

func nextGardenChange(app *testworld.Peer) protocol.GardenSeedsChangedMessage {
	app.T.Helper()
	return testworld.Await[protocol.GardenSeedsChangedMessage](app, protocol.EventGardenSeedsChanged, nil)
}

func TestGardenPushAfterAProfileSwitchSendsOnlyWhatChangedInTheNewProfile(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	original := app.SelectedProfile()
	side := createProfile(app, "Side")
	home := cli.WithRequester(original, "")
	away := cli.WithRequester(side.ID, "")
	homeSeed := plantSeedAs(t, home, "", "home seed")
	awayFirst := plantSeedAs(t, away, "", "away first")
	awaySecond := plantSeedAs(t, away, "", "away second")

	for _, step := range []struct {
		profile string
		seeds   *client.Client
		moved   string
		window  []string
	}{
		{side.ID, away, awayFirst, []string{awaySecond, awayFirst}},
		{original, home, homeSeed, []string{homeSeed}},
	} {
		selectProfile(app, step.profile)
		testworld.Await(app, protocol.EventGardenSeedsUpdated, func(m protocol.GardenSeedsUpdatedMessage) bool { return m.ProfileID == step.profile })
		lifeMove(t, step.seeds, "", step.moved, "wither", "", "")
		changed := testworld.Await(app, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool { return m.ProfileID == step.profile })
		if !slices.Equal(changed.Order, step.window) {
			t.Errorf("after switching to %s, the change orders %v, want %v", step.profile, changed.Order, step.window)
		}
		if got := lifeSeedIDs(changed.Seeds); !slices.Equal(got, []string{step.moved}) {
			t.Errorf("after switching to %s, the change carries %v, want only %s", step.profile, got, step.moved)
		}
	}
}
