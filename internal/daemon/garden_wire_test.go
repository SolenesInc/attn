package daemon_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEverySeedMoveReachesTheAppAsOneGardenPush(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "gardener")
		w.advance(0)
		seen := len(lifeGardenPushes(app))
		pushed := func(move string) protocol.WebSocketEvent {
			t.Helper()
			w.advance(0)
			pushes := lifeGardenPushes(app)
			if len(pushes) != seen+1 {
				t.Fatalf("%s produced %d garden pushes, want exactly one", move, len(pushes)-seen)
			}
			seen = len(pushes)
			return pushes[seen-1]
		}

		plot := plantSeedAs(t, cli, "gardener", "the plot")
		if push := pushed("planting"); len(push.Seeds) != 1 || push.Seeds[0].ID != plot || protocol.Deref(push.Total) != 1 {
			t.Fatalf("the planting pushed %d seeds of %d, want the new seed alone", len(push.Seeds), protocol.Deref(push.Total))
		}
		planted, err := cli.SeedPlant("gardener", "live a life", "", plot, "")
		if err != nil {
			t.Fatal(err)
		}
		seed := planted.Seed.ID

		var statuses []string
		record := func(move string) protocol.Seed {
			t.Helper()
			push := pushed(move)
			if protocol.Deref(push.Total) != 2 {
				t.Fatalf("%s pushed a garden of %d, want 2", move, protocol.Deref(push.Total))
			}
			for _, s := range push.Seeds {
				if s.ID == seed {
					statuses = append(statuses, s.Status)
					return s
				}
			}
			t.Fatalf("%s pushed a garden without %s", move, seed)
			return protocol.Seed{}
		}
		record("planting inside the plot")

		if tended := lifeMove(t, cli, "gardener", seed, "tend", "", ""); tenderSessionOf(tended) != "gardener" || !tended.Claimed {
			t.Fatalf("tend did not claim the seed for the gardener: %+v", tended.Tender)
		}
		record("tend")
		if _, err := cli.SeedNote("gardener", seed, "found the seam in internal/daemon", "", false, nil); err != nil {
			t.Fatal(err)
		}
		if note := pushed("note"); len(note.Seeds) != 0 {
			t.Fatalf("the note pushed %v, want no changed seed", lifeSeedIDs(note.Seeds))
		}
		lifeMove(t, cli, "gardener", seed, "harvest", "shipped it", "")
		if harvested := record("harvest"); protocol.Deref(harvested.Reason) != "shipped it" || tenderSessionOf(harvested) != "" || tenderMemberOf(harvested) != "" {
			t.Fatalf("the harvest reached the app as %+v, want the reason recorded and the claim released", harvested)
		}
		lifeMove(t, cli, "gardener", seed, "replant", "", "")
		if replanted := record("replant"); replanted.Reason != nil {
			t.Fatalf("the replant reached the app still carrying reason %q", protocol.Deref(replanted.Reason))
		}
		lifeMove(t, cli, "gardener", seed, "wither", "nobody is picking this up", "")
		record("wither")

		if want := []string{"planted", "growing", "harvested", "planted", "withered"}; !slices.Equal(statuses, want) {
			t.Errorf("the app saw %v, want %v", statuses, want)
		}

		joined := w.App()
		if ids := lifeSeedIDs(joined.Initial.Seeds); !slices.Contains(ids, seed) || !slices.Contains(ids, plot) || protocol.Deref(joined.Initial.SeedsTotal) != 2 {
			t.Errorf("a newly connected app's initial state carries %v of %d, want the whole garden", ids, protocol.Deref(joined.Initial.SeedsTotal))
		}
	})
}

func TestALiveSeedClaimRefusesOthersUntilForcedOrParked(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	panes := spawnPanes(w, app, w.Path("first"), w.Path("second"), w.Path("departing"))
	first, second, departing := panes[0].session, panes[1].session, panes[2]

	t.Run("a member's claim is refused to a second session until it is parked", func(t *testing.T) {
		seed := plantSeedAs(t, cli, first, "contended")
		lifeMove(t, cli, first, seed, "tend", "", "trellis")
		if _, err := editSeedBody(t, cli, seed, "edited body"); err != nil {
			t.Fatal(err)
		}
		_, err := cli.SeedTransition(protocol.SessionID(second), seed, "tend", "", false, client.SeedTransitionOptions{})
		lifeRefusal(t, "a second tend", err, seed, "Trellis", "delegation chain", "--force")
		if still := lifeShow(t, cli, seed).Seed; tenderSessionOf(still) != "" || tenderMemberOf(still) != "trellis" {
			t.Fatalf("the refused claim changed the tender: %+v", still)
		}
		if _, err := cli.SeedTransition(protocol.SessionID(first), seed, "park", "", true, client.SeedTransitionOptions{}); err != nil {
			t.Fatal(err)
		}
		if taken := lifeMove(t, cli, second, seed, "tend", "", "alder"); tenderSessionOf(taken) != "" || tenderMemberOf(taken) != "alder" {
			t.Fatalf("a parked seed did not hand over: %+v", taken)
		}
	})

	t.Run("two simultaneous tends leave one tender and tell the loser who won", func(t *testing.T) {
		seed := plantSeedAs(t, cli, first, "raced for")
		type outcome struct {
			result *protocol.SeedTransitionResult
			err    error
		}
		outcomes := make(chan outcome, 2)
		var start sync.WaitGroup
		start.Add(1)
		for _, session := range []string{first, second} {
			racer := w.Client()
			go func() {
				start.Wait()
				result, err := racer.SeedTransition(protocol.SessionID(session), seed, "tend", "", false, client.SeedTransitionOptions{})
				outcomes <- outcome{result, err}
			}()
		}
		start.Done()
		var winners, refusals []string
		for range 2 {
			got := <-outcomes
			if got.err != nil {
				refusals = append(refusals, got.err.Error())
				continue
			}
			winners = append(winners, string(tenderSessionOf(got.result.Seed)))
		}
		if len(winners) != 1 || len(refusals) != 1 {
			t.Fatalf("two simultaneous tends produced winners %v and refusals %q, want one of each", winners, refusals)
		}
		if !strings.Contains(refusals[0], winners[0]) {
			t.Errorf("the loser was not told %s won:\n%s", winners[0], refusals[0])
		}
		if held := tenderSessionOf(lifeShow(t, cli, seed).Seed); string(held) != winners[0] {
			t.Errorf("seed show says %q tends it, but %q was told it won", held, winners[0])
		}
	})

	t.Run("a member's claim outlives the session that made it until forced", func(t *testing.T) {
		seed := plantSeedAs(t, cli, first, "member work")
		if claimed := lifeMove(t, cli, departing.session, seed, "tend", "", "alder"); tenderMemberOf(claimed) != "alder" || tenderSessionOf(claimed) != "" {
			t.Fatalf("a member claim = member %q session %q", tenderMemberOf(claimed), tenderSessionOf(claimed))
		}
		closePane(app, departing)
		_, err := cli.SeedTransition(protocol.SessionID(second), seed, "tend", "", false, client.SeedTransitionOptions{})
		lifeRefusal(t, "a tend after the claiming session ended", err, "Alder")
		forced, err := cli.SeedTransition(protocol.SessionID(second), seed, "tend", "", true, client.SeedTransitionOptions{})
		if err != nil {
			t.Fatalf("a forced takeover of the member's claim: %v", err)
		}
		if got := forced.Seed; string(tenderSessionOf(got)) != second || tenderMemberOf(got) != "" {
			t.Errorf("the forced claim = member %q session %q, want the second session", tenderMemberOf(got), tenderSessionOf(got))
		}
	})

	for _, verb := range []string{"tend", "park", "harvest", "wither", "replant"} {
		t.Run("a forced "+verb+" records who forced and who held", func(t *testing.T) {
			seed := plantSeedAs(t, cli, first, "held work to "+verb)
			lifeMove(t, cli, first, seed, "tend", "", "")
			reason := ""
			if verb == "harvest" || verb == "wither" {
				reason = "done"
			}
			if verb == "wither" {
				_, err := cli.SeedTransition(protocol.SessionID(second), seed, verb, reason, false, client.SeedTransitionOptions{})
				lifeRefusal(t, "an unforced wither of a live claim", err, "--force")
			}
			forced, err := cli.SeedTransition(protocol.SessionID(second), seed, verb, reason, true, client.SeedTransitionOptions{})
			if err != nil {
				t.Fatalf("forced %s: %v", verb, err)
			}
			if verb == "wither" && (forced.Seed.Status != "withered" || tenderSessionOf(forced.Seed) != "") {
				t.Errorf("the forced wither left %s tended by %q, want it withered and released", forced.Seed.Status, tenderSessionOf(forced.Seed))
			}
			notes := lifeNoteBodies(t, cli, seed)
			if len(notes) != 2 || !slices.ContainsFunc(notes, func(note string) bool {
				return strings.HasPrefix(note, verb) && strings.Contains(note, second+" forced") && strings.Contains(note, first+" held")
			}) {
				t.Errorf("the log after a forced %s = %q, want its move entry naming who forced and who held", verb, notes)
			}
		})
	}
}

func TestASeedLogReadsNewestFirstAndSaysWhatItWithheld(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	writer := spawnPanes(w, app, w.Path("writer"))[0].session
	seed := plantSeedAs(t, cli, writer, "with a log")
	bodies := []string{"first", "second", "third", "fourth", "fifth", "sixth", "seventh"}
	for _, body := range bodies {
		if _, err := cli.SeedNote(protocol.SessionID(writer), seed, body, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}

	shown := lifeShow(t, cli, seed)
	if shown.NotesTotal != len(bodies) || len(shown.Notes) != garden.ShowNotes {
		t.Fatalf("show carries %d notes inline of %d, want %d of %d", len(shown.Notes), shown.NotesTotal, garden.ShowNotes, len(bodies))
	}
	if newest := shown.Notes[0]; newest.Body != "seventh" || string(authorSessionOf(newest)) != writer {
		t.Errorf("the log leads with %+v, want the newest note and who wrote it", newest)
	}
	all, err := cli.SeedNotes("", seed, client.SeedNotesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Notes) != len(bodies) || all.Total != len(bodies) {
		t.Errorf("the whole log is %d of %d, want all %d", len(all.Notes), all.Total, len(bodies))
	}
	elsewhere := plantSeedAs(t, cli, writer, "no log")
	if other := lifeShow(t, cli, elsewhere); len(other.Notes) != 0 || other.NotesTotal != 0 {
		t.Errorf("another seed shows %d notes of %d, want none", len(other.Notes), other.NotesTotal)
	}
}

func TestSeedRefusalsNameWhatIsWrongAndChangeNothing(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	gardener := spawnPanes(w, app, w.Path("gardener"))[0].session
	seed := plantSeedAs(t, cli, gardener, "refusals")
	before := lifeShow(t, cli, seed).Seed

	for _, refusal := range []struct {
		name  string
		err   func() error
		wants []string
	}{
		{"an unknown verb", func() error {
			_, err := cli.SeedTransition(protocol.SessionID(gardener), seed, "compost", "", false, client.SeedTransitionOptions{})
			return err
		}, []string{"harvest"}},
		{"a wordless harvest", func() error {
			_, err := cli.SeedTransition(protocol.SessionID(gardener), seed, "harvest", "", false, client.SeedTransitionOptions{})
			return err
		}, []string{"-m"}},
		{"a move on an unplanted seed", func() error {
			_, err := cli.SeedTransition(protocol.SessionID(gardener), "s-zzzzzz", "tend", "", false, client.SeedTransitionOptions{})
			return err
		}, []string{"s-zzzzzz"}},
		{"an empty note", func() error {
			_, err := cli.SeedNote("", seed, "  ", "", false, nil)
			return err
		}, []string{"attn seed note"}},
		{"a note on an unplanted seed", func() error {
			_, err := cli.SeedNote("", "s-zzzzzz", "into the void", "", false, nil)
			return err
		}, []string{"s-zzzzzz"}},
		{"an empty title", func() error {
			_, err := cli.SeedPlant("", "   ", "", "", "")
			return err
		}, []string{"attn seed plant"}},
		{"an over-long title", func() error {
			_, err := cli.SeedPlant("", strings.Repeat("x", garden.MaxTitleChars+1), "", "", "")
			return err
		}, []string{"401", "400"}},
		{"a note past the limit", func() error {
			_, err := cli.SeedNote("", seed, strings.Repeat("x", garden.MaxNoteBytes+1), "", false, nil)
			return err
		}, []string{strconv.Itoa(garden.MaxNoteBytes + 1)}},
		{"a malformed id", func() error {
			_, err := cli.SeedShow("", "nope")
			return err
		}, []string{"seed id"}},
		{"showing an unplanted seed", func() error {
			_, err := cli.SeedShow("", "s-zzzzzz")
			return err
		}, []string{"s-zzzzzz"}},
		{"editing an unplanted seed", func() error {
			_, err := cli.SeedEdit("s-ffffff", "words", "", "")
			return err
		}, []string{"s-ffffff"}},
	} {
		lifeRefusal(t, refusal.name, refusal.err(), refusal.wants...)
	}

	if after := lifeShow(t, cli, seed).Seed; !reflect.DeepEqual(after, before) {
		t.Errorf("the refusals changed the seed: before %+v, after %+v", before, after)
	}
	if listed, err := cli.SeedList("", false, 0); err != nil || listed.Total != 1 {
		t.Errorf("after the refusals the garden lists %+v (%v), want the one seed", listed, err)
	}
}

func TestEditingASeedChangesOnlyItsBody(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	editor := spawnPanes(w, app, w.Path("editor"))[0].session
	crown := plantSeedAs(t, cli, "", "Crown")
	planted, err := cli.SeedPlant(protocol.SessionID(editor), "Editable", "old body", crown, "")
	if err != nil {
		t.Fatal(err)
	}
	before := lifeMove(t, cli, editor, planted.Seed.ID, "tend", "", "")

	edited, err := editSeedBody(t, cli, before.ID, "# New body\n\nStill the same seed.")
	if err != nil {
		t.Fatal(err)
	}
	after := edited.Seed
	if after.Body != "# New body\n\nStill the same seed." || after.Rev != before.Rev+1 {
		t.Fatalf("edited body/revision = %q/%d, want the new body at revision %d", after.Body, after.Rev, before.Rev+1)
	}
	if after.ID != before.ID || after.Title != before.Title || after.Status != before.Status ||
		tenderSessionOf(after) != tenderSessionOf(before) || tenderMemberOf(after) != tenderMemberOf(before) ||
		!reflect.DeepEqual(after.Edges, before.Edges) || !reflect.DeepEqual(after.Vars, before.Vars) {
		t.Fatalf("the edit changed the seed's identity or lifecycle: before %+v, after %+v", before, after)
	}
	if shown := lifeShow(t, cli, before.ID).Seed; shown.Body != after.Body || shown.Rev != after.Rev {
		t.Errorf("seed show reads body %q at revision %d after the edit", shown.Body, shown.Rev)
	}

	_, err = editSeedBody(t, cli, before.ID, "")
	if err == nil {
		t.Fatal("an empty edit erased the body")
	}
	if got := lifeShow(t, cli, before.ID).Seed; got.Body != after.Body || got.Rev != after.Rev {
		t.Fatalf("empty edit changed the seed: %+v", got)
	}

}

func TestAPlantedSeedRoundTripsThroughListAndShow(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	planter := spawnPanes(w, app, w.Path("planter"))[0].session

	result, err := cli.SeedPlant(protocol.SessionID(planter), "Plant and see", "# slice 1\n\nthe first vertical", "", "")
	if err != nil {
		t.Fatal(err)
	}
	planted := result.Seed
	if err := garden.ValidateID(planted.ID); err != nil {
		t.Fatalf("plant returned an id that is not a seed id: %v", err)
	}
	if planted.Status != "planted" || planted.StepSlug != "plant-see" || string(planterSessionOf(planted)) != planter {
		t.Fatalf("the planted seed = %+v, want planted, slug plant-see, and its planter", planted)
	}
	sessionless := plantSeedAs(t, cli, "", "planted with no session at all")

	for _, from := range []string{planter, ""} {
		listed, err := cli.SeedList(protocol.SessionID(from), false, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := lifeSeedIDs(listed.Seeds); !slices.Equal(got, []string{sessionless, planted.ID}) || listed.Total != 2 {
			t.Errorf("ls from session %q = %v of %d, want the whole garden newest first", from, got, listed.Total)
		}
	}

	shown := lifeShow(t, cli, planted.ID).Seed
	if shown.Title != "Plant and see" || shown.Body != "# slice 1\n\nthe first vertical" || shown.Rev < 1 || shown.CreatedAt == "" {
		t.Errorf("show lost the seed: %+v", shown)
	}
	if shown.Edges == nil || shown.Vars == nil || shown.Template || shown.Gate {
		t.Errorf("the schema is not whole on a fresh seed: %+v", shown)
	}

	found, err := cli.SeedPlant("", "the follow-up", "", "", planted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if edges := found.Seed.Edges; len(edges) != 1 || edges[0].Kind != "discovered-from" || edges[0].To != planted.ID {
		t.Errorf("a seed planted discovered from another has edges %+v", edges)
	}
	_, err = cli.SeedPlant("", "must not land", "", "", "s-miss11")
	lifeRefusal(t, "planting from an unknown origin", err, "s-miss11")
	if listed, err := cli.SeedList("", false, 0); err != nil || listed.Total != 3 {
		t.Errorf("after the refused planting the garden holds %+v (%v), want 3 seeds", listed, err)
	}
}

func TestParkingASeedKeepsItsExecutionAndItsComment(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	worker := spawnPanes(w, app, w.Path("worker"))[0].session
	seed := plantSeedAs(t, cli, "", "work to put down")
	execution := protocol.Deref(lifeMove(t, cli, worker, seed, "tend", "", "").LastExecutionID)
	if execution == "" {
		t.Fatal("tending left the seed without an execution")
	}

	parked := lifeMove(t, cli, worker, seed, "park", "Waiting for the upstream API.", "")
	if parked.Status != "dormant" || tenderSessionOf(parked) != "" || protocol.Deref(parked.LastExecutionID) != execution {
		t.Errorf("the parked seed = %+v, want dormant, unclaimed, execution %s", parked, execution)
	}
	notes, err := cli.SeedNotes("", seed, client.SeedNotesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes.Notes) != 2 || !slices.ContainsFunc(notes.Notes, func(note protocol.SeedNote) bool {
		return note.Kind == garden.NoteKindMove && note.Body == "parked: Waiting for the upstream API." && string(authorSessionOf(note)) == worker
	}) {
		t.Errorf("the log after parking = %+v, want one park entry with the parker's comment", notes.Notes)
	}
}

func TestEverySeedASessionTendsRemembersWhereItRan(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	cwd := w.Path("shop")
	worker := w.Spawn(app, fakeagent.Claude, cwd)
	w.Launched(worker)
	plot, err := cli.SeedPlot("", protocol.SeedPlotMessage{
		Title: "the plot", Children: []protocol.SeedPlotChild{{Title: "first"}, {Title: "second"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, child := range plot.Children {
		lifeMove(t, cli, worker, child.ID, "tend", "", "")
	}
	for _, child := range plot.Children {
		seed := lifeShow(t, cli, child.ID).Seed
		continuation := seed.Continuation
		if string(protocol.Deref(seed.LastExecutionID)) != worker || continuation == nil || continuation.Cwd != cwd || continuation.Agent != "claude" {
			t.Errorf("%s remembers execution %q with continuation %+v, want %s running claude in %s",
				child.ID, protocol.Deref(seed.LastExecutionID), continuation, worker, cwd)
		}
	}
}

func TestASeedsStateClockMovesOnlyWithItsLifecycle(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "worker")
		planted, err := cli.SeedPlant("", "clocked work", "first body", "", "")
		if err != nil {
			t.Fatal(err)
		}
		plantedAt := time.Now()
		lifeClockAt(t, "planting", planted.Seed, plantedAt)

		w.advance(time.Second)
		edited, err := editSeedBody(t, cli, planted.Seed.ID, "a newer body")
		if err != nil {
			t.Fatal(err)
		}
		lifeClockAt(t, "a body edit", edited.Seed, plantedAt)

		w.advance(time.Second)
		lifeClockAt(t, "tending", lifeMove(t, cli, "worker", planted.Seed.ID, "tend", "", ""), time.Now())
	})
}

func TestASeedWrittenBeforeTheStateClockShowsItsLastUpdateAsInexact(t *testing.T) {
	w := newWorld(t)
	seed := plantSeedAs(t, w.Client(), "", "old work")
	w.stop()
	updated := time.Date(2026, 8, 22, 14, 30, 0, 0, time.UTC)
	older, err := store.NewWithDB(config.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	schema, found, err := older.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
	if err != nil || !found {
		t.Fatalf("the seeds collection = %v, %v", found, err)
	}
	doc, _, err := older.GetDocument(*schema, seed)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(doc.Body, &body); err != nil {
		t.Fatal(err)
	}
	delete(body, "state_changed_at")
	legacy, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := older.PutDocument(*schema, seed, legacy, updated, &doc.Rev); err != nil {
		t.Fatal(err)
	}
	if err := older.Close(); err != nil {
		t.Fatal(err)
	}
	w.start()

	shown := lifeShow(t, w.Client(), seed).Seed
	if shown.StateChangedAt != updated.Format(time.RFC3339Nano) || shown.StateChangedAtExact {
		t.Errorf("the legacy seed's state clock reads %q (exact=%t), want its last update %s marked inexact", shown.StateChangedAt, shown.StateChangedAtExact, updated.Format(time.RFC3339Nano))
	}
}

func lifeClockAt(t *testing.T, move string, seed protocol.Seed, want time.Time) {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, seed.StateChangedAt)
	if err != nil || !at.Equal(want) || !seed.StateChangedAtExact {
		t.Errorf("after %s the state clock reads %q (exact=%t), want exactly %s", move, seed.StateChangedAt, seed.StateChangedAtExact, want.UTC().Format(time.RFC3339Nano))
	}
}

func lifeGardenPushes(app *testworld.Peer) []protocol.WebSocketEvent {
	var pushes []protocol.WebSocketEvent
	for _, event := range app.Received() {
		if event.Event == protocol.EventGardenSeedsChanged {
			pushes = append(pushes, event)
		}
	}
	return pushes
}

func lifeMove(t *testing.T, cli *client.Client, session, seedID, verb, reason, assignee string) protocol.Seed {
	t.Helper()
	moved, err := cli.SeedTransition(protocol.SessionID(session), seedID, verb, reason, false, client.SeedTransitionOptions{Assignee: assignee})
	if err != nil {
		t.Fatalf("%s %s as %q: %v", verb, seedID, session, err)
	}
	return moved.Seed
}

func lifeShow(t *testing.T, cli *client.Client, seedID string) *protocol.SeedShowResult {
	t.Helper()
	shown, err := cli.SeedShow("", seedID)
	if err != nil {
		t.Fatalf("show %s: %v", seedID, err)
	}
	return shown
}

func lifeNoteBodies(t *testing.T, cli *client.Client, seedID string) []string {
	t.Helper()
	notes, err := cli.SeedNotes("", seedID, client.SeedNotesQuery{})
	if err != nil {
		t.Fatalf("notes of %s: %v", seedID, err)
	}
	bodies := make([]string, 0, len(notes.Notes))
	for _, note := range notes.Notes {
		bodies = append(bodies, note.Body)
	}
	return bodies
}

func lifeEntryBodies(t *testing.T, cli *client.Client, seedID, kind string) []string {
	t.Helper()
	notes, err := cli.SeedNotes("", seedID, client.SeedNotesQuery{Kind: kind})
	if err != nil {
		t.Fatalf("%s entries of %s: %v", kind, seedID, err)
	}
	bodies := []string{}
	for _, note := range notes.Notes {
		bodies = append(bodies, note.Body)
	}
	return bodies
}

func lifeRefusal(t *testing.T, what string, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s was accepted, want it refused", what)
		return
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal of %s does not name %q: %v", what, want, err)
		}
	}
}

func lifeSeedIDs(seeds []protocol.Seed) []string {
	ids := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		ids = append(ids, seed.ID)
	}
	return ids
}

func editSeedBody(t *testing.T, cli *client.Client, id, body string) (*protocol.SeedEditResult, error) {
	t.Helper()
	shown, err := cli.SeedShow("", id)
	if err != nil {
		return nil, err
	}
	return cli.SeedEdit(id, body, garden.BodySHA256(shown.Seed.Body), "")
}

func TestSeedMovesFollowTheChainAndChiefStandingOfAgentCloseAndTellTheDelegate(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		parent := w.bubbleClaude(t, app, "parent").id
		chief := w.bubbleClaude(t, app, "chief").id
		registerSessions(t, w, cli, "stranger")
		if result := setChiefOfStaff(app, chief, true); !result.Success {
			t.Fatalf("set chief: %+v", result)
		}
		delegate := func(caller, title string) (string, string) {
			t.Helper()
			seed := plantDelegationSeed(t, cli, caller, title)
			request := delegateAtSeed(caller, w.Path("parent"), seed)
			request.Agent = protocol.Ptr("claude")
			request.RequestID = "standing-" + seed
			started := make(chan struct{})
			w.terms.OnNextSpawn(func(*testworld.Terminal) { close(started) })
			accepted, err := cli.StartDelegation(request)
			if err != nil {
				t.Fatal(err)
			}
			<-started
			w.bootBubbleClaude(t, string(accepted.SessionID)).reply("Ready. <!-- attn:state=idle -->")
			result, err := cli.Delegate(request)
			if err != nil {
				t.Fatal(err)
			}
			return string(result.SessionID), seed
		}
		middle, _ := delegate(parent, "Coordinate the work")
		worker, seed := delegate(middle, "Complete the work")
		reset := func() {
			t.Helper()
			if _, err := cli.SeedTransition(protocol.SessionID(parent), seed, "replant", "", true, client.SeedTransitionOptions{}); err != nil {
				t.Fatal(err)
			}
			lifeMove(t, cli, worker, seed, "tend", "", "")
			w.advance(0)
			if _, err := cli.SeedShow(protocol.SessionID(worker), seed); err != nil {
				t.Fatal(err)
			}
		}
		for _, actor := range []struct{ id, standing string }{{middle, "delegation-chain standing"}, {parent, "delegation-chain standing"}, {chief, "chief standing"}} {
			for verb, status := range map[string]string{"tend": "growing", "park": "dormant", "harvest": "harvested", "wither": "withered", "replant": "planted"} {
				reset()
				reason := ""
				if verb == "harvest" || verb == "wither" {
					reason = "complete"
				}
				result, err := cli.SeedTransition(protocol.SessionID(actor.id), seed, verb, reason, false, client.SeedTransitionOptions{})
				if err != nil {
					t.Fatalf("%s %s without force: %v", actor.standing, verb, err)
				}
				if result.Seed.Status != status || result.Audit == nil {
					t.Fatalf("standing move: %+v", result)
				}
				notes := lifeNoteBodies(t, cli, seed)
				if len(notes) == 0 {
					t.Fatal("standing move left no audit note")
				}
				audit := result.Audit.Body
				if !slices.Contains(notes, audit) {
					t.Fatalf("returned audit is absent from log: %q", notes)
				}
				for _, want := range []string{actor.id, actor.standing, worker, "claim ended", "attn seed " + verb} {
					if !strings.Contains(audit, want) {
						t.Errorf("audit %q lacks %q", audit, want)
					}
				}
				if !slices.Contains(result.RingRecipients, worker) {
					t.Fatalf("delegate missing from recipients: %+v", result.RingRecipients)
				}
				w.advance(0)
				items := readInbox(t, cli, worker, 0).Items
				if len(items) != 1 || !strings.Contains(items[0].Content, seed+" moved:") {
					t.Fatalf("delegate inbox: %+v", items)
				}
			}
		}
		for _, actor := range []string{"stranger", ""} {
			for _, verb := range []string{"tend", "park", "harvest", "wither", "replant"} {
				reset()
				reason := ""
				if verb == "harvest" || verb == "wither" {
					reason = "complete"
				}
				_, err := cli.SeedTransition(protocol.SessionID(actor), seed, verb, reason, false, client.SeedTransitionOptions{})
				refusal := []string{"delegation chain", "--force"}
				if actor == "" && verb == "tend" {
					refusal = []string{"the user never claims a seed", "--for <name>"}
				}
				if err == nil || strings.Contains(err.Error(), "\n") || !strings.Contains(err.Error(), refusal[0]) || !strings.Contains(err.Error(), refusal[1]) {
					t.Fatalf("outside standing %q %s: %v", actor, verb, err)
				}
			}
		}
		result, err := cli.SeedTransition("stranger", seed, "harvest", "forced", true, client.SeedTransitionOptions{})
		if err != nil {
			t.Fatalf("forced move: %+v, %v", result, err)
		}
		forcedNotes := lifeNoteBodies(t, cli, seed)
		if result.Audit == nil || !slices.Contains(forcedNotes, result.Audit.Body) || !strings.Contains(result.Audit.Body, "stranger forced") || !strings.Contains(result.Audit.Body, worker) {
			t.Fatalf("forced audit: %q", forcedNotes)
		}
		if _, err := cli.CrewCreate("Trellis"); err != nil {
			t.Fatal(err)
		}
		memberSeed := plantSeedAs(t, cli, parent, "A crew member's claim")
		lifeMove(t, cli, "", memberSeed, "tend", "", "trellis")
		for _, actor := range []string{parent, middle, "stranger", ""} {
			_, err := cli.SeedTransition(protocol.SessionID(actor), memberSeed, "harvest", "complete", false, client.SeedTransitionOptions{})
			if err == nil || !strings.Contains(err.Error(), "Trellis") {
				t.Fatalf("a member claim yielded to %q: %v", actor, err)
			}
		}
		result, err = cli.SeedTransition(protocol.SessionID(chief), memberSeed, "harvest", "complete", false, client.SeedTransitionOptions{})
		if err != nil {
			t.Fatalf("the chief moves a member claim: %+v, %v", result, err)
		}
		chiefNotes := lifeNoteBodies(t, cli, memberSeed)
		if result.Audit == nil || !slices.Contains(chiefNotes, result.Audit.Body) || !strings.Contains(result.Audit.Body, "chief standing") || !strings.Contains(result.Audit.Body, "Trellis") {
			t.Fatalf("chief audit: %q", chiefNotes)
		}
	})
}
