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
		abandoned := gardenReviewRegisteredAbandonedSeed(t, w, cli, "gone", "work its tender walked away from")
		registerSessions(t, w, cli, "tender", "closer")
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
		children := make([]protocol.SeedPlotChild, docstore.MaxLimit)
		for i := range children {
			children[i].Title = fmt.Sprintf("row %d", i)
		}
		page, err := cli.SeedPlot("", protocol.SeedPlotMessage{Title: "a page of seeds", Children: children})
		if err != nil {
			t.Fatal(err)
		}
		edgeLink(t, cli, panel, "part-of", page.Crown.ID)
		w.advance(time.Second)
		newBlocked, closing := plantSeedAs(t, cli, "", "run water through the new pipe"), plantSeedAs(t, cli, "", "lay the new pipe")
		edgeLink(t, cli, oldBlocker, "blocks", newBlocked)
		edgeLink(t, cli, closing, "blocks", newBlocked)
		edgeLink(t, cli, closing, "blocks", oldDependent)
		seeds := docstore.MaxLimit + 7

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
		if len(listed.Seeds) != docstore.MaxLimit || listed.Total != seeds {
			t.Errorf("ls returned %d of %d seeds, want a capped page of %d with total %d", len(listed.Seeds), listed.Total, docstore.MaxLimit, seeds)
		}
		app := w.App()
		if len(app.Initial.Seeds) != docstore.MaxLimit || protocol.Deref(app.Initial.SeedsTotal) != seeds {
			t.Errorf("the initial garden push returned %d of %d seeds, want %d of %d", len(app.Initial.Seeds), protocol.Deref(app.Initial.SeedsTotal), docstore.MaxLimit, seeds)
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

		review := gardenReviewStart(t, cli)
		if !slices.ContainsFunc(review.Items, func(item protocol.GardenReviewItem) bool { return item.SeedID == abandoned }) {
			t.Errorf("the review holds %d items, none for the oldest abandoned seed %s", len(review.Items), abandoned)
		}
	})
}
