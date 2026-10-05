package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func desktopRows(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT id || '|' || profile_id || '|' || tree_json || '|' || active_pane_id FROM desktops ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	return out
}

func TestHistoricalProfileDatabasesPreserveDesktopsAndUsageOnUpgrade(t *testing.T) {
	for _, start := range []struct {
		name   string
		schema int
	}{
		{"an install at 153", 153},
		{"a desktops install at 162", 162},
		{"a production desktops install at 163", 163},
		{"an install at 164", 164},
		{"an install at 165", 165},
		{"a desktops install at 167", 167},
		{"a desktops install at 168", 168},
	} {
		t.Run(start.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "attn.db")
			db, err := OpenDBAtSchemaVersion(path, start.schema)
			if err != nil {
				t.Fatalf("OpenDBAtSchemaVersion: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO sessions (id, label, directory, state_since, state_updated_at, last_seen, session_cost_json)
				VALUES ('sol', 'sol', '/fixture', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z', ?)`, legacySolCost); err != nil {
				t.Fatal(err)
			}
			desktops := []string(nil)
			if start.schema >= 158 {
				desktops = desktopRows(t, db)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			var s *Store
			var upgrade SchemaUpgrade
			err = withMigrationsThrough(171, func() error { var err error; s, upgrade, err = Open(path); return err })
			if err != nil {
				t.Fatalf("upgrade from %d: %v", start.schema, err)
			}
			t.Cleanup(func() { s.Close() })
			if upgrade.From != start.schema || upgrade.To != LatestSchemaVersion() {
				t.Fatalf("upgrade = %+v, want %d -> %d", upgrade, start.schema, LatestSchemaVersion())
			}
			if desktops != nil {
				if got := desktopRows(t, s.db); !reflect.DeepEqual(got, desktops) {
					t.Fatalf("desktops changed from %v to %v, want existing desktops preserved", desktops, got)
				}
			}
			if _, err := s.db.Exec(`INSERT INTO kept_conversations (resume_id, agent, source_path, bytes, stored_bytes, copied_at) VALUES ('kept', 'codex', '/fixture', 1, 1, 'now')`); err != nil {
				t.Fatalf("kept conversations after upgrade: %v", err)
			}
			state, err := s.SessionCost("sol")
			if err != nil {
				t.Fatal(err)
			}
			if start.schema < 163 && len(state.Ledger) != 2 {
				t.Fatalf("ledger = %+v, want the Sol observations filed under a standard and a long-context row", state.Ledger)
			}
		})
	}
}

const legacySolCost = `{"initialized":true,
	"ledger":{"agent|gpt-6.1-sol":{"input_tokens":144001,"output_tokens":20000,"cache_read_input_tokens":400000}},
	"observations":{
		"codex:1":{"observation_id":"codex:1","model":"gpt-6.1-sol","purpose":"agent","usage":{"input_tokens":72000,"output_tokens":10000,"cache_read_input_tokens":200000}},
		"codex:2":{"observation_id":"codex:2","model":"gpt-6.1-sol","purpose":"agent","usage":{"input_tokens":72001,"output_tokens":10000,"cache_read_input_tokens":200000}}}}`
