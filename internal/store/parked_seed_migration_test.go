package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
)

func TestMigrationNamesPausedSeedsParked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1793572036186130-1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DefineDocumentCollection(garden.SeedsSchema(), time.Now()); err != nil {
		t.Fatal(err)
	}
	stored, _, err := s.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
	if err != nil {
		t.Fatal(err)
	}
	schema := *stored
	view, err := s.ProfileMigration()
	if err != nil {
		t.Fatal(err)
	}
	for id, status := range map[string]string{"s-pause1": "dormant", "s-open01": "planted"} {
		body, err := garden.Seed{ID: id, Title: id, Body: "keep this", Status: status, ProfileID: view.Manifest.ProfileID}.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutDocument(schema, id, body, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	read := func(id string) (garden.Seed, int64, time.Time) {
		t.Helper()
		doc, found, err := s.GetDocument(schema, id)
		if err != nil || !found {
			t.Fatalf("read %s: %v found=%v", id, err, found)
		}
		seed, err := garden.Decode(doc.Body)
		if err != nil {
			t.Fatal(err)
		}
		return seed, doc.Rev, doc.UpdatedAt
	}
	_, pausedRev, pausedAt := read("s-pause1")
	_, openRev, _ := read("s-open01")

	if err := migrateDBThrough(s.db, path, 1793572036186130); err != nil {
		t.Fatal(err)
	}
	paused, migratedRev, migratedAt := read("s-pause1")
	if paused.Status != garden.StatusParked || paused.Body != "keep this" {
		t.Fatalf("the paused seed is %q with body %q, want parked and its body kept", paused.Status, paused.Body)
	}
	if migratedRev <= pausedRev {
		t.Fatalf("rev stayed at %d although the status changed; stale writes and resuming watchers decide by rev", migratedRev)
	}
	if !migratedAt.Equal(pausedAt) {
		t.Fatalf("updated_at moved from %s to %s; the rename is not a move, and the board, stale reviews and ages read updated_at", pausedAt, migratedAt)
	}
	if open, rev, _ := read("s-open01"); open.Status != garden.StatusPlanted || rev != openRev {
		t.Fatalf("the planted seed became %q at rev %d, want it untouched at rev %d", open.Status, rev, openRev)
	}

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigration1793572036186130(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if again, rev, _ := read("s-pause1"); again.Status != garden.StatusParked || rev != migratedRev {
		t.Fatalf("a second run left the seed %q at rev %d, want parked at rev %d", again.Status, rev, migratedRev)
	}
}
