package daemon_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheGardenIsReadWholePastOnePageOfSeedsAndNotes(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "gone", "tender", "closer")
		archived := plantSeedAs(t, cli, "", "Shipped legacy sidebar layout")
		lifeMove(t, cli, "closer", archived, "harvest", "shipped last month", "")
		closedAncestor := plantSeedAs(t, cli, "", "Shipped ancestor plot")
		lifeMove(t, cli, "closer", closedAncestor, "harvest", "initial milestone shipped", "")
		openFollowUp := plantSeedAs(t, cli, "", "Follow-up under closed ancestor")
		edgeLink(t, cli, openFollowUp, "part-of", closedAncestor)
		abandoned := plantSeedAs(t, cli, "", "work its tender walked away from")
		lifeMove(t, cli, "gone", abandoned, "tend", "", "")
		if err := cli.Unregister("gone"); err != nil {
			t.Fatal(err)
		}
		panel := plantSeedAs(t, cli, "", "Garden panel renders a seed body as markdown")
		gardenSearchNote(t, cli, "", panel, "Waiting on the markdown renderer landing in next.")
		gardenArtifactNote(t, cli, "", panel, garden.NoteKindAttach, "", gardenArtifactMarkdown("plan.md"))
		oldBlocker, oldDependent := plantSeedAs(t, cli, "", "lay the old pipe"), plantSeedAs(t, cli, "", "run water through the old pipe")
		edgeLink(t, cli, oldBlocker, "part-of", panel)
		edgeLink(t, cli, oldDependent, "part-of", panel)
		checkMoveOK(t, cli, protocol.SeedCheckMessage{SeedID: panel, CheckID: protocol.Ptr("C1"), Verb: "add", Text: protocol.Ptr("The panel renders markdown."), Shared: protocol.Ptr(true)})
		lifeMove(t, cli, "tender", oldDependent, "tend", "", "")
		w.advance(time.Minute)
		for range docstore.MaxLimit {
			gardenSearchNote(t, cli, "", panel, "another day of work")
		}
		children := make([]client.PlotChild, docstore.MaxLimit)
		for i := range children {
			children[i].Title = fmt.Sprintf("row %d", i)
		}
		page, err := cli.SeedPlot("", client.Plot{Title: "a page of seeds", Children: children})
		if err != nil {
			t.Fatal(err)
		}
		edgeLink(t, cli, panel, "part-of", page.Crown.ID)
		w.advance(time.Second)
		newBlocked, closing := plantSeedAs(t, cli, "", "run water through the new pipe"), plantSeedAs(t, cli, "", "lay the new pipe")
		edgeLink(t, cli, oldBlocker, "blocks", newBlocked)
		edgeLink(t, cli, closing, "blocks", newBlocked)
		edgeLink(t, cli, closing, "blocks", oldDependent)
		windowSeeds := docstore.MaxLimit + 9
		seeds := windowSeeds + 1

		result := gardenSearch(t, cli, "", "renderer landing", 0)
		if result.Matched != 1 || len(result.Hits) != 1 || result.Hits[0].Seed.ID != panel || result.Hits[0].Where != garden.MatchLog {
			t.Errorf("search got %+v, want the oldest seed matched by its oldest note", result)
		}
		if result.Searched != seeds {
			t.Errorf("search says it read %d seeds, want all %d", result.Searched, seeds)
		}

		shown := lifeShow(t, cli, panel)
		if len(shown.References) != 1 || protocol.Deref(shown.References[0].Path) != "plan.md" {
			t.Errorf("a page of notes after plan.md was attached the seed references %+v, want plan.md", shown.References)
		}
		if !slices.ContainsFunc(shown.Relations, func(relation protocol.SeedRelation) bool {
			return relation.SeedID == oldDependent && relation.Label == "has-part"
		}) {
			t.Errorf("the old plot's relations = %+v, want its old child %s", shown.Relations, oldDependent)
		}
		if progress := shown.Seed.PlotProgress; progress == nil || progress.Total != 2 || progress.Ready != 1 || progress.Growing != 1 {
			t.Errorf("the old plot's progress = %+v, want two children, one ready and one growing", progress)
		}
		if blocker := lifeShow(t, cli, oldBlocker).Seed; !blocker.Ready || len(blocker.Checks) != 1 || blocker.Checks[0].ID != "C1" {
			t.Errorf("the old blocker has ready=%v and checks %+v, want ready and the old plot's shared C1", blocker.Ready, blocker.Checks)
		}
		ready := gardenPlotReadyIDs(t, cli, "", "", true)
		if !slices.Contains(ready, oldBlocker) || slices.Contains(ready, newBlocked) {
			t.Errorf("ready includes old blocker=%v and new dependent=%v, want true and false", slices.Contains(ready, oldBlocker), slices.Contains(ready, newBlocked))
		}
		stale := gardenPlotList(t, cli, true, int(time.Minute/time.Second))
		if !slices.Contains(lifeSeedIDs(stale.Seeds), abandoned) || slices.Contains(lifeSeedIDs(stale.Seeds), oldDependent) {
			t.Errorf("stale lists %d seeds, want the old open seed %s and not %s, which just gained a blocker", len(stale.Seeds), abandoned, oldDependent)
		}
		listed := gardenPlotList(t, cli, false, 0)
		if len(listed.Seeds) != windowSeeds || listed.Total != seeds {
			t.Errorf("ls returned %d of %d seeds, want every open seed and open-seed ancestor (%d) with total %d", len(listed.Seeds), listed.Total, windowSeeds, seeds)
		}
		if !slices.ContainsFunc(listed.Seeds, func(seed protocol.SeedRow) bool { return seed.ID == panel }) {
			t.Fatal("ls omitted an old open seed past the snapshot cap")
		}
		if !slices.ContainsFunc(listed.Seeds, func(seed protocol.SeedRow) bool { return seed.ID == closedAncestor }) {
			t.Fatal("ls omitted a closed ancestor of an open seed past the snapshot cap")
		}
		if slices.ContainsFunc(listed.Seeds, func(seed protocol.SeedRow) bool { return seed.ID == archived }) {
			t.Fatal("ls unexpectedly included an old closed seed past the snapshot cap")
		}
		app := w.App()
		if len(app.Initial.Seeds) != windowSeeds || protocol.Deref(app.Initial.SeedsTotal) != seeds {
			t.Errorf("the initial garden push returned %d of %d seeds, want every open seed and open-seed ancestor (%d) of %d", len(app.Initial.Seeds), protocol.Deref(app.Initial.SeedsTotal), windowSeeds, seeds)
		}
		if !slices.ContainsFunc(app.Initial.Seeds, func(seed protocol.Seed) bool { return seed.ID == panel }) {
			t.Fatal("the old open seed was dropped from the broadcast")
		}
		if !slices.ContainsFunc(app.Initial.Seeds, func(seed protocol.Seed) bool { return seed.ID == closedAncestor }) {
			t.Fatal("the closed ancestor of an open seed was dropped from the broadcast")
		}
		if slices.ContainsFunc(app.Initial.Seeds, func(seed protocol.Seed) bool { return seed.ID == archived }) {
			t.Fatal("the old closed seed unexpectedly appeared in the capped broadcast")
		}
		for _, seed := range app.Initial.Seeds {
			if seed.ID == newBlocked && seed.Ready {
				t.Error("the push marks the new dependent ready while its old blocker is still open")
			}
			if seed.ID == page.Crown.ID && (seed.PlotProgress == nil || seed.PlotProgress.Total != docstore.MaxLimit+3 || seed.PlotProgress.Growing != 1) {
				t.Errorf("the pushed plot's progress = %+v, want its page plus the old plot and two old children", seed.PlotProgress)
			}
		}
		document := testworld.Request(app, protocol.SeedDocumentGetMessage{Cmd: protocol.CmdSeedDocumentGet, RequestID: "old-plot", SeedID: panel}, protocol.EventSeedDocumentGetResult,
			func(m protocol.SeedDocumentGetResultMessage) bool { return m.RequestID == "old-plot" })
		if !document.Success || document.Document == nil || len(document.Document.Children) != 2 {
			t.Errorf("the old plot's document = %+v, want its two children", document)
		}
		archivedDoc := testworld.Request(app, protocol.SeedDocumentGetMessage{Cmd: protocol.CmdSeedDocumentGet, RequestID: "old-closed", SeedID: archived}, protocol.EventSeedDocumentGetResult,
			func(m protocol.SeedDocumentGetResultMessage) bool { return m.RequestID == "old-closed" })
		if !archivedDoc.Success || archivedDoc.Document == nil || archivedDoc.Document.Seed.ID != archived {
			t.Errorf("the old closed seed's document beyond the broadcast = %+v, want %s", archivedDoc, archived)
		}

		oldSlug := lifeShow(t, cli, archived).Seed.StepSlug
		asked, err := cli.AskOpen("closer", "", fmt.Sprintf("Read %s and %s.\n\n%s", archived, oldSlug, cacheAsk), "", w.Path("closer"))
		if err != nil {
			t.Fatal(err)
		}
		mentions := func() []protocol.AskSeedMention {
			t.Helper()
			result := testworld.Request(app, protocol.AskSeedMentionsMessage{Cmd: protocol.CmdAskSeedMentions, RequestID: "mentions", AskID: asked.ID}, protocol.EventAskSeedMentionsResult,
				func(m protocol.AskSeedMentionsResultMessage) bool { return m.RequestID == "mentions" })
			if !result.Success {
				t.Fatalf("resolving mentions: %s", protocol.Deref(result.Error))
			}
			return result.Mentions
		}
		for _, text := range []string{archived, oldSlug} {
			if !slices.Contains(mentions(), protocol.AskSeedMention{Text: text, SeedID: archived}) {
				t.Errorf("the old closed seed's %s did not resolve beyond the broadcast", text)
			}
		}
		plantSeedAs(t, cli, "", "Shipped legacy sidebar layout")
		if slices.ContainsFunc(mentions(), func(mention protocol.AskSeedMention) bool { return mention.Text == oldSlug }) {
			t.Error("a slug shared by two seeds resolved as unambiguous")
		}

		moved, err := cli.SeedTransition("closer", closing, "harvest", "laid", false, client.SeedTransitionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := lifeSeedIDs(moved.Unblocked); !slices.Equal(got, []string{oldDependent}) {
			t.Errorf("the harvest announced %v unblocked, want only %s: the new seed is still held by the old blocker", got, oldDependent)
		}
		if slices.Contains(gardenPlotReadyIDs(t, cli, "", "", true), newBlocked) {
			t.Error("the new dependent became ready after harvesting only its new blocker, while its old blocker is still open")
		}
		if slices.ContainsFunc(w.App().Initial.Seeds, func(seed protocol.Seed) bool { return seed.ID == newBlocked && seed.Ready }) {
			t.Error("the push marks the new dependent ready after harvesting only its new blocker")
		}
		w.advance(0)
		rippleRung(t, cli, "the old dependent", oldDependent, "tender")
	})
}

func TestGardenSeedsPageLookupAndClosedCounts(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client().WithGardenProfile(app.SelectedProfile(), "")
		setSetting(t, app, "garden_v2_enabled", "true")
		bedRes := testworld.Request(app, protocol.GardenBedSaveMessage{
			Cmd: protocol.CmdGardenBedSave, RequestID: "bed-1", Name: "Core", Repos: []string{},
		}, protocol.EventGardenBedSaveResult, func(m protocol.GardenBedSaveResultMessage) bool { return m.RequestID == "bed-1" })
		if !bedRes.Success || bedRes.Bed == nil {
			t.Fatalf("creating bed: %+v", bedRes)
		}
		bedID := bedRes.Bed.ID

		first, err := cli.SeedPlant("", "Planted first, closed last", "first body", "", "")
		if err != nil {
			t.Fatal(err)
		}
		w.advance(time.Second)
		second, err := cli.SeedPlant("", "Planted second, closed first", "second body", "", "")
		if err != nil {
			t.Fatal(err)
		}
		w.advance(time.Second)
		third, err := cli.SeedPlant("", "Withered in bed", "third body", "", "")
		if err != nil {
			t.Fatal(err)
		}
		w.advance(time.Second)
		closedPlot, err := cli.SeedPlant("", "Closed plot with open child", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		assignRes := testworld.Request(app, protocol.SeedBedSetMessage{
			Cmd: protocol.CmdSeedBedSet, RequestID: "assign-1", SeedID: third.ID, BedID: protocol.Ptr(bedID),
		}, protocol.EventSeedBedSetResult, func(m protocol.SeedBedSetResultMessage) bool { return m.RequestID == "assign-1" })
		if !assignRes.Success {
			t.Fatalf("assigning bed: %+v", assignRes)
		}

		lifeMove(t, cli, "", second.ID, "harvest", "done first", "")
		w.advance(time.Second)
		lifeMove(t, cli, "", third.ID, "wither", "dropped", "")
		w.advance(time.Second)
		lifeMove(t, cli, "", first.ID, "harvest", "done last", "")
		w.advance(time.Second)
		lifeMove(t, cli, "", closedPlot.ID, "harvest", "initial milestone shipped", "")
		if _, err := cli.SeedPlant("", "Open child", "", closedPlot.ID, ""); err != nil {
			t.Fatal(err)
		}

		freshApp := w.App()
		counts := freshApp.Initial.GardenClosedCounts
		if counts == nil {
			t.Fatal("initial state omitted garden_closed_counts")
		}
		if counts.All.Harvested != 2 || counts.All.Withered != 1 {
			t.Errorf("closed_counts.all = %+v, want harvested=2 withered=1 (excluding closed plot with open child)", counts.All)
		}
		if counts.Unassigned.Harvested != 2 || counts.Unassigned.Withered != 0 {
			t.Errorf("closed_counts.unassigned = %+v, want harvested=2 withered=0", counts.Unassigned)
		}
		if len(counts.ByBed) != 1 || counts.ByBed[0].BedID != bedID || counts.ByBed[0].Harvested != 0 || counts.ByBed[0].Withered != 1 {
			t.Errorf("closed_counts.by_bed = %+v, want [{%s 0 1}]", counts.ByBed, bedID)
		}

		page1 := testworld.Request(freshApp, protocol.GardenSeedsPageMessage{
			Cmd: protocol.CmdGardenSeedsPage, RequestID: "p1", Limit: protocol.Ptr(2),
		}, protocol.EventGardenSeedsPageResult, func(m protocol.GardenSeedsPageResultMessage) bool { return m.RequestID == "p1" })
		if !page1.Success || page1.Total != 3 || page1.Harvested != 2 || page1.Withered != 1 {
			t.Fatalf("page1 = %+v", page1)
		}
		if got := lifeSeedIDs(page1.Seeds); !slices.Equal(got, []string{first.ID, third.ID}) {
			t.Errorf("page1 order = %v, want state_changed_at DESC [%s %s]", got, first.ID, third.ID)
		}
		if page1.Seeds[0].Body != "" {
			t.Errorf("paged seed unexpectedly carried body %q", page1.Seeds[0].Body)
		}
		if page1.NextBefore == nil {
			t.Fatal("page1 omitted next_before")
		}

		lifeMove(t, cli, "", third.ID, "replant", "", "")
		changed := testworld.Await(freshApp, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
			return m.ClosedCounts != nil && m.ClosedCounts.All.Withered == 0
		})
		if changed.ClosedCounts.All.Harvested != 2 || changed.ClosedCounts.Unassigned.Harvested != 2 || changed.ClosedCounts.Unassigned.Withered != 0 || len(changed.ClosedCounts.ByBed) != 0 {
			t.Errorf("garden_seeds_changed closed_counts after replant = %+v, want all={2,0} unassigned={2,0} by_bed=[]", changed.ClosedCounts)
		}
		page2 := testworld.Request(freshApp, protocol.GardenSeedsPageMessage{
			Cmd: protocol.CmdGardenSeedsPage, RequestID: "p2", Before: page1.NextBefore, Limit: protocol.Ptr(2),
		}, protocol.EventGardenSeedsPageResult, func(m protocol.GardenSeedsPageResultMessage) bool { return m.RequestID == "p2" })
		if !page2.Success || page2.NextBefore != nil {
			t.Fatalf("page2 = %+v", page2)
		}
		if got := lifeSeedIDs(page2.Seeds); !slices.Equal(got, []string{second.ID}) {
			t.Errorf("page2 after cursor seed was replanted = %v, want [%s]", got, second.ID)
		}

		badCursor := testworld.Request(freshApp, protocol.GardenSeedsPageMessage{
			Cmd: protocol.CmdGardenSeedsPage, RequestID: "p-bad", Before: protocol.Ptr(first.ID),
		}, protocol.EventGardenSeedsPageResult, func(m protocol.GardenSeedsPageResultMessage) bool { return m.RequestID == "p-bad" })
		if badCursor.Success || badCursor.Error == nil {
			t.Errorf("bare seed id as before cursor = %+v, want refusal", badCursor)
		}

		bedPage := testworld.Request(freshApp, protocol.GardenSeedsPageMessage{
			Cmd: protocol.CmdGardenSeedsPage, RequestID: "p-bed", Bed: protocol.Ptr(bedID), Status: protocol.Ptr("all"),
		}, protocol.EventGardenSeedsPageResult, func(m protocol.GardenSeedsPageResultMessage) bool { return m.RequestID == "p-bed" })
		if !bedPage.Success || !slices.Equal(lifeSeedIDs(bedPage.Seeds), []string{third.ID}) {
			t.Errorf("bed page = %+v, want [%s]", bedPage, third.ID)
		}

		other := createProfile(freshApp, "Other")
		otherSeed, err := w.Client().WithGardenProfile(other.ID, "").SeedPlant("", "Other profile seed", "secret", "", "")
		if err != nil {
			t.Fatal(err)
		}
		lookup := testworld.Request(freshApp, protocol.GardenSeedsLookupMessage{
			Cmd: protocol.CmdGardenSeedsLookup, RequestID: "l1", Ids: []string{first.ID, "s-000000", otherSeed.ID, first.ID},
		}, protocol.EventGardenSeedsLookupResult, func(m protocol.GardenSeedsLookupResultMessage) bool { return m.RequestID == "l1" })
		if !lookup.Success || !slices.Equal(lifeSeedIDs(lookup.Seeds), []string{first.ID}) || lookup.Seeds[0].Body != "first body" {
			t.Errorf("lookup = %+v, want only %s with its body", lookup, first.ID)
		}
	})
}

func TestGardenPlotPushCountsOnlyDirectHiddenClosedChildren(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		outer := plantSeedAs(t, cli, "", "Release")
		inner := plantSeedAs(t, cli, "", "Packaging")
		archived := plantSeedAs(t, cli, "", "Build the package")
		edgeLink(t, cli, inner, "part-of", outer)
		edgeLink(t, cli, archived, "part-of", inner)
		lifeMove(t, cli, "", archived, "harvest", "built", "")

		kept := plantSeedAs(t, cli, "", "Published package")
		edgeLink(t, cli, kept, "part-of", inner)
		lifeMove(t, cli, "", kept, "harvest", "published", "")
		followUp := plantSeedAs(t, cli, "", "Document how to install it")
		edgeLink(t, cli, followUp, "part-of", kept)

		app := w.App()
		requireCount := func(seeds []protocol.Seed, id string, want int) {
			t.Helper()
			idx := slices.IndexFunc(seeds, func(s protocol.Seed) bool { return s.ID == id })
			if idx < 0 {
				t.Fatalf("missing plot %s in %v", id, lifeSeedIDs(seeds))
			}
			got := seeds[idx].HiddenClosedChildren
			if got == nil || *got != want {
				t.Errorf("plot %s hidden closed children = %v, want %d", id, protocol.Deref(got), want)
			}
		}

		requireCount(app.Initial.Seeds, outer, 0)
		requireCount(app.Initial.Seeds, inner, 1)
		page := testworld.Request(app, protocol.GardenSeedsPageMessage{
			Cmd: protocol.CmdGardenSeedsPage, RequestID: "inner-history", PlotID: &inner,
		}, protocol.EventGardenSeedsPageResult, func(m protocol.GardenSeedsPageResultMessage) bool {
			return m.RequestID == "inner-history"
		})
		if !page.Success || page.Total != 1 || !slices.Equal(lifeSeedIDs(page.Seeds), []string{archived}) {
			t.Fatalf("archive page = %+v, want only %s", page, archived)
		}

		lifeMove(t, cli, "", followUp, "harvest", "documented", "")
		requireCount(nextGardenChange(app).Seeds, inner, 2)

		lifeMove(t, cli, "", followUp, "replant", "", "")
		requireCount(nextGardenChange(app).Seeds, inner, 1)
	})
}
