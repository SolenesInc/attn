package daemon

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func search(t *testing.T, d *Daemon, query string, limit int) protocol.Response {
	t.Helper()
	msg := protocol.SeedSearchMessage{Cmd: protocol.CmdSeedSearch, Query: query}
	if limit != 0 {
		msg.Limit = protocol.Ptr(limit)
	}
	return gardenCall(t, func(c net.Conn) { d.handleSeedSearch(c, &msg) })
}

func searchOK(t *testing.T, d *Daemon, query string, limit int) *protocol.SeedSearchResult {
	t.Helper()
	resp := search(t, d, query, limit)
	if !resp.Ok {
		t.Fatalf("search %q: %v", query, protocol.Deref(resp.Error))
	}
	return resp.SeedSearchResult
}

func seededGarden(t *testing.T, d *Daemon) map[string]string {
	t.Helper()
	ids := map[string]string{}
	planted := []struct {
		key, title, body string
	}{
		{"ripples", "harvest-ripples: closing a seed says what it unblocked",
			"Harvest and wither print the seeds they made ready."},
		{"priming", "Wake priming lists the seeds a member holds",
			"A woken member is told the seeds it holds, each with its freshest handoff."},
		{"board", "Board: dropping a seed on Growing offers dispatch an agent",
			"The board already moves a seed between columns; a drop on Growing should offer a dispatch."},
		{"tickets", "Retire tickets in favour of the garden",
			"One tracker, not two. Tickets become seeds and the ticket surface goes."},
		{"fts", "Full-text index for the docstore",
			"Give every docstore collection a SQLite FTS mirror."},
		{"panel", "Garden panel renders a seed body as markdown",
			"The panel shows raw markdown today."},
	}
	for _, seed := range planted {
		ids[seed.key] = plant(t, d, protocol.SeedPlantMessage{
			SourceSessionID: protocol.Ptr("sess-a"), Title: seed.title, Body: protocol.Ptr(seed.body),
		}).ID
	}
	note(t, d, "sess-a", ids["board"],
		"Prototyped the drop target in the panel and it felt right; the dispatch dialog is the missing half.", "")
	note(t, d, "sess-a", ids["panel"], "Waiting on the markdown renderer landing in next.", "")
	move(t, d, "sess-a", ids["tickets"], garden.VerbHarvest, "Tickets are gone; every seed lives in the garden.", "")
	move(t, d, "sess-a", ids["fts"], garden.VerbWither,
		"A scan answers this in a couple of milliseconds, so an index earns nothing.", "")
	return ids
}

func TestSeedSearchFindsAHarvestedSeed(t *testing.T) {
	d := newGardenDaemon(t)
	ids := seededGarden(t, d)
	result := searchOK(t, d, "retire tickets", 0)
	if result.Matched != 1 || len(result.Hits) != 1 {
		t.Fatalf("wanted the closed seed alone, got %d hits of %d matched", len(result.Hits), result.Matched)
	}
	hit := result.Hits[0]
	if hit.Seed.ID != ids["tickets"] || hit.Seed.Status != garden.StatusHarvested {
		t.Fatalf("`was this already done` must reach a harvested seed, got %s %s", hit.Seed.ID, hit.Seed.Status)
	}
	if hit.Where != garden.MatchTitle || !strings.Contains(hit.Snippet, "Retire tickets") {
		t.Fatalf("the hit must say where it matched and quote it, got %s %q", hit.Where, hit.Snippet)
	}
}

func TestSeedSearchFindsWhatOnlyTheLogSays(t *testing.T) {
	d := newGardenDaemon(t)
	ids := seededGarden(t, d)
	result := searchOK(t, d, "dispatch dialog", 0)
	if result.Matched != 1 {
		t.Fatalf("only one log says this, matched %d", result.Matched)
	}
	hit := result.Hits[0]
	if hit.Seed.ID != ids["board"] || hit.Where != garden.MatchLog {
		t.Fatalf("a log-only match must come back marked as one, got %s from %s", hit.Seed.ID, hit.Where)
	}
	if !strings.Contains(hit.Snippet, "the dispatch dialog is the missing half") {
		t.Fatalf("the snippet must quote the note that matched, got %q", hit.Snippet)
	}
}

func TestSeedSearchSaysWhatItTrimmedAndHowToSeeMore(t *testing.T) {
	d := newGardenDaemon(t)
	seededGarden(t, d)
	result := searchOK(t, d, "seed", 2)
	if len(result.Hits) != 2 || result.Matched <= 2 {
		t.Fatalf("a capped answer keeps the full count, got %d hits of %d", len(result.Hits), result.Matched)
	}
	if result.Limit != 2 {
		t.Fatalf("the answer must carry the cap it applied, got %d", result.Limit)
	}
	raised := searchOK(t, d, "seed", garden.MaxSearchResults)
	if len(raised.Hits) != raised.Matched {
		t.Fatalf("raising the limit must show every match, got %d of %d", len(raised.Hits), raised.Matched)
	}
}

func TestSeedSearchRefusalsNameTheLimitAndTheAsk(t *testing.T) {
	d := newGardenDaemon(t)
	seededGarden(t, d)
	for _, tc := range []struct {
		name, query string
		limit       int
		wants       []string
	}{
		{"nothing to look for", "   ", 0, []string{"attn seed search"}},
		{"a body pasted as a query", strings.Repeat("x", garden.MaxSearchQueryChars+1), 0,
			[]string{"max_query_chars=400", "asked for 401"}},
		{"a negative cap", "seed", -1, []string{"asked for -1", "cannot be negative"}},
		{"a cap past the whole garden", "seed", garden.MaxSearchResults + 1,
			[]string{fmt.Sprintf("max_results=%d", garden.MaxSearchResults), fmt.Sprintf("asked for %d", garden.MaxSearchResults+1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := search(t, d, tc.query, tc.limit)
			if resp.Ok {
				t.Fatalf("wanted a refusal, got an answer")
			}
			for _, want := range tc.wants {
				if !strings.Contains(protocol.Deref(resp.Error), want) {
					t.Fatalf("the refusal must say %q, got %q", want, protocol.Deref(resp.Error))
				}
			}
		})
	}
}

func TestSeedSearchSearchesEverySeedPastOnePage(t *testing.T) {
	d := newGardenDaemon(t)
	d.gardenNotePageSize = 2
	ids := seededGarden(t, d)
	result := searchOK(t, d, "waiting markdown renderer", 0)
	if result.Matched != 1 || result.Hits[0].Seed.ID != ids["panel"] {
		t.Fatalf("a note past the first page of the log was not searched: %d matched", result.Matched)
	}
	if result.Searched != len(ids) {
		t.Fatalf("the answer must say how many seeds it read, got %d of %d", result.Searched, len(ids))
	}
}
