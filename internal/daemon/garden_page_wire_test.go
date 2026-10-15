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
		lifeMove(t, cli, "tender", oldDependent, "tend", "", "")
		w.advance(time.Second)
		for range docstore.MaxLimit {
			gardenSearchNote(t, cli, "", panel, "another day of work")
		}
		children := make([]protocol.SeedPlotChild, docstore.MaxLimit)
		for i := range children {
			children[i].Title = fmt.Sprintf("row %d", i)
		}
		if _, err := cli.SeedPlot("", protocol.SeedPlotMessage{Title: "a page of seeds", Children: children}); err != nil {
			t.Fatal(err)
		}
		w.advance(time.Second)
		newBlocked, closing := plantSeedAs(t, cli, "", "run water through the new pipe"), plantSeedAs(t, cli, "", "lay the new pipe")
		edgeLink(t, cli, oldBlocker, "blocks", newBlocked)
		edgeLink(t, cli, closing, "blocks", newBlocked)
		edgeLink(t, cli, closing, "blocks", oldDependent)
		seeds := docstore.MaxLimit + 7

		result := gardenSearch(t, cli, "", "markdown renderer", 0)
		if result.Matched != 1 || len(result.Hits) != 1 || result.Hits[0].Seed.ID != panel || result.Hits[0].Where != garden.MatchLog {
			t.Errorf("search got %d hits of %d matched, want the oldest seed matched by its oldest note", len(result.Hits), result.Matched)
		}
		if result.Searched != seeds {
			t.Errorf("search says it read %d seeds, want all %d", result.Searched, seeds)
		}

		shown := lifeShow(t, cli, panel)
		if len(shown.References) != 1 || protocol.Deref(shown.References[0].Path) != "plan.md" {
			t.Errorf("a page of notes after plan.md was attached the seed references %+v, want plan.md", shown.References)
		}

		moved, err := cli.SeedTransition("closer", closing, "harvest", "laid", false, client.SeedTransitionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := lifeSeedIDs(moved.Unblocked); !slices.Equal(got, []string{oldDependent}) {
			t.Errorf("the harvest announced %v unblocked, want only %s: the new seed is still held by the old blocker", got, oldDependent)
		}
		w.advance(0)
		rippleRung(t, cli, "the old dependent", oldDependent, "tender")

		review := gardenReviewStart(t, cli)
		if !slices.ContainsFunc(review.Items, func(item protocol.GardenReviewItem) bool { return item.SeedID == abandoned }) {
			t.Errorf("the review holds %d items, none for the oldest abandoned seed %s", len(review.Items), abandoned)
		}
	})
}
