package daemon_test

import (
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestPlantSuggestsSimilarOpenSeedsInItsProfile(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		home := app.SelectedProfile()
		registerSessions(t, w, cli, "planter")
		parent := plantSeedAs(t, cli, "planter", "Callout wire test races app disconnect")
		original, err := cli.SeedPlant("planter", "Callout wire test races app disconnect", "", parent, "", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, verb := range []string{"harvest", "wither"} {
			closed := plantSeedAs(t, cli, "planter", "Callout wire test races app disconnect")
			lifeMove(t, cli, "planter", closed, verb, "closed", "")
		}
		for _, title := range []string{"Callout wire test", "Callout disconnect", "Callout app"} {
			plantSeedAs(t, cli, "planter", title)
		}
		side := createProfile(app, "Side")
		selectProfile(app, side.ID)
		registerSessions(t, w, cli, "other")
		foreign := plantSeedAs(t, cli, "other", "Callout wire test races app disconnect")
		selectProfile(app, home)
		planted, err := cli.SeedPlant("planter", "Callouts wire test races the app disconnect", "", parent, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(planted.Similar) != 3 || planted.Similar[0].Seed.ID != original.Seed.ID {
			t.Fatalf("similar = %+v, want the open original first of three", planted.Similar)
		}
		for _, hit := range planted.Similar {
			if hit.Seed.ID == parent || hit.Seed.ID == foreign || hit.Seed.ID == planted.Seed.ID || hit.Seed.Status == "harvested" || hit.Seed.Status == "withered" || hit.Like != planted.Seed.ID {
				t.Errorf("unexpected suggestion: %+v", hit)
			}
			if _, err := time.Parse(time.RFC3339Nano, hit.LastActive); err != nil {
				t.Errorf("last active: %v", err)
			}
		}
		for _, title := range []string{"zygomorphicquasar", "!!!"} {
			unrelated, err := cli.SeedPlant("planter", title, "", "", "", "")
			if err != nil || len(unrelated.Similar) != 0 {
				t.Fatalf("no matches: %+v, %v", unrelated, err)
			}
		}
	})
}

func TestPlotMergesSimilarSeedsByBestRankAndTitleOrder(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "planter")
		first := plantSeedAs(t, cli, "planter", "quartz archive")
		second := plantSeedAs(t, cli, "planter", "cobalt worker")
		third := plantSeedAs(t, cli, "planter", "zephyr browser")
		fourth := plantSeedAs(t, cli, "planter", "quartz")
		planted, err := cli.SeedPlot("planter", "", protocol.SeedPlotMessage{
			Title: "quartz archive", Children: []protocol.SeedPlotChild{
				{Title: "cobalt worker"}, {Title: "zephyr browser"}, {Title: "quartz archive followup"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{first, second, third}
		got := []string{}
		for _, hit := range planted.Similar {
			got = append(got, hit.Seed.ID)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("similar IDs = %v, want %v (not rank-two %s)", got, want, fourth)
		}
		for i, hit := range planted.Similar {
			like := planted.Crown.ID
			if i > 0 {
				like = planted.Children[i-1].ID
			}
			if hit.Like != like {
				t.Errorf("suggestion %d like = %s, want %s", i, hit.Like, like)
			}
		}
		for _, id := range append([]string{planted.Crown.ID}, gardenPlotSeedIDs(planted.Children)...) {
			if slices.Contains(got, id) {
				t.Errorf("plot suggests its own seed %s", id)
			}
		}
		dedup, err := cli.SeedPlot("planter", "", protocol.SeedPlotMessage{Title: "cobalt worker", Children: []protocol.SeedPlotChild{{Title: "cobalt worker followup"}}})
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, hit := range dedup.Similar {
			if seen[hit.Seed.ID] {
				t.Errorf("repeated suggestion %s", hit.Seed.ID)
			}
			seen[hit.Seed.ID] = true
		}
		if len(dedup.Similar) == 0 || dedup.Similar[0].Like != dedup.Crown.ID {
			t.Errorf("payload-order tie = %+v", dedup.Similar)
		}
	})
}
