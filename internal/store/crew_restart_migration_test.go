package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

func TestCrewRestartRemovalPreservesMemberAndOrdinaryInbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1791667794877794)
	if err != nil {
		t.Fatal(err)
	}
	for collection, body := range map[string]string{
		"members":          `{"id":"trellis","binding_session":"day","letter_path":"handoffs/letter.md","letter_session":"day","restart":{"state":"queued"}}`,
		"restart_requests": `{"request_id":"restart"}`,
	} {
		schema := docstore.CollectionSchema{Namespace: "core/crew", Collection: collection}
		if _, err := s.DefineDocumentCollection(schema, time.Now()); err != nil {
			t.Fatal(err)
		}
		stored, _, err := s.DocumentCollection(schema.Namespace, schema.Collection)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutDocument(*stored, "trellis", []byte(body), time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO inbox_items(id,address,kind,coalesce_key,text,created_at) VALUES
		('restart','session:day','notice','crew-restart-trellis','nap','now'),
		('sleep','session:day','notice','','sleep','now')`); err != nil {
		t.Fatal(err)
	}
	requests, _, err := s.DocumentCollection("core/crew", "restart_requests")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, found, err := s.DocumentCollection("core/crew", "restart_requests"); err != nil || found || hasTable(t, s, requests.Table) {
		t.Fatalf("restart requests survived: found=%t err=%v", found, err)
	}
	members, _, err := s.DocumentCollection("core/crew", "members")
	if err != nil {
		t.Fatal(err)
	}
	doc, found, err := s.GetDocument(*members, "trellis")
	want := `{"id":"trellis","binding_session":"day","letter_session":"day","letter_file":"letter.md","home_dir":"trellis"}`
	if err != nil || !found || string(doc.Body) != want {
		t.Fatalf("member after upgrade = %s, found=%t err=%v", doc.Body, found, err)
	}
	var remaining string
	if err := s.db.QueryRow(`SELECT group_concat(id) FROM inbox_items`).Scan(&remaining); err != nil || remaining != "sleep" {
		t.Fatalf("inbox after upgrade = %q, err=%v", remaining, err)
	}
}
