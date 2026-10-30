package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
)

func TestDocumentTextMigrationIndexesExistingGarden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	db, err := openDBAtVersion(path, 1793109271851966)
	if err != nil {
		t.Fatal(err)
	}
	for i, schema := range []docstore.CollectionSchema{garden.SeedsSchema(), garden.NotesSchema()} {
		fields, _ := json.Marshal(schema.Fields)
		if _, err := db.Exec(`INSERT INTO document_collections(id,namespace,collection,fields_json,updated_at) VALUES(?,?,?,?,?)`, i+1, schema.Namespace, schema.Collection, string(fields), "2026-10-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := createCollectionTable(tx, docstore.TableName(int64(i+1)), schema.Fields); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct{ table, id, body string }{
		{"doc_1", "s-oldold", `{"id":"s-oldold","title":"Existing work","body":"Obsidian from before the upgrade","status":"harvested","profile_id":"old-profile"}`},
		{"doc_2", "n-oldold", `{"seed":"s-oldold","body":"Chrysoprase in the old log"}`},
	} {
		if _, err := db.Exec(`INSERT INTO `+row.table+`(id,body,rev,created_at,updated_at) VALUES(?,?,1,?,?)`, row.id, row.body, "2026-10-01T00:00:00Z", "2026-10-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		schema        docstore.CollectionSchema
		word, id, key string
	}{
		{garden.SeedsSchema(), "obsidian", "s-oldold", ""},
		{garden.NotesSchema(), "chrysoprase", "n-oldold", "seed"},
	} {
		if _, err := s.DefineDocumentCollection(tc.schema, time.Now()); err != nil {
			t.Fatal(err)
		}
		rows, err := s.MatchText(tc.schema, docstore.Words(tc.word), tc.key)
		if err != nil || len(rows) != 1 || rows[0].ID != tc.id {
			t.Fatalf("upgraded %s: %+v %v", tc.word, rows, err)
		}
		if tc.key != "" && rows[0].Key != "s-oldold" {
			t.Errorf("log lost its seed: %+v", rows)
		}
	}
}
