package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
)

func TestAnswerUpgradeGivesAskAnswersTheirOwnKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1793388869817138)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineDocumentCollection(garden.NotesSchema(), time.Now()); err != nil {
		t.Fatal(err)
	}
	declared, _, err := s.DocumentCollection(garden.Namespace, garden.CollectionNotes)
	if err != nil {
		t.Fatal(err)
	}
	schema := *declared
	notes := map[string]garden.Note{
		"n-answer": {ID: "n-answer", Seed: "s-aaaaaa", Kind: garden.NoteKindNote, Body: "🙋 The user answered the ask \"Cache\" (a-111111).\n\nAnswer: Postgres"},
		"n-quoted": {ID: "n-quoted", Seed: "s-aaaaaa", Kind: garden.NoteKindNote, Body: "🙋 The user answered the ask \"Cache\" (a-111111).", AuthorSession: "agent"},
		"n-system": {ID: "n-system", Seed: "s-aaaaaa", Kind: garden.NoteKindNote, Body: "The worktree was swept."},
	}
	revs := map[string]int64{}
	for id, note := range notes {
		body, err := note.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if revs[id], err = s.PutDocument(schema, id, body, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := map[string]string{"n-answer": garden.NoteKindAnswer, "n-quoted": garden.NoteKindNote, "n-system": garden.NoteKindNote}
	for id, kind := range want {
		doc, found, err := s.GetDocument(schema, id)
		if err != nil || !found {
			t.Fatalf("%s after the upgrade: found=%v %v", id, found, err)
		}
		note, err := garden.DecodeNote(doc.Body)
		if err != nil {
			t.Fatal(err)
		}
		if note.Kind != kind || note.Body != notes[id].Body {
			t.Errorf("%s is %q %q after the upgrade, want kind %q and its body unchanged", id, note.Kind, note.Body, kind)
		}
		if changed := doc.Rev != revs[id]; changed != (kind == garden.NoteKindAnswer) {
			t.Errorf("%s rev %d → %d; only a converted answer moves its rev", id, revs[id], doc.Rev)
		}
	}
	matches, err := s.MatchText(schema, []string{"answered"}, "kind")
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range matches {
		if match.Key != want[match.ID] {
			t.Errorf("search reads %s as kind %q, want %q", match.ID, match.Key, want[match.ID])
		}
	}
	if len(matches) != 2 {
		t.Errorf("search found %d answered notes, want 2", len(matches))
	}
}
