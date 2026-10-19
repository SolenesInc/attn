package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

func TestOutpostsRetirementKeepsSessionsAndRetiresRemoteDispatches(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(dbPath, 1791676859283007)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTable(t, s, "endpoints") {
		t.Fatal("database before the retirement has no endpoints table")
	}
	for _, query := range []string{
		`INSERT INTO endpoints(id, name, ssh_target, enabled, instance, created_at, updated_at) VALUES ('endpoint-1', 'box', 'user@box', 1, '', 'now', 'now')`,
		`INSERT INTO sessions(id, label, directory, state, state_since, state_updated_at, last_seen, endpoint_id) VALUES ('session-1', 'Preserved', '/tmp', 'idle', 'now', 'now', 'now', 'endpoint-1')`,
	} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	schema := docstore.CollectionSchema{Namespace: "core/garden", Collection: "dispatches"}
	if _, err := s.DefineDocumentCollection(schema, time.Now()); err != nil {
		t.Fatal(err)
	}
	dispatches, _, err := s.DocumentCollection(schema.Namespace, schema.Collection)
	if err != nil {
		t.Fatal(err)
	}
	for id, body := range map[string]string{
		"remote": `{"session_id":"remote","crown":"s-1","cwd":"/srv/app","agent":"claude","repository_root":"/srv/app","branch":"feat","resume":"conv-1","host_kind":"remote","endpoint_id":"endpoint-1"}`,
		"local":  `{"session_id":"local","crown":"s-2","cwd":"/work/app","agent":"codex","branch":"main","host_kind":"local"}`,
	} {
		if _, err := s.PutDocument(*dispatches, id, []byte(body), time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if hasTable(t, upgraded, "endpoints") {
		t.Fatal("endpoints table survived the upgrade")
	}
	if session := upgraded.Get("session-1"); session == nil || session.Label != "Preserved" {
		t.Fatalf("session after the upgrade = %+v, want Preserved", session)
	}
	for id, want := range map[string]string{
		"remote": `{"session_id":"remote","crown":"s-1","agent":"claude"}`,
		"local":  `{"session_id":"local","crown":"s-2","cwd":"/work/app","agent":"codex","branch":"main"}`,
	} {
		doc, found, err := upgraded.GetDocument(*dispatches, id)
		if err != nil || !found || string(doc.Body) != want {
			t.Fatalf("%s dispatch after the upgrade = %s (found=%t, err=%v), want %s", id, doc.Body, found, err, want)
		}
	}
}
