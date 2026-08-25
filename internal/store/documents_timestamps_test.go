package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

// created_at and updated_at are TEXT columns, so every sort and every "changed
// since" filter on them is a text comparison.

var raggedSeconds = []struct {
	id     string
	offset time.Duration
}{
	{"t0", 0},
	{"t1234", 123400 * time.Microsecond},
	{"t12345", 123450 * time.Microsecond},
	{"t5", 500 * time.Millisecond},
}

func storeWithRaggedStamps(t *testing.T) (*Store, docstore.CollectionSchema, time.Time) {
	t.Helper()
	s := New()
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	if _, err := s.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("define: %v", err)
	}
	schema := declOf(t, s, "app/approval-gate", "requests")
	for _, r := range raggedSeconds {
		if _, err := s.PutDocument(schema, r.id, []byte(`{"status":"pending"}`), base.Add(r.offset), nil); err != nil {
			t.Fatalf("put %s: %v", r.id, err)
		}
	}
	return s, schema, base
}

func chronological() []string {
	out := make([]string, 0, len(raggedSeconds))
	for _, r := range raggedSeconds {
		out = append(out, r.id)
	}
	return out
}

func reversed(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[len(ids)-1-i] = id
	}
	return out
}

func sameOrder(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func stampQuery(sort *docstore.Sort, filters ...docstore.Filter) docstore.Query {
	return docstore.Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Sort:       sort,
		Filters:    filters,
	}
}

func TestSortingOnAStampOrdersDocumentsByWhenTheyWereWritten(t *testing.T) {
	s, _, _ := storeWithRaggedStamps(t)

	got, err := readIDs(t, s, stampQuery(&docstore.Sort{Field: docstore.FieldCreatedAt}))
	if err != nil {
		t.Fatal(err)
	}
	if want := chronological(); !sameOrder(got, want) {
		t.Fatalf("ascending by created_at gave %v, want %v", got, want)
	}

	got, err = readIDs(t, s, stampQuery(&docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true}))
	if err != nil {
		t.Fatal(err)
	}
	if want := reversed(chronological()); !sameOrder(got, want) {
		t.Fatalf("descending by created_at gave %v, want %v", got, want)
	}
}

func TestAChangedSinceFilterFindsEverythingChangedSince(t *testing.T) {
	s, _, base := storeWithRaggedStamps(t)

	got, err := readIDs(t, s, stampQuery(
		&docstore.Sort{Field: docstore.FieldUpdatedAt},
		docstore.Filter{Field: docstore.FieldUpdatedAt, Op: docstore.OpGte, Value: base.Format(time.RFC3339)},
	))
	if err != nil {
		t.Fatal(err)
	}
	if want := chronological(); !sameOrder(got, want) {
		t.Fatalf("updated_at >= the second boundary gave %v, want every document: %v", got, want)
	}

	got, err = readIDs(t, s, stampQuery(
		&docstore.Sort{Field: docstore.FieldUpdatedAt},
		docstore.Filter{Field: docstore.FieldUpdatedAt, Op: docstore.OpGt, Value: base},
	))
	if err != nil {
		t.Fatal(err)
	}
	if want := chronological()[1:]; !sameOrder(got, want) {
		t.Fatalf("updated_at > the first document's stamp gave %v, want %v", got, want)
	}
}

func TestASweepResumesFromTheStampItWasHandedBack(t *testing.T) {
	s, _, _ := storeWithRaggedStamps(t)

	read, found, err := s.ReadQuery(stampQuery(&docstore.Sort{Field: docstore.FieldUpdatedAt}))
	if err != nil || !found {
		t.Fatalf("first sweep: found=%v err=%v", found, err)
	}
	seen := read.Documents[:2]
	cursor := seen[len(seen)-1].UpdatedAt.UTC().Format(docstore.TimeFormat)

	got, err := readIDs(t, s, stampQuery(
		&docstore.Sort{Field: docstore.FieldUpdatedAt},
		docstore.Filter{Field: docstore.FieldUpdatedAt, Op: docstore.OpGt, Value: cursor},
	))
	if err != nil {
		t.Fatal(err)
	}
	if want := chronological()[2:]; !sameOrder(got, want) {
		t.Fatalf("resuming after %s gave %v, want %v", cursor, got, want)
	}
}

func TestABoundMeansTheSameInEveryRFC3339Spelling(t *testing.T) {
	s, _, _ := storeWithRaggedStamps(t)

	var first []string
	for _, form := range []string{
		"2026-08-05T10:00:00Z",
		"2026-08-05T10:00:00.0Z",
		"2026-08-05T10:00:00.000000000Z",
		"2026-08-05T12:00:00+02:00",
	} {
		got, err := readIDs(t, s, stampQuery(
			&docstore.Sort{Field: docstore.FieldCreatedAt},
			docstore.Filter{Field: docstore.FieldCreatedAt, Op: docstore.OpGte, Value: form},
		))
		if err != nil {
			t.Fatalf("%s: %v", form, err)
		}
		if first == nil {
			first = got
			if want := chronological(); !sameOrder(got, want) {
				t.Fatalf("%s matched %v, want %v", form, got, want)
			}
			continue
		}
		if !sameOrder(got, first) {
			t.Fatalf("%s matched %v, but the same instant written differently matched %v", form, got, first)
		}
	}
}

func TestAStampFilterRefusesAValueThatIsNotATimestamp(t *testing.T) {
	s, _, _ := storeWithRaggedStamps(t)

	got, err := readIDs(t, s, stampQuery(nil,
		docstore.Filter{Field: docstore.FieldUpdatedAt, Op: docstore.OpGt, Value: "last tuesday"}))
	if err == nil {
		t.Fatalf("a filter on updated_at accepted \"last tuesday\" and returned %v", got)
	}
}

func TestMigration91RewritesStampsThatDoNotSort(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	if _, err := s.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("define: %v", err)
	}
	schema := declOf(t, s, "app/approval-gate", "requests")
	for _, r := range raggedSeconds {
		if _, err := s.PutDocument(schema, r.id, []byte(`{"status":"pending"}`), base.Add(r.offset), nil); err != nil {
			t.Fatalf("put %s: %v", r.id, err)
		}
	}

	table := docstore.TableName(collectionID(t, s, "app/approval-gate", "requests"))
	for _, r := range raggedSeconds {
		old := base.Add(r.offset).Format(time.RFC3339Nano)
		if _, err := s.db.Exec(fmt.Sprintf(`UPDATE %s SET created_at = ?, updated_at = ? WHERE id = ?`, table),
			old, old, r.id); err != nil {
			t.Fatalf("plant old stamp for %s: %v", r.id, err)
		}
	}
	if _, err := s.db.Exec(`UPDATE document_collections SET updated_at = 'not a timestamp'`); err != nil {
		t.Fatalf("plant unreadable stamp: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 91`); err != nil {
		t.Fatalf("unrecord migration 91: %v", err)
	}

	if got, err := readIDs(t, s, stampQuery(&docstore.Sort{Field: docstore.FieldCreatedAt})); err != nil {
		t.Fatal(err)
	} else if sameOrder(got, chronological()) {
		t.Fatalf("the planted stamps already sort correctly as %v; this test would pass without the migration", got)
	}

	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}

	got, err := readIDs(t, s, stampQuery(&docstore.Sort{Field: docstore.FieldCreatedAt}))
	if err != nil {
		t.Fatal(err)
	}
	if want := chronological(); !sameOrder(got, want) {
		t.Fatalf("after migration 91 the stamps sort as %v, want %v", got, want)
	}

	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 91`); err != nil {
		t.Fatalf("unrecord migration 91 again: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("re-run migrateDB: %v", err)
	}
	got, err = readIDs(t, s, stampQuery(&docstore.Sort{Field: docstore.FieldCreatedAt}))
	if err != nil {
		t.Fatal(err)
	}
	if want := chronological(); !sameOrder(got, want) {
		t.Fatalf("re-running migration 91 left the stamps sorting as %v, want %v", got, want)
	}

	var unreadable string
	if err := s.db.QueryRow(`SELECT updated_at FROM document_collections`).Scan(&unreadable); err != nil {
		t.Fatal(err)
	}
	if unreadable != "not a timestamp" {
		t.Fatalf("the unreadable stamp became %q; it must be left as it was", unreadable)
	}
}

func collectionID(t *testing.T, s *Store, namespace, collection string) int64 {
	t.Helper()
	var id int64
	if err := s.db.QueryRow(
		`SELECT id FROM document_collections WHERE namespace = ? AND collection = ?`,
		namespace, collection).Scan(&id); err != nil {
		t.Fatalf("registry id for %s/%s: %v", namespace, collection, err)
	}
	return id
}
