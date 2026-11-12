package store

import (
	"path/filepath"
	"testing"
)

func TestPluginRunRouteMigrationKeepsLegacyOwnershipWithoutGuessingTheRoute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := OpenDBAtSchemaVersion(path, 1794488765062962)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"active", "closed", "no-driver"} {
		if _, err := db.Exec(`INSERT INTO sessions (id, user_label, directory, agent, closed_at) VALUES (?, '', '/shop', 'independent', ?)`, id, map[string]string{"closed": "2026-11-11T10:00:00Z"}[id]); err != nil {
			t.Fatal(err)
		}
		if id != "no-driver" {
			if _, err := db.Exec(`INSERT INTO session_runtime (session_id, agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq, agent_metadata, conversation_native_id) VALUES (?, 'independent-plugin', ?, 7, '{"native_id":"existing"}', 'existing')`, id, "run-"+id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec(`INSERT INTO inbox_delivery(address,outstanding_at) VALUES('session:active','2026-11-11T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var pending, outstanding string
	if err := s.db.QueryRow(`SELECT pending_input,outstanding_at FROM inbox_delivery WHERE address='session:active'`).Scan(&pending, &outstanding); err != nil {
		t.Fatal(err)
	}
	if pending != "" || outstanding != "2026-11-11T10:00:00Z" {
		t.Fatalf("legacy inbox pending=%q outstanding=%q", pending, outstanding)
	}
	for _, id := range []string{"active", "closed", "no-driver"} {
		var plugin, run, route, native string
		var seq int
		if err := s.db.QueryRow(`SELECT agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq, agent_driver_route, COALESCE((SELECT conversation_native_id FROM session_runtime WHERE session_id = id), '') FROM sessions_with_runtime WHERE id = ?`, id).Scan(&plugin, &run, &seq, &route, &native); err != nil {
			t.Fatal(err)
		}
		if route != "" {
			t.Fatalf("legacy %s route=%q, want unknown until its owner registers", id, route)
		}
		if id != "no-driver" && (plugin != "independent-plugin" || run != "run-"+id || seq != 7 || native != "existing") {
			t.Fatalf("legacy ownership changed: %s %s %d %s", plugin, run, seq, native)
		}
	}
}
