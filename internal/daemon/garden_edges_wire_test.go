package daemon_test

import (
	"os"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestBlockingEdgesGateReadiness(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "worker")
	first := plantSeedAs(t, cli, "worker", "waited longest")
	second := plantSeedAs(t, cli, "worker", "just planted")

	undispatched := edgeReady(t, cli, "worker", "", false)
	if got := lifeSeedIDs(undispatched.Seeds); !slices.Equal(got, []string{first, second}) {
		t.Fatalf("ready = %v, want both seeds oldest first", got)
	}
	if undispatched.Scope != "garden" || undispatched.ScopeID != "" || undispatched.Crown != nil {
		t.Fatalf("flag-free ready for an undispatched session scoped to %s/%s with crown %+v, want the whole garden", undispatched.Scope, undispatched.ScopeID, undispatched.Crown)
	}

	edgeLink(t, cli, first, "blocks", second)
	pushed := testworld.Await(app, protocol.EventGardenSeedsUpdated, func(m protocol.GardenSeedsUpdatedMessage) bool {
		return len(m.Seeds) == 2 && slices.ContainsFunc(m.Seeds, func(s protocol.Seed) bool { return len(s.Edges) == 1 })
	})
	for _, seed := range pushed.Seeds {
		if seed.Ready != (seed.ID == first) {
			t.Errorf("the garden push after linking carries %s ready=%t, want only the blocker ready", seed.ID, seed.Ready)
		}
	}
	if got := lifeSeedIDs(edgeReady(t, cli, "worker", "", false).Seeds); !slices.Equal(got, []string{first}) {
		t.Fatalf("ready after linking = %v, want only the blocker %s", got, first)
	}
	if again := edgeLink(t, cli, first, "blocks", second); again.Changed {
		t.Error("re-linking the same edge reported a change")
	}

	unlinked, err := cli.SeedLink(first, "blocks", second, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(unlinked.Seed.Edges) != 0 {
		t.Errorf("unlinking left %+v", unlinked.Seed.Edges)
	}
	if got := lifeSeedIDs(edgeReady(t, cli, "worker", "", false).Seeds); !slices.Equal(got, []string{first, second}) {
		t.Fatalf("ready after unlinking = %v, want both seeds back", got)
	}

	plot := plantSeedAs(t, cli, "worker", "the plot")
	edgeLink(t, cli, first, "blocks", second)
	edgeLink(t, cli, second, "part-of", plot)
	lifeMove(t, cli, "worker", first, "tend", "", "trellis")
	lifeMove(t, cli, "worker", first, "harvest", "done", "trellis")
	if got := lifeSeedIDs(edgeReady(t, cli, "worker", "", false).Seeds); !slices.Equal(got, []string{second}) {
		t.Errorf("after harvesting the blocker, ready = %v, want the dependent %s", got, second)
	}
}

func TestSeedShowReadsEdgesFromBothEnds(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	first := plantSeedAs(t, cli, "", "first")
	second := plantSeedAs(t, cli, "", "second")
	plot := plantSeedAs(t, cli, "", "the plot")
	edgeLink(t, cli, first, "blocks", second)
	edgeLink(t, cli, second, "part-of", plot)

	relations := lifeShow(t, cli, second).Relations
	labels := map[string]string{}
	for _, relation := range relations {
		labels[relation.Label] = relation.SeedID
		if relation.Title == "" || relation.Status == "" {
			t.Errorf("relation %+v carries no title or status", relation)
		}
	}
	if len(relations) != 2 || labels["part-of"] != plot || labels["blocked-by"] != first {
		t.Errorf("the blocked seed shows relations %+v, want part-of %s and blocked-by %s", relations, plot, first)
	}
	edgeRelation(t, cli, first, "blocks", second)

	origin := plantSeedAs(t, cli, "", "the work in hand")
	found := plantSeedAs(t, cli, "", "the follow-up")
	edgeLink(t, cli, found, "discovered-from", origin)
	edgeRelation(t, cli, found, "discovered-from", origin)
	edgeRelation(t, cli, origin, "discovered", found)
	if got := lifeSeedIDs(edgeReady(t, cli, "", "", true).Seeds); !slices.Contains(got, found) || !slices.Contains(got, origin) {
		t.Errorf("discovered-from changed readiness: ready = %v", got)
	}

	if _, err := cli.SeedLink(found, "discovered-from", origin, true); err != nil {
		t.Fatalf("unlink discovered-from: %v", err)
	}
	for _, seed := range []string{found, origin} {
		if got := lifeShow(t, cli, seed).Relations; len(got) != 0 {
			t.Errorf("%s keeps relations %+v after the unlink", seed, got)
		}
	}
}

func TestSeedLinkRefusalsNameTheSeedsAndTheWayOut(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	first := plantSeedAs(t, cli, "", "first")
	second := plantSeedAs(t, cli, "", "second")
	edgeLink(t, cli, first, "blocks", second)

	for _, refusal := range []struct {
		name       string
		from, kind string
		to         string
		unlink     bool
		wants      []string
	}{
		{"a blocks cycle", second, "blocks", first, false, []string{first, second, "deadlock", "attn seed unlink"}},
		{"an unknown kind", first, "sort-of", second, false, []string{"blocks and part-of"}},
		{"an unknown seed", first, "blocks", "s-zzzzzz", false, []string{"s-zzzzzz"}},
		{"a malformed id", first, "blocks", "nope", false, []string{"seed id"}},
		{"unlinking an edge that is not there", first, "part-of", second, true, []string{"does not part-of"}},
	} {
		_, err := cli.SeedLink(refusal.from, refusal.kind, refusal.to, refusal.unlink)
		lifeRefusal(t, refusal.name, err, refusal.wants...)
	}
	if got := lifeSeedIDs(edgeReady(t, cli, "", "", true).Seeds); !slices.Equal(got, []string{first}) {
		t.Errorf("after the refusals ready = %v, want the blocker alone as before", got)
	}
}

func TestADispatchedSessionIsReadyForItsPlot(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "planner")
	plot, err := cli.SeedPlant("planner", "the plot", "Work through the plot.", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	crown := plot.Seed.ID
	inside, err := cli.SeedPlant("planner", "inside", "", crown, "", "")
	if err != nil {
		t.Fatal(err)
	}
	outside := plantSeedAs(t, cli, "planner", "outside")
	if got := lifeSeedIDs(edgeReady(t, cli, "planner", "", false).Seeds); !slices.Equal(got, []string{inside.Seed.ID, outside}) {
		t.Fatalf("an undispatched session's ready = %v, want both leaves", got)
	}

	cwd := w.Path("shop")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	delegated := testworld.Request(app, protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "work-the-plot", Cwd: cwd, Agent: protocol.Ptr("claude"),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(crown)},
	}, protocol.EventDelegateResult, func(m protocol.DelegateResultMessage) bool { return protocol.Deref(m.RequestID) == "work-the-plot" })
	if !delegated.Success {
		t.Fatalf("dispatching the plot: %s", protocol.Deref(delegated.Error))
	}
	dispatched := delegated.Result.SessionID
	w.Launched(dispatched)

	scoped := edgeReady(t, cli, dispatched, "", false)
	if got := lifeSeedIDs(scoped.Seeds); !slices.Equal(got, []string{inside.Seed.ID}) || scoped.Scope != "plot" || scoped.ScopeID != crown {
		t.Errorf("the dispatched session's ready = %v scoped to %s/%s, want the plot's child in plot %s", got, scoped.Scope, scoped.ScopeID, crown)
	}
	if scoped.Crown == nil || scoped.Crown.ID != crown || scoped.Crown.PlotProgress == nil {
		t.Errorf("the plot answer carries crown %+v, want the crown with its progress", scoped.Crown)
	}
	whole := edgeReady(t, cli, dispatched, "", true)
	if got := lifeSeedIDs(whole.Seeds); !slices.Equal(got, []string{inside.Seed.ID, outside}) || whole.Scope != "garden" {
		t.Errorf("--all from the dispatched session = %v scoped to %s, want the whole garden", got, whole.Scope)
	}
}

func TestReadyScopesToAPlotAndListsPlotsBeforeLooseSeeds(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	firstPlot, err := cli.SeedPlot("", "", protocol.SeedPlotMessage{Title: "first plot", Children: []protocol.SeedPlotChild{{Title: "first child"}}})
	if err != nil {
		t.Fatal(err)
	}
	secondPlot, err := cli.SeedPlot("", "", protocol.SeedPlotMessage{Title: "second plot", Children: []protocol.SeedPlotChild{{Title: "second child"}}})
	if err != nil {
		t.Fatal(err)
	}
	loose := plantSeedAs(t, cli, "", "loose work")

	all := edgeReady(t, cli, "", "", true)
	if got, want := lifeSeedIDs(all.Plots), []string{firstPlot.Crown.ID, secondPlot.Crown.ID}; !slices.Equal(got, want) {
		t.Errorf("ready --all plot headers = %v, want %v", got, want)
	}
	if got, want := lifeSeedIDs(all.Seeds), []string{firstPlot.Children[0].ID, secondPlot.Children[0].ID, loose}; !slices.Equal(got, want) {
		t.Errorf("ready --all seeds = %v, want %v", got, want)
	}

	deeper, err := cli.SeedPlant("", "deeper", "", firstPlot.Children[0].ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	scoped := edgeReady(t, cli, "", firstPlot.Crown.ID, false)
	if got := lifeSeedIDs(scoped.Seeds); !slices.Equal(got, []string{deeper.Seed.ID}) || scoped.Scope != "plot" || scoped.ScopeID != firstPlot.Crown.ID {
		t.Errorf("ready --plot = %v scoped to %s/%s, want the plot's one leaf %s", got, scoped.Scope, scoped.ScopeID, deeper.Seed.ID)
	}
	_, err = cli.SeedReady("", "s-zzzzzz", false)
	lifeRefusal(t, "ready for an unknown plot", err, "s-zzzzzz")
}

func TestASeedHeldByASessionThatEndedIsReadyAgain(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	registerSessions(t, w, cli, "planner", "holder")
	seed := plantSeedAs(t, cli, "planner", "held")
	lifeMove(t, cli, "holder", seed, "tend", "", "")
	if got := lifeSeedIDs(edgeReady(t, cli, "planner", "", false).Seeds); len(got) != 0 {
		t.Fatalf("ready = %v while a live session holds the seed, want nothing", got)
	}

	if err := cli.Unregister("holder"); err != nil {
		t.Fatal(err)
	}
	if got := lifeSeedIDs(edgeReady(t, cli, "planner", "", false).Seeds); !slices.Equal(got, []string{seed}) {
		t.Errorf("ready = %v after the holder ended, want the seed back", got)
	}
}

func edgeLink(t *testing.T, cli *client.Client, from, kind, to string) *protocol.SeedLinkResult {
	t.Helper()
	linked, err := cli.SeedLink(from, kind, to, false)
	if err != nil {
		t.Fatalf("link %s %s %s: %v", from, kind, to, err)
	}
	return linked
}

func edgeReady(t *testing.T, cli *client.Client, session, plot string, all bool) *protocol.SeedReadyResult {
	t.Helper()
	ready, err := cli.SeedReady(session, plot, all)
	if err != nil {
		t.Fatalf("ready as %q: %v", session, err)
	}
	return ready
}

func edgeRelation(t *testing.T, cli *client.Client, seedID, label, relatedID string) {
	t.Helper()
	relations := lifeShow(t, cli, seedID).Relations
	if len(relations) != 1 || relations[0].Label != label || relations[0].SeedID != relatedID {
		t.Errorf("%s shows relations %+v, want %s %s", seedID, relations, label, relatedID)
	}
}
