package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
)

func TestUpgradedGardenIndexesParkedSeedsAsParked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1793572036186130-1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineDocumentCollection(garden.SeedsSchema(), time.Now()); err != nil {
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
	for id, status := range map[string]string{"s-pause1": "dormant", "s-open01": "planted"} {
		body, err := garden.Seed{ID: id, Title: "Malachite " + id, Body: "keep this", Status: status, ProfileID: view.Manifest.ProfileID}.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutDocument(*stored, id, body, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateDBThrough(s.db, path, 1793572036186130); err != nil {
		t.Fatal(err)
	}
	before, found, err := s.GetDocument(*stored, "s-pause1")
	if err != nil || !found {
		t.Fatalf("read s-pause1: %v found=%v", err, found)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DefineDocumentCollection(garden.SeedsSchema(), time.Now()); err != nil {
		t.Fatal(err)
	}
	rows, err := s.MatchText(garden.SeedsSchema(), docstore.Words("malachite"), "status")
	if err != nil {
		t.Fatal(err)
	}
	indexed := map[string]string{}
	for _, row := range rows {
		indexed[row.ID] = row.Key
	}
	if indexed["s-pause1"] != garden.StatusParked || indexed["s-open01"] != garden.StatusPlanted || len(indexed) != 2 {
		t.Fatalf("indexed statuses %v, want s-pause1 parked and s-open01 planted", indexed)
	}
	after, _, err := s.GetDocument(*stored, "s-pause1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Rev != before.Rev || !after.UpdatedAt.Equal(before.UpdatedAt) || string(after.Body) != string(before.Body) {
		t.Fatalf("repairing the index changed the seed: rev %d→%d, updated_at %s→%s", before.Rev, after.Rev, before.UpdatedAt, after.UpdatedAt)
	}
}
