package daemon_test

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPlotPlantsItsCrownAndChildrenInOneStep(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "sess-a")
		w.advance(0)
		pushesBefore := gardenPlotPushes(app)

		planted, err := cli.SeedPlot("sess-a", "trellis", protocol.SeedPlotMessage{
			Title: "ship the thing", Body: protocol.Ptr("# the plan"),
			Children: []protocol.SeedPlotChild{
				{Title: "first step"},
				{Title: "second step"},
				{Title: "third step", Blocks: []string{"second-step"}},
			},
		})
		if err != nil {
			t.Fatalf("plot: %v", err)
		}
		w.advance(0)
		if pushes := gardenPlotPushes(app) - pushesBefore; pushes != 1 {
			t.Errorf("planting one plot pushed the garden %d times, want once", pushes)
		}

		if planted.Crown.Title != "ship the thing" || planted.Crown.Body != "# the plan" || len(planted.Children) != 3 {
			t.Fatalf("plot = crown %+v with %d children, want the titled crown and three children", planted.Crown, len(planted.Children))
		}
		bySlug := map[string]protocol.Seed{}
		for _, child := range planted.Children {
			bySlug[child.StepSlug] = child
		}
		for slug, child := range bySlug {
			var partOf, blocks []string
			for _, edge := range child.Edges {
				switch edge.Kind {
				case garden.EdgePartOf:
					partOf = append(partOf, edge.To)
				case garden.EdgeBlocks:
					blocks = append(blocks, edge.To)
				}
			}
			wantBlocks := []string(nil)
			if slug == "third-step" {
				wantBlocks = []string{bySlug["second-step"].ID}
			}
			if !slices.Equal(partOf, []string{planted.Crown.ID}) || !slices.Equal(blocks, wantBlocks) || child.PlanterMember != "trellis" {
				t.Errorf("%s is part of %v, blocks %v, planted by %q; want the crown, %v and trellis", slug, partOf, blocks, child.PlanterMember, wantBlocks)
			}
		}
		ready := gardenPlotReadyIDs(t, cli, "", planted.Crown.ID, false)
		want := []string{bySlug["first-step"].ID, bySlug["third-step"].ID}
		slices.Sort(ready)
		slices.Sort(want)
		if !slices.Equal(ready, want) {
			t.Errorf("ready in the fresh plot = %v, want the two unblocked children", ready)
		}

		joined, err := cli.SeedPlant("sess-a", "inside", "", planted.Crown.ID, "", "")
		if err != nil {
			t.Fatalf("plant into the plot: %v", err)
		}
		if edges := joined.Seed.Edges; len(edges) != 1 || edges[0].Kind != garden.EdgePartOf || edges[0].To != planted.Crown.ID {
			t.Errorf("--part-of planted with edges %+v, want it in the plot", edges)
		}
		total := gardenPlotList(t, cli, false, 0).Total
		if _, err := cli.SeedPlant("sess-a", "orphan", "", "s-zzzzzz", "", ""); err == nil || !strings.Contains(err.Error(), "s-zzzzzz") {
			t.Errorf("planting under a crown that is not here = %v, want a refusal naming it", err)
		}
		if _, err := cli.SeedPlot("sess-a", "", protocol.SeedPlotMessage{
			Title: "ship it", Children: []protocol.SeedPlotChild{{Title: "a", Blocks: []string{"nobody"}}},
		}); err == nil || !strings.Contains(err.Error(), "nobody") {
			t.Errorf("a plot with a dangling blocks = %v, want a refusal naming it", err)
		}
		if after := gardenPlotList(t, cli, false, 0).Total; after != total {
			t.Errorf("the refusals left %d seeds, want %d", after, total)
		}
	})
}

func TestTheCrownCarriesItsPlotProgress(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	planter := spawnPanes(w, app, w.Path("planter"))[0].session
	planted, err := cli.SeedPlot(planter, "", protocol.SeedPlotMessage{
		Title: "ship it", Children: []protocol.SeedPlotChild{{Title: "a"}, {Title: "b", Blocks: []string{"a"}}},
	})
	if err != nil {
		t.Fatalf("plot: %v", err)
	}
	unblocked := planted.Children[1].ID
	for _, move := range []struct{ verb, reason string }{{"tend", ""}, {"harvest", "done"}} {
		if _, err := cli.SeedTransition(planter, unblocked, move.verb, move.reason, "", false, client.SeedTransitionOptions{}); err != nil {
			t.Fatalf("%s b: %v", move.verb, err)
		}
	}

	want := protocol.SeedPlotProgress{Total: 2, Done: 1, Ready: 1}
	shown, err := cli.SeedShow(planter, planted.Crown.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := shown.Seed.PlotProgress; got == nil || *got != want {
		t.Errorf("show progress = %+v, want %+v", got, want)
	}
	for _, seed := range gardenPlotList(t, cli, false, 0).Seeds {
		switch {
		case seed.ID == planted.Crown.ID && (seed.PlotProgress == nil || *seed.PlotProgress != want):
			t.Errorf("listed crown progress = %+v, want %+v", seed.PlotProgress, want)
		case seed.ID != planted.Crown.ID && seed.PlotProgress != nil:
			t.Errorf("%s carries a plot it does not have: %+v", seed.ID, seed.PlotProgress)
		}
	}
}

func TestStaleListsOnlyTheQuietOpenSeeds(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "sess-a")
		quiet := plantSeedAs(t, cli, "sess-a", "quiet for a while")
		noted := plantSeedAs(t, cli, "sess-a", "old document, live log")
		closed := plantSeedAs(t, cli, "sess-a", "quiet but harvested")
		for _, move := range []struct{ verb, reason string }{{"tend", ""}, {"harvest", "done"}} {
			if _, err := cli.SeedTransition("sess-a", closed, move.verb, move.reason, "", false, client.SeedTransitionOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		w.advance(2 * time.Minute)
		plantSeedAs(t, cli, "sess-a", "just planted")
		if _, err := cli.SeedNote("sess-a", noted, "still on this", "", "", false, nil); err != nil {
			t.Fatal(err)
		}

		if all := gardenPlotList(t, cli, false, 0); all.Total != 4 || all.StaleWindowSeconds != nil {
			t.Errorf("a plain listing = %d seeds with window %v, want all four and no window", all.Total, all.StaleWindowSeconds)
		}
		stale := gardenPlotList(t, cli, true, 60)
		if len(stale.Seeds) != 1 || stale.Seeds[0].ID != quiet || stale.Total != 1 || protocol.Deref(stale.StaleWindowSeconds) != 60 {
			t.Errorf("stale over a minute = %+v (total %d, window %v), want only %s, counted, under the window it used", stale.Seeds, stale.Total, stale.StaleWindowSeconds, quiet)
		}
		byDefault := gardenPlotList(t, cli, true, 0)
		if len(byDefault.Seeds) != 0 || protocol.Deref(byDefault.StaleWindowSeconds) != int(garden.DefaultStaleWindow/time.Second) {
			t.Errorf("stale by default = %+v under window %v, want nothing under the default window", byDefault.Seeds, byDefault.StaleWindowSeconds)
		}
	})
}

func TestADelegationAtACrownIsScopedToItsPlot(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	planner := spawnPanes(w, app, w.Path("planner"))[0].session
	cwd := w.Path("shop")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	planted, err := cli.SeedPlot(planner, "", protocol.SeedPlotMessage{
		Title: "ship the thing", Body: protocol.Ptr("# the plan"),
		Children: []protocol.SeedPlotChild{
			{Title: "first step", Blocks: []string{"third-step"}},
			{Title: "second step"},
			{Title: "third step"},
		},
	})
	if err != nil {
		t.Fatalf("plot: %v", err)
	}
	first, second := planted.Children[0].ID, planted.Children[1].ID
	outside := plantSeedAs(t, cli, planner, "somewhere else")
	for _, body := range []string{"old direction", "the fixture is seeded"} {
		if _, err := cli.SeedNote(planner, first, body, "trellis", garden.NoteKindHandoff, false, nil); err != nil {
			t.Fatal(err)
		}
	}

	refused := gardenPlotDelegate(app, fakeagent.Codex, planner, "nowhere", "s-zzzzzz", cwd)
	if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "s-zzzzzz") {
		t.Fatalf("delegating at a crown that is not here = %+v, want a refusal naming it", refused)
	}
	if sessions, err := cli.Query(""); err != nil || len(sessions) != 1 {
		t.Fatalf("after the refusal the sessions are %+v (%v), want only the planner", sessions, err)
	}

	delegated := gardenPlotDelegate(app, fakeagent.Codex, planner, "plot", planted.Crown.ID, cwd)
	if !delegated.Success {
		t.Fatalf("delegating at the crown: %s", protocol.Deref(delegated.Error))
	}
	delegate := delegated.Result.SessionID
	w.Launched(delegate)

	primed, err := cli.SeedReady(delegate, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if primed.Scope != "plot" || primed.Crown == nil || primed.Crown.ID != planted.Crown.ID {
		t.Fatalf("the delegate's own ready = scope %q crown %+v, want the plot it was dispatched to", primed.Scope, primed.Crown)
	}
	if got := gardenPlotSeedIDs(primed.Seeds); !slices.Equal(got, []string{first, second}) {
		t.Errorf("the delegate is offered %v, want the plot's unblocked children %v", got, []string{first, second})
	}
	if len(primed.Handoffs) != 1 || primed.Handoffs[0].Body != "the fixture is seeded" || primed.Handoffs[0].AuthorMember != "trellis" {
		t.Errorf("the delegate is handed %+v, want only the freshest handoff", primed.Handoffs)
	}
	if everywhere := gardenPlotReadyIDs(t, cli, delegate, "", true); !slices.Contains(everywhere, outside) {
		t.Errorf("--all from the delegate = %v, want the seed outside the plot too", everywhere)
	}
}

func gardenPlotPushes(app *testworld.Peer) int {
	pushes := 0
	for _, event := range app.Received() {
		if event.Event == protocol.EventGardenSeedsUpdated {
			pushes++
		}
	}
	return pushes
}

func gardenPlotList(t *testing.T, cli *client.Client, stale bool, windowSeconds int) *protocol.SeedListResult {
	t.Helper()
	listed, err := cli.SeedList("sess-a", stale, windowSeconds)
	if err != nil {
		t.Fatalf("seed ls: %v", err)
	}
	return listed
}

func gardenPlotReadyIDs(t *testing.T, cli *client.Client, session, crown string, all bool) []string {
	t.Helper()
	ready, err := cli.SeedReady(session, crown, all)
	if err != nil {
		t.Fatalf("seed ready: %v", err)
	}
	return gardenPlotSeedIDs(ready.Seeds)
}

func gardenPlotSeedIDs(seeds []protocol.Seed) []string {
	ids := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		ids = append(ids, seed.ID)
	}
	return ids
}

func gardenPlotDelegate(app *testworld.Peer, agent fakeagent.Harness, source, requestID, crown, cwd string) protocol.DelegateResultMessage {
	return testworld.Request(app, protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: requestID, Cwd: cwd, Agent: protocol.Ptr(string(agent)),
		SourceSessionID: protocol.Ptr(source),
		Assignment:      protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(crown)},
	}, protocol.EventDelegateResult, func(m protocol.DelegateResultMessage) bool { return protocol.Deref(m.RequestID) == requestID })
}
