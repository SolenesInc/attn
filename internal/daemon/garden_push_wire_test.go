package daemon_test

import (
	"github.com/victorarias/attn/internal/garden"
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

	lifeMove(t, cli, "", older, "park", "later", "")
	changed := nextGardenChange(app)
	if !slices.Equal(changed.Order, []string{newer, older}) {
		t.Errorf("the change after a park orders %v, want the whole live window newest first", changed.Order)
	}
	if len(changed.Seeds) != 1 || changed.Seeds[0].ID != older || changed.Seeds[0].Status != "parked" {
		t.Fatalf("the change after a park carries %+v, want only the parked seed", changed.Seeds)
	}

	if _, err := cli.SeedNote("", newer, "a note changes no seed", "", false, nil); err != nil {
		t.Fatal(err)
	}
	if changed = nextGardenChange(app); len(changed.Seeds) != 0 || !slices.Equal(changed.Order, []string{newer, older}) {
		t.Fatalf("the change after a note carries %v in %v, want no seed in the same window", lifeSeedIDs(changed.Seeds), changed.Order)
	}

	lifeMove(t, cli, "", older, "wither", "", "")
	changed = nextGardenChange(app)
	if !slices.Equal(changed.Order, []string{newer}) || changed.Total != 2 {
		t.Errorf("the change after a wither orders %v of %d, want only the open seed of 2", changed.Order, changed.Total)
	}
	if len(changed.Seeds) != 0 {
		t.Errorf("the change after a wither carries %v, want no live seed changed", lifeSeedIDs(changed.Seeds))
	}
	if changed.ClosedCounts == nil || changed.ClosedCounts.All.Withered != 1 {
		t.Errorf("closed_counts after wither = %+v, want withered=1", changed.ClosedCounts)
	}

	newest := plantSeedAs(t, cli, "", "newest")
	changed = nextGardenChange(app)
	if !slices.Equal(changed.Order, []string{newest, newer}) || changed.Total != 3 {
		t.Errorf("the change after planting orders %v of %d, want the two open seeds of 3", changed.Order, changed.Total)
	}
	if got := lifeSeedIDs(changed.Seeds); !slices.Equal(got, []string{newest}) {
		t.Errorf("the change after planting carries %v, want only the new seed", got)
	}

	late := w.App()
	if got := lifeSeedIDs(late.Initial.Seeds); !slices.Equal(got, []string{newest, newer}) {
		t.Errorf("a client connecting later starts from %v, want only open seeds", got)
	}
	if late.Initial.GardenClosedCounts == nil || late.Initial.GardenClosedCounts.All.Withered != 1 {
		t.Errorf("late client closed_counts = %+v, want withered=1", late.Initial.GardenClosedCounts)
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
	if slices.Contains(changed.Order, blocker) {
		t.Errorf("harvested unpinned blocker %s stayed in live order %v", blocker, changed.Order)
	}
	seeds := map[string]protocol.Seed{}
	for _, seed := range changed.Seeds {
		seeds[seed.ID] = seed
	}
	if _, ok := seeds[bystander]; ok || len(seeds) != 2 {
		t.Errorf("the change after the harvest carries %v, want the dependent and the plot", lifeSeedIDs(changed.Seeds))
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

	harvestArm(t, cli, shipper, seed, url)
	changed := testworld.Await(app, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
		return len(m.Seeds) == 1 && m.Seeds[0].ID == seed && m.Seeds[0].Status == "parked"
	})
	if changed.Seeds[0].HarvestWhen == nil {
		t.Fatalf("the change after arming carries %+v, want the armed seed waiting on its pull request", changed.Seeds[0])
	}
	if notes := lifeNoteBodies(t, cli, seed); !slices.Contains(notes, "parked; harvests when acme/shop#72 merges") {
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
	home := cli.WithGardenProfile(original, "")
	away := cli.WithGardenProfile(side.ID, "")
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
		lifeMove(t, step.seeds, "", step.moved, "park", "hold", "")
		changed := testworld.Await(app, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool { return m.ProfileID == step.profile })
		if !slices.Equal(changed.Order, step.window) {
			t.Errorf("after switching to %s, the change orders %v, want %v", step.profile, changed.Order, step.window)
		}
		if got := lifeSeedIDs(changed.Seeds); !slices.Equal(got, []string{step.moved}) {
			t.Errorf("after switching to %s, the change carries %v, want only %s", step.profile, got, step.moved)
		}
	}
}

func TestGardenPushPinsLiveSessionDispatchCrownAfterHarvestUntilSessionCloses(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	crown := plantDelegationSeed(t, cli, "caller", "Worker crown")
	unpinned := plantSeedAs(t, cli, "", "Unpinned task")
	delegated, err := cli.Delegate(delegateAtSeed("caller", cwd, crown))
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(string(delegated.SessionID))

	beforeUnpinned := len(app.Log())
	lifeMove(t, cli, "", unpinned, "harvest", "done", "")
	testworld.AwaitSince(app, beforeUnpinned, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
		return !slices.Contains(m.Order, unpinned) && slices.Contains(m.Order, crown)
	})

	beforeCrownHarvest := len(app.Log())
	lifeMove(t, cli, string(delegated.SessionID), crown, "harvest", "shipped", "")
	harvested := testworld.AwaitSince(app, beforeCrownHarvest, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
		return slices.ContainsFunc(m.Seeds, func(s protocol.Seed) bool {
			return s.ID == crown && s.Status == "harvested"
		})
	})
	if !slices.Contains(harvested.Order, crown) {
		t.Fatalf("harvested live dispatch crown %s dropped from order %v", crown, harvested.Order)
	}
	if fresh := w.App(); !slices.Contains(lifeSeedIDs(fresh.Initial.Seeds), crown) || slices.Contains(lifeSeedIDs(fresh.Initial.Seeds), unpinned) {
		t.Fatalf("fresh client initial_state = %v, want %s pinned and %s omitted", lifeSeedIDs(fresh.Initial.Seeds), crown, unpinned)
	}

	beforeClose := len(app.Log())
	if err := cli.Unregister(delegated.SessionID); err != nil {
		t.Fatal(err)
	}
	dropped := testworld.AwaitSince(app, beforeClose, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
		return !slices.Contains(m.Order, crown)
	})
	if slices.Contains(dropped.Order, crown) {
		t.Fatalf("closed session crown %s stayed in live order %v", crown, dropped.Order)
	}
	if afterClose := w.App(); slices.Contains(lifeSeedIDs(afterClose.Initial.Seeds), crown) {
		t.Fatalf("fresh client after close still received closed crown %s in %v", crown, lifeSeedIDs(afterClose.Initial.Seeds))
	}
}

func TestGardenPushCarriesBodies(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	planted, err := cli.SeedPlant("", "Body travels", "first body", "", "")
	if err != nil {
		t.Fatal(err)
	}
	app := w.App()
	if len(app.Initial.Seeds) != 1 || app.Initial.Seeds[0].Body != "first body" {
		t.Fatalf("initial state carries %+v, want the seed's body", app.Initial.Seeds)
	}

	if _, err := cli.SeedEdit(planted.ID, "second body", garden.BodySHA256("first body"), ""); err != nil {
		t.Fatal(err)
	}
	changed := nextGardenChange(app)
	if len(changed.Seeds) != 1 || changed.Seeds[0].Body != "second body" {
		t.Fatalf("the change after a body edit carries %+v, want the new body", changed.Seeds)
	}
}
