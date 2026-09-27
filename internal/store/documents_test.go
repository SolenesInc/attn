package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

func requestsDeclaration() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Fields: []docstore.FieldSpec{
			{Name: "status", Type: docstore.FieldString},
			{Name: "attempts", Type: docstore.FieldNumber},
			{Name: "urgent", Type: docstore.FieldBool},
		},
	}
}

func storeWithRequests(t *testing.T, bodies map[string]string) (*Store, time.Time) {
	t.Helper()
	s := New()
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	if _, err := s.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("define: %v", err)
	}
	i := 0
	for _, id := range sortedKeys(bodies) {
		if _, err := s.PutDocument(declOf(t, s, "app/approval-gate", "requests"), id, []byte(bodies[id]), base.Add(time.Duration(i)*time.Second), nil); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
		i++
	}
	return s, base
}

func declOf(t *testing.T, s *Store, namespace, collection string) docstore.CollectionSchema {
	t.Helper()
	schema, ok, err := s.DocumentCollection(namespace, collection)
	if err != nil || !ok {
		t.Fatalf("declaration for %s/%s: ok=%v err=%v", namespace, collection, ok, err)
	}
	return *schema
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func queryIDs(t *testing.T, s *Store, q docstore.Query) []string {
	t.Helper()
	schema, ok, err := s.DocumentCollection(q.Namespace, q.Collection)
	if err != nil || !ok {
		t.Fatalf("declaration for %s/%s: ok=%v err=%v", q.Namespace, q.Collection, ok, err)
	}
	var anchor *docstore.Document
	if q.After != "" {
		doc, found, err := s.GetDocument(*schema, q.After)
		if err != nil {
			t.Fatalf("anchor %s: %v", q.After, err)
		}
		if found {
			anchor = doc
		}
	}
	c, err := q.Compile(*schema, anchor)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	docs, err := s.queryDocuments(c)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	return ids
}

func rev(n int64) *int64 { return &n }

func requestsDecl(t *testing.T, s *Store) docstore.CollectionSchema {
	t.Helper()
	return declOf(t, s, "app/approval-gate", "requests")
}

func seedV88DocumentStore(t *testing.T, dbPath string) {
	t.Helper()
	db, err := openSeededDB(dbPath)
	if err != nil {
		t.Fatalf("open for seeding: %v", err)
	}
	if _, err := db.Exec(`
		DROP TABLE document_collections;
		CREATE TABLE documents (
		    namespace  TEXT NOT NULL,
		    collection TEXT NOT NULL,
		    id         TEXT NOT NULL,
		    body       TEXT NOT NULL,
		    created_at TEXT NOT NULL,
		    updated_at TEXT NOT NULL,
		    PRIMARY KEY (namespace, collection, id)
		);
		CREATE TABLE document_collections (
		    namespace   TEXT NOT NULL,
		    collection  TEXT NOT NULL,
		    fields_json TEXT NOT NULL,
		    updated_at  TEXT NOT NULL,
		    PRIMARY KEY (namespace, collection)
		);
		INSERT INTO document_collections VALUES
		    ('app/approval-gate', 'requests',
		     '[{"name":"status","type":"string"},{"name":"attempts","type":"number"}]',
		     '2026-08-02T09:00:00Z'),
		    ('app/notes', 'scratch', '[]', '2026-08-02T09:30:00Z');
		INSERT INTO documents VALUES
		    ('app/approval-gate', 'requests', 'r1', '{"status":"open","attempts":2,"note":"kept"}',
		     '2026-08-02T10:00:00Z', '2026-08-02T10:00:00Z'),
		    ('app/approval-gate', 'requests', 'r2', '{"status":"done","attempts":"10"}',
		     '2026-08-02T10:01:00Z', '2026-08-02T10:05:00Z'),
		    ('app/approval-gate', 'requests', 'r3', '{"status":"open","attempts":7}',
		     '2026-08-02T10:02:00Z', '2026-08-02T10:02:00Z'),
		    ('app/notes', 'scratch', 'n1', '{"anything":true}',
		     '2026-08-02T11:00:00Z', '2026-08-02T11:00:00Z'),
		    ('app/ghost', 'lost', 'g1', '{"orphaned":true}',
		     '2026-08-02T12:00:00Z', '2026-08-02T12:00:00Z');
		DELETE FROM schema_migrations WHERE version >= 89;
	`); err != nil {
		t.Fatalf("seed v88 document store: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded database: %v", err)
	}
}

func TestAPopulatedV88StoreIsCarriedIntoItsOwnTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-89.db")
	seedV88DocumentStore(t, dbPath)

	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()

	schema := declOf(t, s, "app/approval-gate", "requests")
	if len(schema.Fields) != 2 || schema.Fields[0].Name != "status" || schema.Fields[1].Type != docstore.FieldNumber {
		t.Fatalf("declaration did not survive: %+v", schema.Fields)
	}

	doc, found, err := s.GetDocument(schema, "r1")
	if err != nil || !found {
		t.Fatalf("r1 after migration: found=%v err=%v", found, err)
	}
	if string(doc.Body) != `{"status":"open","attempts":2,"note":"kept"}` {
		t.Fatalf("body was rewritten: %s", doc.Body)
	}
	want := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	if !doc.CreatedAt.Equal(want) || !doc.UpdatedAt.Equal(want) {
		t.Fatalf("timestamps moved: created=%s updated=%s", doc.CreatedAt, doc.UpdatedAt)
	}

	q := docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Filters: []docstore.Filter{{Field: "status", Op: docstore.OpEq, Value: "open"}},
		Sort:    &docstore.Sort{Field: "attempts"},
	}
	if got := queryIDs(t, s, q); strings.Join(got, ",") != "r1,r3" {
		t.Fatalf("query over carried documents = %v, want [r1 r3]", got)
	}
	c, err := q.Compile(schema, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	plan, err := s.QueryPlan(c)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if joined := strings.Join(plan, " | "); !strings.Contains(joined, "INDEX") {
		t.Fatalf("carried collection has no index: %s", joined)
	}

	notes := declOf(t, s, "app/notes", "scratch")
	if _, found, err := s.GetDocument(notes, "n1"); err != nil || !found {
		t.Fatalf("n1 after migration: found=%v err=%v", found, err)
	}

	ghost, ok, err := s.DocumentCollection("app/ghost", "lost")
	if err != nil || !ok {
		t.Fatalf("undeclared address was not carried: ok=%v err=%v", ok, err)
	}
	if len(ghost.Fields) != 0 {
		t.Fatalf("undeclared address arrived with fields: %+v", ghost.Fields)
	}
	if _, found, err := s.GetDocument(*ghost, "g1"); err != nil || !found {
		t.Fatalf("g1 after migration: found=%v err=%v", found, err)
	}

	if collections, err := s.ListDocumentCollections(); err != nil || len(collections) != 3 {
		t.Fatalf("collections after migration = %d (err=%v), want 3", len(collections), err)
	}
	if _, err := s.db.Exec(`SELECT 1 FROM documents LIMIT 1`); err == nil {
		t.Fatal("the shared v88 table is still there")
	}
}

func seedPreRevisionDocuments(t *testing.T, dbPath string) {
	t.Helper()
	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatalf("open for seeding: %v", err)
	}
	base := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	if _, err := s.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("seed declaration: %v", err)
	}
	schema := declOf(t, s, "app/approval-gate", "requests")
	for _, doc := range []struct{ id, body string }{
		{"r1", `{"status":"open","attempts":2}`},
		{"r2", `{"status":"done","attempts":9}`},
	} {
		if _, err := s.PutDocument(schema, doc.id, []byte(doc.body), base, nil); err != nil {
			t.Fatalf("seed %s: %v", doc.id, err)
		}
	}
	if _, err := s.db.Exec(`ALTER TABLE ` + schema.Table + ` DROP COLUMN rev;
		DELETE FROM schema_migrations WHERE version >= 90;`); err != nil {
		t.Fatalf("rewind to migration 89: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close seeded database: %v", err)
	}
}

func TestDocumentsStoredBeforeRevisionsGetTheFirstOne(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-90.db")
	seedPreRevisionDocuments(t, dbPath)

	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()

	schema := declOf(t, s, "app/approval-gate", "requests")
	doc, found, err := s.GetDocument(schema, "r1")
	if err != nil || !found {
		t.Fatalf("r1 after migration: found=%v err=%v", found, err)
	}
	if doc.Rev != docstore.FirstRev {
		t.Fatalf("a carried document is at rev %d, want %d", doc.Rev, docstore.FirstRev)
	}
	if string(doc.Body) != `{"status":"open","attempts":2}` {
		t.Fatalf("the migration rewrote a body: %s", doc.Body)
	}

	next, err := s.PutDocument(schema, "r1", []byte(`{"status":"closed","attempts":2}`),
		time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC), rev(doc.Rev))
	if err != nil {
		t.Fatalf("conditional write against a carried revision: %v", err)
	}
	if next != docstore.FirstRev+1 {
		t.Fatalf("write returned rev %d, want %d", next, docstore.FirstRev+1)
	}
	if _, err := s.PutDocument(schema, "r1", []byte(`{"status":"stale"}`),
		time.Date(2026, 8, 4, 9, 1, 0, 0, time.UTC), rev(doc.Rev)); !docstore.IsConflict(err) {
		t.Fatalf("a second write at the carried revision returned %v, want a conflict", err)
	}

	if got := queryIDs(t, s, docstore.Query{Namespace: "app/approval-gate", Collection: "requests"}); len(got) != 2 {
		t.Fatalf("documents after migration = %v, want both", got)
	}
}
