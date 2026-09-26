package daemon_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func TestSeedSearchReachesEverySeedAndSaysWhereItMatched(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	gardener := spawnPanes(w, app, w.Path("gardener"))[0].session
	ids := map[string]string{}
	for _, seed := range []struct{ key, title, body string }{
		{"ripples", "harvest-ripples: closing a seed says what it unblocked", "Harvest and wither print the seeds they made ready."},
		{"priming", "Wake priming lists the seeds a member holds", "A woken member is told the seeds it holds, each with its freshest handoff."},
		{"board", "Board: dropping a seed on Growing offers dispatch an agent", "The board already moves a seed between columns; a drop on Growing should offer a dispatch."},
		{"tickets", "Retire tickets in favour of the garden", "One tracker, not two. Tickets become seeds and the ticket surface goes."},
		{"fts", "Full-text index for the docstore", "Give every docstore collection a SQLite FTS mirror."},
		{"panel", "Garden panel renders a seed body as markdown", "The panel shows raw markdown today."},
	} {
		planted, err := cli.SeedPlant(gardener, seed.title, seed.body, "", "", "")
		if err != nil {
			t.Fatalf("plant %q: %v", seed.title, err)
		}
		ids[seed.key] = planted.Seed.ID
	}
	gardenSearchNote(t, cli, gardener, ids["board"], "Prototyped the drop target in the panel and it felt right; the dispatch dialog is the missing half.")
	if _, err := cli.SeedTransition(gardener, ids["tickets"], "harvest", "Tickets are gone; every seed lives in the garden.", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatalf("harvest the tickets seed: %v", err)
	}
	if _, err := cli.SeedTransition(gardener, ids["fts"], "wither", "A scan answers this in a couple of milliseconds, so an index earns nothing.", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatalf("wither the index seed: %v", err)
	}

	t.Run("a harvested seed is found by its title", func(t *testing.T) {
		result := gardenSearch(t, cli, gardener, "retire tickets", 0)
		if result.Matched != 1 || len(result.Hits) != 1 {
			t.Fatalf("got %d hits of %d matched, want the closed seed alone", len(result.Hits), result.Matched)
		}
		hit := result.Hits[0]
		if hit.Seed.ID != ids["tickets"] || hit.Seed.Status != garden.StatusHarvested {
			t.Errorf("the hit is %s %s, want the harvested tickets seed", hit.Seed.ID, hit.Seed.Status)
		}
		if hit.Where != garden.MatchTitle || !strings.Contains(hit.Snippet, "Retire tickets") {
			t.Errorf("the hit says %s %q, want a title match quoting it", hit.Where, hit.Snippet)
		}
	})

	t.Run("text only the log says is a log match quoting the note", func(t *testing.T) {
		result := gardenSearch(t, cli, gardener, "dispatch dialog", 0)
		if result.Matched != 1 || len(result.Hits) != 1 {
			t.Fatalf("got %d hits of %d matched, want the board seed alone", len(result.Hits), result.Matched)
		}
		hit := result.Hits[0]
		if hit.Seed.ID != ids["board"] || hit.Where != garden.MatchLog {
			t.Errorf("the hit is %s from %s, want the board seed from its log", hit.Seed.ID, hit.Where)
		}
		if !strings.Contains(hit.Snippet, "the dispatch dialog is the missing half") {
			t.Errorf("the snippet %q does not quote the note that matched", hit.Snippet)
		}
		if result.Searched != len(ids) {
			t.Errorf("the answer says it read %d seeds, want %d", result.Searched, len(ids))
		}
	})

	t.Run("a capped answer keeps the full count and raising the cap shows every match", func(t *testing.T) {
		capped := gardenSearch(t, cli, gardener, "seed", 2)
		if len(capped.Hits) != 2 || capped.Matched <= 2 || capped.Limit != 2 {
			t.Fatalf("got %d hits of %d matched under limit %d, want 2 hits, the full count and the cap", len(capped.Hits), capped.Matched, capped.Limit)
		}
		raised := gardenSearch(t, cli, gardener, "seed", garden.MaxSearchResults)
		if len(raised.Hits) != raised.Matched || raised.Matched != capped.Matched {
			t.Errorf("the raised cap shows %d of %d, want all %d", len(raised.Hits), raised.Matched, capped.Matched)
		}
	})

	for _, tc := range []struct {
		name, query string
		limit       int
		wants       []string
	}{
		{"nothing to look for", "   ", 0, []string{"attn seed search"}},
		{"a body pasted as a query", strings.Repeat("x", garden.MaxSearchQueryChars+1), 0, []string{"max_query_chars=400", "asked for 401"}},
		{"a negative cap", "seed", -1, []string{"asked for -1", "cannot be negative"}},
		{"a cap past the whole garden", "seed", garden.MaxSearchResults + 1, []string{
			fmt.Sprintf("max_results=%d", garden.MaxSearchResults), fmt.Sprintf("asked for %d", garden.MaxSearchResults+1),
		}},
	} {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			_, err := cli.SeedSearch(gardener, tc.query, tc.limit)
			if err == nil {
				t.Fatal("answered; want a refusal")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q does not say %q", err, want)
				}
			}
		})
	}
}

func gardenSearchNote(t *testing.T, cli *client.Client, session, seedID, body string) {
	t.Helper()
	if _, err := cli.SeedNote(session, seedID, body, "", "", false, nil); err != nil {
		t.Fatalf("note %q on %s: %v", body, seedID, err)
	}
}

func gardenSearch(t *testing.T, cli *client.Client, session, query string, limit int) *protocol.SeedSearchResult {
	t.Helper()
	result, err := cli.SeedSearch(session, query, limit)
	if err != nil {
		t.Fatalf("search %q: %v", query, err)
	}
	return result
}
