package daemon_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func TestSeedSearchMatchesWordsAndIdentifiers(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client().WithGardenProfile(app.SelectedProfile(), "")
		ids := map[string]string{}
		for _, row := range []struct{ key, title, body string }{
			{"report", "Publish the report", ""},
			{"callout", "Callout races the app disconnect", ""},
			{"flaky", "A flaky connection", ""},
			{"identifier", "Regression test", "TestARefusedDashboardRefreshKeepsThePreviousList"},
			{"prose", "A refused dashboard refresh keeps the previous list", ""},
		} {
			seed, err := cli.SeedPlant("", row.title, row.body, "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			ids[row.key] = seed.Seed.ID
		}
		for _, tc := range []struct {
			query string
			want  []string
		}{
			{"port", nil},
			{"callouts disconnected", []string{"callout"}},
			{"flak", []string{"flaky"}},
			{"refused dashboard", []string{"identifier", "prose"}},
			{"TestARefusedDashboardRefreshKeepsThePreviousList", []string{"identifier", "prose"}},
		} {
			func() {
				got := gardenSearch(t, cli, "", tc.query, 0)
				if len(got.Hits) != len(tc.want) && !strings.HasPrefix(tc.query, "TestA") {
					t.Fatalf("got %+v, want %v", got, tc.want)
				}
				for _, key := range tc.want {
					found := false
					for _, hit := range got.Hits {
						if hit.Seed.ID == ids[key] {
							found = true
							if key == "identifier" && hit.Snippet != "TestARefusedDashboardRefreshKeepsThePreviousList" {
								t.Errorf("snippet is not the original identifier: %q", hit.Snippet)
							}
						}
					}
					if !found {
						t.Errorf("missing %s in %+v", key, got)
					}
				}
			}()
		}
		for _, tc := range []struct {
			query string
			limit int
			wants []string
		}{
			{"   ", 0, []string{"attn seed search"}},
			{"***", 0, []string{"words"}},
			{strings.Repeat("x", garden.MaxSearchQueryChars+1), 0, []string{"max_query_chars=400", "asked for 401"}},
			{"seed", -1, []string{"asked for -1", "cannot be negative"}},
			{"seed", garden.MaxSearchResults + 1, []string{fmt.Sprintf("max_results=%d", garden.MaxSearchResults), fmt.Sprintf("asked for %d", garden.MaxSearchResults+1)}},
		} {
			_, err := cli.SeedSearch("", tc.query, tc.limit)
			if err == nil {
				t.Fatalf("query %q limit %d was not refused", tc.query, tc.limit)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q lacks %q", err, want)
				}
			}
		}
	})
}

func TestSeedSearchRanksCoverageAndKeepsClosedSeeds(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client().WithGardenProfile(app.SelectedProfile(), "")
		plant := func(title, body string) string {
			seed, err := cli.SeedPlant("", title, body, "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			return seed.Seed.ID
		}
		title := plant("Quartz amber", "")
		body := plant("Body match", "Original quartz amber text")
		log := plant("Log match", "")
		gardenSearchNote(t, cli, "", log, "A log entry quoting quartz amber together.")
		cross := plant("Quartz across fields", "Amber lives in the body")
		for i := 0; i < 11; i++ {
			plant(fmt.Sprintf("Quartz filler %d", i), "")
		}
		result := gardenSearch(t, cli, "", "quartz amber", 0)
		if result.Matched != 4 || result.Partial != 11 || len(result.Hits) != 10 || result.Limit != 10 || result.Searched != 15 {
			t.Fatalf("counts or default: %+v", result)
		}
		if result.Hits[0].Seed.ID != title {
			t.Fatalf("title must rank first: %+v", result.Hits)
		}
		positions := map[string]int{}
		for i, hit := range result.Hits {
			positions[hit.Seed.ID] = i
			if _, err := time.Parse(time.RFC3339Nano, hit.LastActive); err != nil {
				t.Errorf("last_active %q: %v", hit.LastActive, err)
			}
			if hit.Seed.Status != garden.StatusPlanted || hit.Where == "" || hit.Snippet == "" {
				t.Errorf("incomplete hit: %+v", hit)
			}
			if hit.Seed.ID == body && (hit.Where != garden.MatchBody || hit.Snippet != "Original quartz amber text") {
				t.Errorf("body quote: %+v", hit)
			}
			if hit.Seed.ID == log && (hit.Where != garden.MatchLog || hit.Snippet != "A log entry quoting quartz amber together.") {
				t.Errorf("log quote: %+v", hit)
			}
		}
		for _, id := range []string{body, log, cross} {
			if i, ok := positions[id]; !ok || i >= 4 {
				t.Errorf("full match %s did not precede partial matches: %+v", id, positions)
			}
		}
		raised := gardenSearch(t, cli, "", "quartz amber", garden.MaxSearchResults)
		if len(raised.Hits) != 15 || raised.Matched != 4 || raised.Partial != 11 {
			t.Errorf("raised limit: %+v", raised)
		}

		open := plant("Paired ranking", "")
		w.advance(time.Hour)
		closed := plant("Paired ranking", "")
		if _, err := cli.SeedTransition("", closed, "harvest", "finished", "", false, client.SeedTransitionOptions{}); err != nil {
			t.Fatal(err)
		}
		paired := gardenSearch(t, cli, "", "paired ranking", 0)
		if len(paired.Hits) != 2 || paired.Hits[0].Seed.ID != open || paired.Hits[1].Seed.ID != closed || paired.Hits[1].Seed.Status != garden.StatusHarvested {
			t.Fatalf("open boost and closed visibility: %+v", paired)
		}
	})
}

func TestSeedSearchFollowsWritesAndRestart(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profile := app.SelectedProfile()
		cli := w.Client().WithGardenProfile(profile, "")
		seed, err := cli.SeedPlant("", "Durable word search", "Obsidian preserves the original body", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		id := seed.Seed.ID
		gardenSearchNote(t, cli, "", id, "Chrysoprase is only in this note")
		assertHit := func(query string, want bool) {
			t.Helper()
			result := gardenSearch(t, cli, "", query, 0)
			if (len(result.Hits) == 1 && result.Hits[0].Seed.ID == id) != want || !want && len(result.Hits) != 0 {
				t.Fatalf("search %q want found=%v: %+v", query, want, result)
			}
		}
		assertHit("chrysoprase", true)
		if _, err := cli.SeedEdit(id, "Malachite replaces the previous body", garden.BodySHA256(seed.Seed.Body), ""); err != nil {
			t.Fatal(err)
		}
		assertHit("malachite", true)
		assertHit("obsidian", false)
		if _, err := cli.SeedRollback(id, nil); err != nil {
			t.Fatal(err)
		}
		assertHit("obsidian", true)
		assertHit("malachite", false)
		if _, err := cli.SeedTransition("", id, "harvest", "finished", "", false, client.SeedTransitionOptions{}); err != nil {
			t.Fatal(err)
		}
		before := gardenSearch(t, cli, "", "obsidian chrysoprase", 0)
		if before.Matched != 1 || before.Hits[0].Seed.Status != garden.StatusHarvested {
			t.Fatalf("closed full match: %+v", before)
		}
		w.restart()
		cli = w.Client().WithGardenProfile(profile, "")
		after := gardenSearch(t, cli, "", "obsidian chrysoprase", 0)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("restart changed search: before=%+v after=%+v", before, after)
		}
	})
}

func gardenSearchNote(t *testing.T, cli *client.Client, session, seedID, body string) {
	t.Helper()
	if _, err := cli.SeedNote(protocol.SessionID(session), seedID, body, "", "", false, nil); err != nil {
		t.Fatalf("note %q on %s: %v", body, seedID, err)
	}
}

func gardenSearch(t *testing.T, cli *client.Client, session, query string, limit int) *protocol.SeedSearchResult {
	t.Helper()
	result, err := cli.SeedSearch(protocol.SessionID(session), query, limit)
	if err != nil {
		t.Fatalf("search %q: %v", query, err)
	}
	return result
}
