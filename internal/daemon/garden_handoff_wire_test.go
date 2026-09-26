package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAHandoffReachesTheNextTenderAndOnlyTheTender(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	panes := spawnPanes(w, app, w.Path("predecessor"), w.Path("successor"))
	predecessor, successor := panes[0], panes[1].session

	t.Run("the next tender is handed the handoff its predecessor left", func(t *testing.T) {
		seed := plantSeedAs(t, cli, predecessor.session, "carry this across sessions")
		gardenHandoffMove(t, cli, predecessor.session, seed, "tend", "")
		gardenHandoffNote(t, cli, predecessor.session, seed, "read the docstore compiler first", "trellis", "")
		left := gardenHandoffNote(t, cli, predecessor.session, seed, "the join test is the gate; run it before touching wireProjections", "trellis", garden.NoteKindHandoff)
		if left.Kind != garden.NoteKindHandoff {
			t.Fatalf("the note was stored as %q, want a handoff", left.Kind)
		}
		closePane(app, predecessor)

		got := gardenHandoffMove(t, cli, successor, seed, "tend", "").Handoff
		if got == nil || got.ID != left.ID || got.Body != left.Body || got.AuthorMember != "trellis" {
			t.Fatalf("the successor's tend carried %+v, want the handoff trellis left", got)
		}
		if shown := gardenHandoffShow(t, cli, seed); shown.Handoff == nil || shown.Handoff.ID != left.ID {
			t.Errorf("show surfaced %+v, want the handoff", shown.Handoff)
		}
	})

	t.Run("the freshest handoff survives a busy log", func(t *testing.T) {
		seed := plantSeedAs(t, cli, successor, "busy")
		gardenHandoffNote(t, cli, successor, seed, "the first word", "keel", garden.NoteKindHandoff)
		newest := gardenHandoffNote(t, cli, successor, seed, "the last word", "keel", garden.NoteKindHandoff)
		for range garden.ShowNotes + 2 {
			gardenHandoffNote(t, cli, successor, seed, "ordinary progress", "keel", "")
		}

		shown := gardenHandoffShow(t, cli, seed)
		if shown.Handoff == nil || shown.Handoff.ID != newest.ID {
			t.Fatalf("show surfaced %+v, want the freshest handoff %s", shown.Handoff, newest.ID)
		}
		if len(shown.Notes) != garden.ShowNotes || shown.NotesTotal != garden.ShowNotes+4 {
			t.Errorf("show rendered %d of %d notes, want %d of %d with the handoffs counted", len(shown.Notes), shown.NotesTotal, garden.ShowNotes, garden.ShowNotes+4)
		}
	})

	t.Run("only tend carries the handoff", func(t *testing.T) {
		seed := plantSeedAs(t, cli, successor, "settled")
		gardenHandoffNote(t, cli, successor, seed, "what I learned", "keel", garden.NoteKindHandoff)
		if tended := gardenHandoffMove(t, cli, successor, seed, "tend", ""); tended.Handoff == nil {
			t.Fatal("tend carried no handoff")
		}
		for _, move := range []struct{ verb, reason string }{{"park", ""}, {"harvest", "done"}, {"replant", ""}} {
			if moved := gardenHandoffMove(t, cli, successor, seed, move.verb, move.reason); moved.Handoff != nil {
				t.Errorf("%s carried a handoff; only the pickup primes", move.verb)
			}
		}
	})

	t.Run("plain notes are never a handoff", func(t *testing.T) {
		seed := plantSeedAs(t, cli, successor, "quiet")
		if plain := gardenHandoffNote(t, cli, successor, seed, "what happened", "keel", ""); plain.Kind != garden.NoteKindNote {
			t.Fatalf("an unkinded note was stored as %q", plain.Kind)
		}
		if shown := gardenHandoffShow(t, cli, seed); shown.Handoff != nil {
			t.Errorf("show invented a handoff: %+v", shown.Handoff)
		}
		if tended := gardenHandoffMove(t, cli, successor, seed, "tend", ""); tended.Handoff != nil {
			t.Errorf("tend invented a handoff: %+v", tended.Handoff)
		}
	})

	t.Run("an unknown note kind is refused listing every kind", func(t *testing.T) {
		seed := plantSeedAs(t, cli, successor, "kinds")
		_, err := cli.SeedNote(successor, seed, "words", "", "farewell", false, nil)
		if err == nil {
			t.Fatal("a note kind nothing knows about was written")
		}
		for _, want := range garden.NoteKinds {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal %q does not offer %q", err, want)
			}
		}
	})
}

func gardenHandoffNote(t *testing.T, cli *client.Client, session, seedID, body, member, kind string) protocol.SeedNote {
	t.Helper()
	result, err := cli.SeedNote(session, seedID, body, member, kind, false, nil)
	if err != nil {
		t.Fatalf("%s notes %q on %s: %v", session, body, seedID, err)
	}
	return result.Note
}

func gardenHandoffMove(t *testing.T, cli *client.Client, session, seedID, verb, reason string) *protocol.SeedTransitionResult {
	t.Helper()
	result, err := cli.SeedTransition(session, seedID, verb, reason, "", false, client.SeedTransitionOptions{})
	if err != nil {
		t.Fatalf("%s %ss %s: %v", session, verb, seedID, err)
	}
	return result
}

func gardenHandoffShow(t *testing.T, cli *client.Client, seedID string) *protocol.SeedShowResult {
	t.Helper()
	shown, err := cli.SeedShow("", seedID)
	if err != nil {
		t.Fatalf("show %s: %v", seedID, err)
	}
	return shown
}
