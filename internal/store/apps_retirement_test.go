package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

func TestAppsRetirementKeepsCoreStateAcrossUpgradeAndRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ownedTables := []string{"apps", "app_versions", "app_invocations", "app_serving_steps", "app_reconcile_requests", "app_reconcile_progress"}
	for _, table := range ownedTables {
		if hasTable(t, s, table) {
			t.Fatalf("fresh database created retired table %s", table)
		}
	}

	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	appDoc := docstore.CollectionSchema{Namespace: "app/obsolete", Collection: "state"}
	coreDoc := docstore.CollectionSchema{Namespace: "core/preserved", Collection: "state"}
	for _, schema := range []docstore.CollectionSchema{appDoc, coreDoc} {
		if _, err := s.DefineDocumentCollection(schema, now); err != nil {
			t.Fatal(err)
		}
		stored, found, err := s.DocumentCollection(schema.Namespace, schema.Collection)
		if err != nil || !found {
			t.Fatalf("read declared collection: found=%t err=%v", found, err)
		}
		if _, err := s.PutDocument(*stored, "one", []byte(`{"value":1}`), now, nil); err != nil {
			t.Fatal(err)
		}
	}
	var appCollectionID int64
	if err := s.db.QueryRow(`SELECT id FROM document_collections WHERE namespace='app/obsolete'`).Scan(&appCollectionID); err != nil {
		t.Fatal(err)
	}
	for _, table := range ownedTables {
		if _, err := s.db.Exec(`CREATE TABLE ` + table + ` (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`INSERT INTO bus_consumers(name, cursor, filter, enabled, updated_at) VALUES ('app:obsolete', 7, '*', 1, 'now'), ('garden-seed-bells', 11, 'garden.*', 1, 'now')`,
		`INSERT INTO supervised_parks(child, parked_at, restart_attempt, exit_at, exit_code, exit_signal, exit_error) VALUES ('runtime', 'now', 1, 'now', 1, '', ''), ('plugin:pi', 'now', 2, 'now', 2, '', '')`,
		`INSERT INTO sessions(id, label, directory, state, state_since, state_updated_at, last_seen, agent_driver_plugin_name) VALUES ('session-1', 'Preserved', '/tmp', 'idle', 'now', 'now', 'now', 'attn-pi')`,
		`INSERT INTO workspaces(id, title, directory, created_at) VALUES ('workspace-1', 'Preserved', '/tmp', 'now')`,
		`INSERT INTO notifications(id, kind, source_kind, created_at) VALUES ('app-notice', 'warning', 'app_runtime', 'now'), ('core-notice', 'warning', 'plugin', 'now')`,
		`DELETE FROM schema_migrations WHERE version = 157`,
	} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	for restart := 0; restart < 2; restart++ {
		upgraded, err := NewWithDB(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range append(ownedTables, docstore.TableName(appCollectionID)) {
			if hasTable(t, upgraded, table) {
				t.Errorf("retired table %s survived restart %d", table, restart)
			}
		}
		stored, found, err := upgraded.DocumentCollection(coreDoc.Namespace, coreDoc.Collection)
		if err != nil || !found {
			t.Fatalf("core collection after restart %d: found=%t err=%v", restart, found, err)
		}
		if doc, found, err := upgraded.GetDocument(*stored, "one"); err != nil || !found || string(doc.Body) != `{"value":1}` {
			t.Errorf("core document after restart %d = %+v, %t, %v", restart, doc, found, err)
		}
		if _, found, err := upgraded.DocumentCollection(appDoc.Namespace, appDoc.Collection); err != nil || found {
			t.Errorf("app collection after restart %d: found=%t err=%v", restart, found, err)
		}
		for query, want := range map[string]int{
			`SELECT COUNT(*) FROM bus_consumers WHERE name='garden-seed-bells' AND cursor=11`:           1,
			`SELECT COUNT(*) FROM bus_consumers WHERE name LIKE 'app:%'`:                                0,
			`SELECT COUNT(*) FROM supervised_parks WHERE child='plugin:pi'`:                             1,
			`SELECT COUNT(*) FROM supervised_parks WHERE child='runtime'`:                               0,
			`SELECT COUNT(*) FROM sessions WHERE id='session-1' AND agent_driver_plugin_name='attn-pi'`: 1,
			`SELECT COUNT(*) FROM workspaces WHERE id='workspace-1'`:                                    1,
			`SELECT COUNT(*) FROM notifications WHERE id='core-notice'`:                                 1,
			`SELECT COUNT(*) FROM notifications WHERE id='app-notice'`:                                  0,
		} {
			var got int
			if err := upgraded.db.QueryRow(query).Scan(&got); err != nil || got != want {
				t.Errorf("restart %d: %s = %d, %v; want %d", restart, query, got, err, want)
			}
		}
		if err := upgraded.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func hasTable(t *testing.T, s *Store, name string) bool {
	t.Helper()
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count != 0
}
