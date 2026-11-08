package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
)

const seedBodiesMigration = 1794152733866139

func TestUpgradeMovesSeedBodiesApartAndKeepsThemSearchable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, seedBodiesMigration-1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := garden.SeedsSchema()
	old.Text = []string{"title", "body"}
	if _, err := s.DefineDocumentCollection(old, time.Now()); err != nil {
		t.Fatal(err)
	}
	stored, _, err := s.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.ProfileMigration()
	if err != nil {
		t.Fatal(err)
	}
	body := "line \"one\"\n<tag> & émerald  obsidian"
	row := `{"id":"s-abc123","profile_id":"` + view.Manifest.ProfileID + `","title":"Malachite plan","status":"planted","step_slug":"malachite-plan","edges":[],"template":false,"gate":false,"vars":[],"harvest_when_pull_request":"","body":"line \"one\"\n<tag> & émerald  obsidian"}`
	if _, err := s.PutDocument(*stored, "s-abc123", []byte(row), time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	before, _, err := s.GetDocument(*stored, "s-abc123")
	if err != nil {
		t.Fatal(err)
	}

	if err := migrateDBThrough(s.db, path, seedBodiesMigration); err != nil {
		t.Fatal(err)
	}

	after, _, err := s.GetDocument(*stored, "s-abc123")
	if err != nil {
		t.Fatal(err)
	}
	seed, err := garden.Decode(after.Body)
	if err != nil || seed.Title != "Malachite plan" {
		t.Fatalf("the seed changed: %+v %v", seed, err)
	}
	if after.Rev != before.Rev || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("rev %d at %s, want %d at %s: moving a body is not a change to the seed", after.Rev, after.UpdatedAt, before.Rev, before.UpdatedAt)
	}
	bodies, found, err := s.DocumentCollection(garden.Namespace, garden.CollectionSeedBodies)
	if err != nil || !found {
		t.Fatalf("the body collection: %v found=%v", err, found)
	}
	moved, found, err := s.GetDocument(*bodies, "s-abc123")
	if err != nil || !found {
		t.Fatalf("the moved body: %v found=%v", err, found)
	}
	if got, err := garden.DecodeSeedBody(moved.Body); err != nil || got != body {
		t.Fatalf("body = %q, %v; want %q", got, err, body)
	}
	if !moved.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("the body document was created at %s, want the seed's %s", moved.CreatedAt, before.CreatedAt)
	}
	for schema, word := range map[*docstore.CollectionSchema]string{bodies: "obsidian", stored: "malachite"} {
		hits, err := s.MatchText(*schema, []string{word}, "")
		if err != nil || len(hits) != 1 || hits[0].ID != "s-abc123" {
			t.Fatalf("searching %s for %q: %+v %v", schema.Collection, word, hits, err)
		}
	}
	if hits, err := s.MatchText(garden.SeedsSchema(), []string{"obsidian"}, ""); err != nil || len(hits) != 0 {
		t.Fatalf("the seed index still holds the body: %+v %v", hits, err)
	}
}
