package daemon

import (
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func link(t *testing.T, d *Daemon, from, kind, to string, unlink bool) protocol.Response {
	t.Helper()
	msg := protocol.SeedLinkMessage{Cmd: protocol.CmdSeedLink, SeedID: from, Kind: kind, ToSeedID: to}
	if unlink {
		msg.Unlink = protocol.Ptr(true)
	}
	return gardenCall(t, func(c net.Conn) { d.handleSeedLink(c, &msg) })
}

func mustLink(t *testing.T, d *Daemon, from, kind, to string) protocol.SeedLinkResult {
	t.Helper()
	resp := link(t, d, from, kind, to, false)
	if !resp.Ok {
		t.Fatalf("link %s %s %s: %v", from, kind, to, protocol.Deref(resp.Error))
	}
	return *resp.SeedLinkResult
}

func ready(t *testing.T, d *Daemon, msg protocol.SeedReadyMessage) protocol.SeedReadyResult {
	t.Helper()
	msg.Cmd = protocol.CmdSeedReady
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedReady(c, &msg) })
	if !resp.Ok {
		t.Fatalf("ready: %v", protocol.Deref(resp.Error))
	}
	return *resp.SeedReadyResult
}

func readyIDs(result protocol.SeedReadyResult) []string {
	out := make([]string, 0, len(result.Seeds))
	for _, seed := range result.Seeds {
		out = append(out, seed.ID)
	}
	return out
}

func TestGardenEdges_HarvestingABlockerSurfacesTheDependent(t *testing.T) {
	d := newGardenDaemon(t)
	a := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "first"})
	b := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "second"})
	c := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "the plot"})

	mustLink(t, d, a.ID, garden.EdgeBlocks, b.ID)
	mustLink(t, d, b.ID, garden.EdgePartOf, c.ID)

	first := ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})
	if got := readyIDs(first); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("ready = %v, want only the unblocked seed %s", got, a.ID)
	}
	if first.Scope != "garden" || first.ScopeID != "" {
		t.Fatalf("flag-free ready scoped to %s/%s, want the whole garden", first.Scope, first.ScopeID)
	}

	move(t, d, "sess-a", a.ID, garden.VerbTend, "", "trellis")
	move(t, d, "sess-a", a.ID, garden.VerbHarvest, "done", "trellis")

	second := ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})
	if got := readyIDs(second); len(got) != 1 || got[0] != b.ID {
		t.Fatalf("after harvesting the blocker, ready = %v, want %s", got, b.ID)
	}
}

func TestGardenEdges_ReadyRidesTheSeedOnTheWire(t *testing.T) {
	d := newGardenDaemon(t)
	var pushed []protocol.Seed
	d.gardenBroadcastHook = func(seeds []protocol.Seed, _ int) { pushed = seeds }

	blocker := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "blocker"})
	blocked := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "blocked"})
	mustLink(t, d, blocker.ID, garden.EdgeBlocks, blocked.ID)

	states := map[string]bool{}
	for _, seed := range pushed {
		states[seed.ID] = seed.Ready
	}
	if len(pushed) != 2 {
		t.Fatalf("linking pushed %d seeds, want the whole garden", len(pushed))
	}
	if !states[blocker.ID] || states[blocked.ID] {
		t.Fatalf("the push carries ready=%v, want the blocker ready and the blocked one not", states)
	}
}

func TestGardenEdges_ShowListsBothDirections(t *testing.T) {
	d := newGardenDaemon(t)
	a := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "first"})
	b := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "second"})
	c := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "the plot"})
	mustLink(t, d, a.ID, garden.EdgeBlocks, b.ID)
	mustLink(t, d, b.ID, garden.EdgePartOf, c.ID)

	relations := show(t, d, b.ID).Relations
	got := map[string]string{}
	for _, relation := range relations {
		got[relation.Label] = relation.SeedID
		if relation.Title == "" || relation.Status == "" {
			t.Fatalf("relation %+v carries no title or status; a bare id is not readable", relation)
		}
	}
	if got[garden.EdgePartOf] != c.ID || got["blocked-by"] != a.ID || len(relations) != 2 {
		t.Fatalf("show relations = %+v", relations)
	}

	if got := show(t, d, a.ID).Relations; len(got) != 1 || got[0].Label != garden.EdgeBlocks || got[0].SeedID != b.ID {
		t.Fatalf("the blocking side reads %+v, want one outbound blocks edge", got)
	}
}

func TestGardenEdges_DiscoveredFromLinksShowsAndUnlinksFromBothEnds(t *testing.T) {
	d := newGardenDaemon(t)
	origin := plant(t, d, protocol.SeedPlantMessage{Title: "the work in hand"})
	found := plant(t, d, protocol.SeedPlantMessage{Title: "the follow-up"})

	mustLink(t, d, found.ID, garden.EdgeDiscoveredFrom, origin.ID)
	assertRelation := func(seedID, label, relatedID string) {
		t.Helper()
		relations := show(t, d, seedID).Relations
		if len(relations) != 1 || relations[0].Label != label || relations[0].SeedID != relatedID {
			t.Fatalf("show %s relations = %+v, want %s %s", seedID, relations, label, relatedID)
		}
	}
	assertRelation(found.ID, garden.EdgeDiscoveredFrom, origin.ID)
	assertRelation(origin.ID, "discovered", found.ID)
	if got := readyIDs(ready(t, d, protocol.SeedReadyMessage{All: protocol.Ptr(true)})); !slices.Contains(got, found.ID) || !slices.Contains(got, origin.ID) {
		t.Fatalf("discovered-from changed readiness: %v", got)
	}

	resp := link(t, d, found.ID, garden.EdgeDiscoveredFrom, origin.ID, true)
	if !resp.Ok {
		t.Fatalf("unlink discovered-from: %v", protocol.Deref(resp.Error))
	}
	if got := show(t, d, found.ID).Relations; len(got) != 0 {
		t.Fatalf("found seed kept relations after unlink: %+v", got)
	}
	if got := show(t, d, origin.ID).Relations; len(got) != 0 {
		t.Fatalf("origin kept relations after unlink: %+v", got)
	}
}

func TestGardenEdges_UnlinkPutsTheSeedBack(t *testing.T) {
	d := newGardenDaemon(t)
	a := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "first"})
	b := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "second"})
	mustLink(t, d, a.ID, garden.EdgeBlocks, b.ID)

	if again := mustLink(t, d, a.ID, garden.EdgeBlocks, b.ID); again.Changed {
		t.Fatal("re-linking the same edge reported a change")
	}

	resp := link(t, d, a.ID, garden.EdgeBlocks, b.ID, true)
	if !resp.Ok {
		t.Fatalf("unlink: %v", protocol.Deref(resp.Error))
	}
	if len(resp.SeedLinkResult.Seed.Edges) != 0 {
		t.Fatalf("unlink left %+v", resp.SeedLinkResult.Seed.Edges)
	}
	if got := readyIDs(ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})); len(got) != 2 {
		t.Fatalf("after unlinking, ready = %v, want both seeds", got)
	}
}

func TestGardenEdges_RefusalsNameBothSeeds(t *testing.T) {
	d := newGardenDaemon(t)
	a := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "first"})
	b := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "second"})
	mustLink(t, d, a.ID, garden.EdgeBlocks, b.ID)

	cycle := link(t, d, b.ID, garden.EdgeBlocks, a.ID, false)
	if cycle.Ok {
		t.Fatal("a blocks cycle was accepted")
	}
	for _, want := range []string{a.ID, b.ID, "deadlock", "attn seed unlink"} {
		if !strings.Contains(protocol.Deref(cycle.Error), want) {
			t.Fatalf("cycle refusal does not name %q: %s", want, protocol.Deref(cycle.Error))
		}
	}

	kind := link(t, d, a.ID, "sort-of", b.ID, false)
	if kind.Ok || !strings.Contains(protocol.Deref(kind.Error), "blocks and part-of") {
		t.Fatalf("an unknown kind was not refused with the kinds that exist: %+v", kind)
	}

	missing := link(t, d, a.ID, garden.EdgeBlocks, "s-zzzzzz", false)
	if missing.Ok || !strings.Contains(protocol.Deref(missing.Error), "s-zzzzzz") {
		t.Fatalf("an unknown seed was not refused by name: %+v", missing)
	}

	malformed := link(t, d, a.ID, garden.EdgeBlocks, "nope", false)
	if malformed.Ok || !strings.Contains(protocol.Deref(malformed.Error), "seed id") {
		t.Fatalf("a malformed id was not refused by shape: %+v", malformed)
	}

	stray := link(t, d, a.ID, garden.EdgePartOf, b.ID, true)
	if stray.Ok || !strings.Contains(protocol.Deref(stray.Error), "does not part-of") {
		t.Fatalf("unlinking an edge that is not there was not refused: %+v", stray)
	}
}

func TestGardenEdges_ReadyInfersTheDispatchedPlot(t *testing.T) {
	d := newGardenDaemon(t)
	crown := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "the plot"})
	inside := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "inside", PartOf: protocol.Ptr(crown.ID),
	})
	outside := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "outside"})

	if got := readyIDs(ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})); len(got) != 2 {
		t.Fatalf("undispatched ready = %v, want both seeds", got)
	}

	if err := d.recordGardenDispatch("sess-a", crown.ID, "", "", "", false); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}

	dispatched := ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})
	if got := readyIDs(dispatched); len(got) != 1 || got[0] != inside.ID {
		t.Fatalf("dispatched ready = %v, want the plot's child %s", got, inside.ID)
	}
	if dispatched.Scope != "plot" || dispatched.ScopeID != crown.ID {
		t.Fatalf("dispatched scope = %s/%s, want plot/%s", dispatched.Scope, dispatched.ScopeID, crown.ID)
	}
	if dispatched.Crown == nil || dispatched.Crown.PlotProgress == nil {
		t.Fatalf("a plot answer did not carry its crown and progress: %+v", dispatched.Crown)
	}

	all := ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a"), All: protocol.Ptr(true)})
	if got := readyIDs(all); len(got) != 2 || all.Scope != "garden" {
		t.Fatalf("--all from a dispatched session = %v (scope %s), want the whole garden", got, all.Scope)
	}
	move(t, d, "sess-a", outside.ID, garden.VerbTend, "", "trellis")
}

func TestGardenEdges_ReadyFallsBackWhenTheCrownIsGone(t *testing.T) {
	d := newGardenDaemon(t)
	plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "still here"})
	if err := d.recordGardenDispatch("sess-a", "s-zzzzzz", "", "", "", false); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}

	result := ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})
	if result.Scope != "garden" || len(result.Seeds) != 1 {
		t.Fatalf("ready with a dangling dispatch = %+v, want the whole garden", result)
	}
}

func TestGardenEdges_ReadyScopesToAPlot(t *testing.T) {
	d := newGardenDaemon(t)
	crown := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "the plot"})
	inside := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "inside"})
	deeper := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "deeper"})
	plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "outside"})
	mustLink(t, d, inside.ID, garden.EdgePartOf, crown.ID)
	mustLink(t, d, deeper.ID, garden.EdgePartOf, inside.ID)

	result := ready(t, d, protocol.SeedReadyMessage{Plot: protocol.Ptr(crown.ID)})
	if got := readyIDs(result); len(got) != 1 || got[0] != deeper.ID {
		t.Fatalf("plot ready = %v, want the one leaf %s", got, deeper.ID)
	}
	if result.Scope != "plot" || result.ScopeID != crown.ID {
		t.Fatalf("plot scope = %s/%s", result.Scope, result.ScopeID)
	}

	missing := gardenCall(t, func(c net.Conn) {
		d.handleSeedReady(c, &protocol.SeedReadyMessage{Cmd: protocol.CmdSeedReady, Plot: protocol.Ptr("s-zzzzzz")})
	})
	if missing.Ok || !strings.Contains(protocol.Deref(missing.Error), "s-zzzzzz") {
		t.Fatalf("an unknown plot was not refused by name: %+v", missing)
	}
}

func TestGardenEdges_ReadyHandsOverTheOldestFirst(t *testing.T) {
	d := newGardenDaemon(t)
	first := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "waited longest"})
	second := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "just planted"})

	got := readyIDs(ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")}))
	if len(got) != 2 || got[0] != first.ID || got[1] != second.ID {
		t.Fatalf("ready order = %v, want %s before %s", got, first.ID, second.ID)
	}
}

func TestGardenEdges_ReadyCarriesPlotHeadersAndLooseSeeds(t *testing.T) {
	d := newGardenDaemon(t)
	firstPlot := plant(t, d, protocol.SeedPlantMessage{Title: "first plot"})
	firstChild := plant(t, d, protocol.SeedPlantMessage{Title: "first child", PartOf: protocol.Ptr(firstPlot.ID)})
	secondPlot := plant(t, d, protocol.SeedPlantMessage{Title: "second plot"})
	secondChild := plant(t, d, protocol.SeedPlantMessage{Title: "second child", PartOf: protocol.Ptr(secondPlot.ID)})
	loose := plant(t, d, protocol.SeedPlantMessage{Title: "loose work"})

	result := ready(t, d, protocol.SeedReadyMessage{All: protocol.Ptr(true)})
	plotIDs := make([]string, 0, len(result.Plots))
	for _, plot := range result.Plots {
		plotIDs = append(plotIDs, plot.ID)
	}
	if want := []string{firstPlot.ID, secondPlot.ID}; !slices.Equal(plotIDs, want) {
		t.Fatalf("plot headers = %v, want %v", plotIDs, want)
	}
	if got, want := readyIDs(result), []string{firstChild.ID, secondChild.ID, loose.ID}; !slices.Equal(got, want) {
		t.Fatalf("ready seeds = %v, want %v", got, want)
	}
}

func TestGardenEdges_ReadyReleasesASeedWhoseSessionIsGone(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "held"})
	move(t, d, "sess-b", seed.ID, garden.VerbTend, "", "")

	if got := readyIDs(ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})); len(got) != 0 {
		t.Fatalf("ready = %v, want nothing: a live session holds the seed", got)
	}

	d.store.Remove("sess-b")
	if got := readyIDs(ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})); len(got) != 1 {
		t.Fatalf("ready = %v, want the seed back once its session is gone", got)
	}
}

func TestGardenEdges_LaunchPrimerCountsTheSameReady(t *testing.T) {
	d := newGardenDaemon(t)
	a := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "first"})
	b := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "second"})
	mustLink(t, d, a.ID, garden.EdgeBlocks, b.ID)

	prime, err := d.gardenPrime("sess-a")
	if err != nil {
		t.Fatalf("gardenPrime: %v", err)
	}
	if want := len(ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")}).Seeds); len(prime.Seeds) != want {
		t.Fatalf("primer count = %d, want %d", len(prime.Seeds), want)
	}
	if len(prime.Seeds) != 1 {
		t.Fatalf("primer count = %d, want 1", len(prime.Seeds))
	}
	if prime.Crown != nil {
		t.Fatalf("an undispatched session was primed with a plot: %+v", prime.Crown)
	}
}

func TestGardenEdges_OutpostRefusesLinkAndReady(t *testing.T) {
	d := newEnrolledDaemon(t, "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()

	if resp := link(t, d, "s-7k3f9m", garden.EdgeBlocks, "s-7k3f9n", false); resp.Ok {
		t.Fatal("seed link answered on an outpost")
	}
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedReady(c, &protocol.SeedReadyMessage{Cmd: protocol.CmdSeedReady, All: protocol.Ptr(true)})
	})
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), garden.Surface) {
		t.Fatalf("seed ready on an outpost: %+v", resp)
	}
	if primer, err := d.gardenPrime("sess-a"); err == nil || primer != nil {
		t.Fatalf("an outpost primed a launching agent with %+v, %v", primer, err)
	}
}
