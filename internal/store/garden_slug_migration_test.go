package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
)

func TestMigration126RecomputesStoredSlugs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	if _, err := s.DefineDocumentCollection(garden.SeedsSchema(), time.Now()); err != nil {
		t.Fatalf("define seeds: %v", err)
	}
	schema, _, err := s.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
	if err != nil {
		t.Fatalf("read the seeds declaration: %v", err)
	}
	body := []byte(`{"id":"s-e5zefj","title":"Mermaid rendered in the grid, in Rust","body":"","status":"planted","step_slug":"mermaid-rendered-in-the-grid-in-rust","edges":[],"vars":[]}`)
	if _, err := s.PutDocument(*schema, "s-e5zefj", body, time.Now(), nil); err != nil {
		t.Fatalf("plant the old-slug seed: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 126`); err != nil {
		t.Fatalf("unrecord migration 126: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}

	doc, _, err := s.GetDocument(*schema, "s-e5zefj")
	if err != nil {
		t.Fatalf("read the seed back: %v", err)
	}
	var seed garden.Seed
	if err := json.Unmarshal(doc.Body, &seed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if seed.StepSlug != "mermaid-rendered-grid-rust" {
		t.Fatalf("step slug = %q after the migration, want the stop words gone", seed.StepSlug)
	}
}
