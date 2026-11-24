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
