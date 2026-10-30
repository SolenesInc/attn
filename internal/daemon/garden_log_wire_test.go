package daemon_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

type wantEntry struct {
	seed, kind, body, author string
}

func TestEverySeedChangeWritesOneLogEntryWithOneNoteAddedEvent(t *testing.T) {
	github := newHarvestGitHub(t)
	w := newWorld(t, fakeagent.Claude)
	app, base := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	worker := w.Spawn(app, fakeagent.Claude, w.Path("worker"))
	w.Launched(worker)
	cli := base.WithGardenProfile("", protocol.SessionID(worker))
	log := newSeedLogProbe(t, w, cli)

	work := plantSeedAs(t, cli, worker, "work through the moves")
	blocked := plantSeedAs(t, cli, worker, "waits on the work")
	plot := plantSeedAs(t, cli, worker, "a plot")
	part := plantSeedAs(t, cli, worker, "a part")
	origin := plantSeedAs(t, cli, worker, "where it was found")
	found := plantSeedAs(t, cli, worker, "what was found")
	log.watch(work, blocked, plot, part, origin, found)

	move := func(seed, verb, reasonOrComment string) {
		t.Helper()
		lifeMove(t, cli, worker, seed, verb, reasonOrComment, "")
	}

	move(work, "tend", "")
	log.expect("tend", wantEntry{work, "move", "tended", worker})
	move(work, "park", "waiting on review")
	log.expect("park -m", wantEntry{work, "move", "parked: waiting on review", worker})
	move(work, "tend", "")
	log.expect("tend again", wantEntry{work, "move", "tended", worker})
	move(work, "park", "")
	log.expect("park", wantEntry{work, "move", "parked", worker})
	move(work, "park", "")
	log.expect("a converged park")

	for _, edge := range []struct {
		from, kind, to       string
		fromLinked, toLinked string
		fromGone, toGone     string
	}{
		{work, "blocks", blocked, "now blocks " + blocked, "now waits for " + work, "no longer blocks " + blocked, "no longer waits for " + work},
		{part, "part-of", plot, "joined plot " + plot, part + " joined this plot", "left plot " + plot, part + " left this plot"},
		{found, "discovered-from", origin, "discovered from " + origin, found + " was discovered here", "no longer discovered from " + origin, found + " no longer discovered here"},
	} {
		if _, err := cli.SeedLink(edge.from, edge.kind, edge.to, false); err != nil {
			t.Fatal(err)
		}
		log.expect("link "+edge.kind, wantEntry{edge.from, "link", edge.fromLinked, worker}, wantEntry{edge.to, "link", edge.toLinked, worker})
		if _, err := cli.SeedLink(edge.from, edge.kind, edge.to, true); err != nil {
			t.Fatal(err)
		}
		log.expect("unlink "+edge.kind, wantEntry{edge.from, "link", edge.fromGone, worker}, wantEntry{edge.to, "link", edge.toGone, worker})
	}

	if _, err := cli.SeedLink(work, "blocks", blocked, false); err != nil {
		t.Fatal(err)
	}
	log.expect("link the blocker again", wantEntry{work, "link", "now blocks " + blocked, worker}, wantEntry{blocked, "link", "now waits for " + work, worker})
	move(work, "harvest", "shipped")
	log.expect("a harvest that unblocks a seed",
		wantEntry{work, "move", "harvested: shipped", worker}, wantEntry{blocked, "move", "ready: " + work + " harvested", worker})
	move(work, "harvest", "shipped")
	log.expect("a converged harvest")
	move(work, "replant", "")
	log.expect("replant", wantEntry{work, "move", "replanted", worker})
	move(work, "wither", "dropped")
	log.expect("a wither that frees the seed the replant blocked again",
		wantEntry{work, "move", "withered: dropped", worker}, wantEntry{blocked, "move", "ready: " + work + " withered", worker})

	url := github.open(71, "Ship it")
	gardenArtifactNote(t, cli, worker, blocked, garden.NoteKindAttach, "", &protocol.SeedArtifactReference{Kind: "url", URL: protocol.Ptr(url)})
	log.expect("attach the pull request", wantEntry{blocked, "attach", "attached " + url, worker})
	move(blocked, "tend", "")
	log.expect("tend the blocked seed", wantEntry{blocked, "move", "tended", worker})
	harvestArmAs(t, cli, worker, "", blocked, url)
	log.expect("harvest --when-merged arming", wantEntry{blocked, "move", "parked; harvests when acme/shop#71 merges", worker})
	if _, err := cli.SeedTransition(protocol.SessionID(worker), blocked, "harvest", "", "", false, client.SeedTransitionOptions{ClearHarvestWhen: true}); err != nil {
		t.Fatal(err)
	}
	log.expect("harvest --when-merged clearing", wantEntry{blocked, "move", "harvest-on-merge cleared", worker})

	body := "Work through the moves.\n"
	shown := lifeShow(t, cli, blocked).Seed
	if _, err := cli.SeedEdit(blocked, body, garden.BodySHA256(shown.Body), "say what to do"); err != nil {
		t.Fatal(err)
	}
	log.expect("a body edit", wantEntry{blocked, "edit", fmt.Sprintf("body v2: %d → %d bytes — say what to do", len(shown.Body), len(body)), worker})

	move(blocked, "tend", "")
	log.expect("tend before asking", wantEntry{blocked, "move", "tended", worker})
	asked, err := cli.AskOpen(protocol.SessionID(worker), blocked, cacheAsk, "", w.Path("worker"))
	if err != nil {
		t.Fatal(err)
	}
	answerCacheAsk(t, app, asked.ID)
	answer := log.expectOne("an ask answer", blocked)
	if answer.Kind != "answer" || answer.AuthorSession != "" || !strings.HasPrefix(answer.Body, "🙋 The user answered") {
		t.Errorf("the answer entry = %+v, want an authorless answer", answer)
	}

	child, err := cli.SeedPlant(protocol.SessionID(worker), "a new part", "", plot, "", "")
	if err != nil {
		t.Fatal(err)
	}
	log.expect("a plant under a plot", wantEntry{plot, "link", child.Seed.ID + " joined this plot", worker})
	if notes := lifeNoteBodies(t, cli, child.Seed.ID); len(notes) != 0 {
		t.Errorf("the planted child's log = %q, want it empty", notes)
	}

	fresh, err := cli.SeedPlot(protocol.SessionID(worker), "", protocol.SeedPlotMessage{Title: "a fresh plot", Children: []protocol.SeedPlotChild{
		{Title: "first step", Blocks: []string{"second step"}}, {Title: "second step"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, seed := range append([]protocol.Seed{fresh.Crown}, fresh.Children...) {
		if notes := lifeNoteBodies(t, cli, seed.ID); len(notes) != 0 {
			t.Errorf("%s, planted by one plot payload, logs %q; want the payload to start every log empty", seed.ID, notes)
		}
	}
}

type seedLogProbe struct {
	t     *testing.T
	cli   *client.Client
	db    *sql.DB
	seeds []string
	seen  map[string]bool
}

func newSeedLogProbe(t *testing.T, w *world, cli *client.Client) *seedLogProbe {
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(w.Dir, "attn.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &seedLogProbe{t: t, cli: cli, db: db, seen: map[string]bool{}}
}

func (p *seedLogProbe) watch(seeds ...string) {
	p.seeds = append(p.seeds, seeds...)
	p.fresh()
}

func (p *seedLogProbe) fresh() []protocol.SeedNote {
	p.t.Helper()
	var added []protocol.SeedNote
	for _, seed := range p.seeds {
		notes, err := p.cli.SeedNotes("", seed, client.SeedNotesQuery{})
		if err != nil {
			p.t.Fatal(err)
		}
		for _, note := range notes.Notes {
			if !p.seen[note.ID] {
				p.seen[note.ID] = true
				added = append(added, note)
			}
		}
	}
	return added
}

func (p *seedLogProbe) noteAddedEvents(noteID string) int {
	p.t.Helper()
	var count int
	if err := p.db.QueryRow(`SELECT count(*) FROM bus_events WHERE name = 'garden.seed.note.added' AND json_extract(payload, '$.note_id') = ?`, noteID).Scan(&count); err != nil {
		p.t.Fatal(err)
	}
	return count
}

func (p *seedLogProbe) expect(change string, want ...wantEntry) {
	p.t.Helper()
	added := p.fresh()
	got := make([]wantEntry, len(added))
	for i, note := range added {
		got[i] = wantEntry{note.SeedID, note.Kind, note.Body, string(note.AuthorSession)}
		if n := p.noteAddedEvents(note.ID); n != 1 {
			p.t.Errorf("%s: entry %s (%q) has %d NoteAdded events, want one", change, note.ID, note.Body, n)
		}
	}
	order := func(a, b wantEntry) int { return strings.Compare(a.seed+a.body, b.seed+b.body) }
	slices.SortFunc(got, order)
	slices.SortFunc(want, order)
	if !slices.Equal(got, want) {
		p.t.Errorf("%s wrote %s, want %s", change, entriesText(got), entriesText(want))
	}
	if len(want) > 0 {
		encoded, _ := json.Marshal(added)
		p.t.Logf("%s: %s", change, encoded)
	} else {
		p.t.Logf("%s: no entries", change)
	}
}

func (p *seedLogProbe) expectOne(change, seed string) protocol.SeedNote {
	p.t.Helper()
	added := p.fresh()
	if len(added) != 1 || added[0].SeedID != seed {
		p.t.Fatalf("%s wrote %+v, want one entry on %s", change, added, seed)
	}
	if n := p.noteAddedEvents(added[0].ID); n != 1 {
		p.t.Errorf("%s: entry %s has %d NoteAdded events, want one", change, added[0].ID, n)
	}
	encoded, _ := json.Marshal(added[0])
	p.t.Logf("%s: %s", change, encoded)
	return added[0]
}

func entriesText(entries []wantEntry) string {
	lines := make([]string, len(entries))
	for i, entry := range entries {
		lines[i] = fmt.Sprintf("%s %s %q by %q", entry.seed, entry.kind, entry.body, entry.author)
	}
	return "[" + strings.Join(lines, "; ") + "]"
}
