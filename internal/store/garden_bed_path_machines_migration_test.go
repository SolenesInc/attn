package store

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
)

func TestGardenBedPathFenceKeepsExistingSharedNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garden.db")
	old, err := newStoreAtVersion(path, 1795566693854906-1)
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := mustCreateProfile(t, old, "Work")
	schema := garden.BedsSchema()
	if _, err := old.DefineDocumentCollection(schema, time.Now()); err != nil {
		t.Fatal(err)
	}
	declared, _, err := old.DocumentCollection(schema.Namespace, schema.Collection)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"id": "b-shared", "profile_id": profile.ID, "name": "Shared",
		"slug": "shared", "repos": []string{"billing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.PutDocument(*declared, "b-shared", body, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	doc, found, err := upgraded.GetDocument(*declared, "b-shared")
	if err != nil || !found {
		t.Fatalf("read upgraded bed: found %v, error %v", found, err)
	}
	bed, err := garden.DecodeBed(doc.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(bed.Names, []string{"billing"}) || len(bed.Paths) != 0 {
		t.Fatalf("upgraded bed repositories = %+v, want a shared billing name", bed.BedRepositories)
	}
}
