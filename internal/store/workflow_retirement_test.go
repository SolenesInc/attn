package store

import (
	"path/filepath"
	"testing"
)

func TestWorkflowRetirementPreservesSharedStateAcrossUpgradeAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1791301168635041)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO workflow_runs(run_id, script_path, script_hash, status, created_at, updated_at) VALUES ('obsolete', '/tmp/obsolete.js', 'hash', 'running', 'now', 'now')`,
		`INSERT INTO workflow_agent_calls(run_id, ordinal, status) VALUES ('obsolete', '1', 'running')`,
		`INSERT INTO settings(key, value) VALUES ('workflows_enabled', 'true'), ('queue_mode_enabled', 'true')`,
		`INSERT INTO sessions(id, label, directory, state, state_since, state_updated_at, last_seen) VALUES ('preserved', 'Kept session', '/tmp', 'idle', 'now', 'now', 'now')`,
		`INSERT INTO bus_consumers(name, cursor, filter, enabled, updated_at) VALUES ('garden-seed-bells', 11, 'garden.*', 1, 'now')`,
	} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for restart := 0; restart < 2; restart++ {
		upgraded, err := NewWithDB(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"workflow_runs", "workflow_agent_calls"} {
			if hasTable(t, upgraded, table) {
				t.Errorf("retired table %s survived restart %d", table, restart)
			}
		}
		for query, want := range map[string]int{
			`SELECT COUNT(*) FROM settings WHERE key='workflows_enabled'`:                     0,
			`SELECT COUNT(*) FROM settings WHERE key='queue_mode_enabled' AND value='true'`:   1,
			`SELECT COUNT(*) FROM sessions WHERE id='preserved' AND label='Kept session'`:     1,
			`SELECT COUNT(*) FROM bus_consumers WHERE name='garden-seed-bells' AND cursor=11`: 1,
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
