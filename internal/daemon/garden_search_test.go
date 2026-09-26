package daemon

import (
	"net"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func searchOK(t *testing.T, d *Daemon, query string, limit int) *protocol.SeedSearchResult {
	t.Helper()
	msg := protocol.SeedSearchMessage{Cmd: protocol.CmdSeedSearch, Query: query}
	if limit != 0 {
		msg.Limit = protocol.Ptr(limit)
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedSearch(c, &msg) })
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
