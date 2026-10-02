package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestADatabaseFromAnEarlyDesktopsBuildIsRefusedWithItsCauseAndLeftAsItWas(t *testing.T) {
	for _, ladder := range []struct {
		name     string
		recorded int
		shape    []string
	}{
		{"profiles at 152-156", 155, []string{
			`ALTER TABLE sessions DROP COLUMN launched_at`,
			`DROP TABLE delegation_preference_revisions`,
			`DELETE FROM schema_migrations WHERE version >= 152`,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (152, ''), (153, ''), (154, ''), (155, '')`,
		}},
		{"profiles at 156-160", 160, []string{
			`DELETE FROM schema_migrations WHERE version > 160`,
		}},
	} {
		t.Run(ladder.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "attn.db")
			db, err := OpenDBAtSchemaVersion(path, 161)
			if err != nil {
				t.Fatalf("OpenDBAtSchemaVersion: %v", err)
			}
			for _, statement := range append([]string{`ALTER TABLE sessions ADD COLUMN todos TEXT`}, ladder.shape...) {
				if _, err := db.Exec(statement); err != nil {
					t.Fatalf("shaping the early desktops database: %v\n%s", err, statement)
				}
			}
			before := desktopRows(t, db)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			if s, _, err := Open(path); err == nil {
				s.Close()
				t.Fatal("an early desktops database opened, want it refused")
			} else if !strings.Contains(err.Error(), "development build of the desktops branch") || !strings.Contains(err.Error(), "moving "+path+" aside") {
				t.Fatalf("refusal = %v, want the cause and the reset", err)
			}

			reopened, _, err := openSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reopened.Close() })
			var version int
			if err := reopened.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != ladder.recorded {
				t.Fatalf("schema version after the refusal = %d (%v), want %d untouched", version, err, ladder.recorded)
			}
			if got := desktopRows(t, reopened); !reflect.DeepEqual(got, before) {
				t.Fatalf("desktops changed from %v to %v, want the refused database left as it was", before, got)
			}
		})
	}
}

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

func TestADatabaseUpgradedByNextsSolMigrationIsRefusedWithItsCauseAndLeftAsItWas(t *testing.T) {
	for _, schema := range []int{158, 159} {
		t.Run(fmt.Sprint(schema), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "attn.db")
			db, err := OpenDBAtSchemaVersion(path, 157)
			if err != nil {
				t.Fatalf("OpenDBAtSchemaVersion: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, '')`, schema); err != nil {
				t.Fatal(err)
			}
			if schema == 159 {
				if _, err := db.Exec(`CREATE TABLE kept_conversations (resume_id TEXT NOT NULL, agent TEXT NOT NULL, source_path TEXT NOT NULL, bytes INTEGER NOT NULL, stored_bytes INTEGER NOT NULL, copied_at TEXT NOT NULL, released_at TEXT NOT NULL DEFAULT '', deleted_at TEXT NOT NULL DEFAULT '', PRIMARY KEY (agent, resume_id))`); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			if s, _, err := Open(path); err == nil {
				s.Close()
				t.Fatal("a database from next's migration 158 opened, want it refused")
			} else if !strings.Contains(err.Error(), "build of next whose migration 158") || !strings.Contains(err.Error(), "moving "+path+" aside") {
				t.Fatalf("refusal = %v, want the cause and the reset", err)
			}

			reopened, _, err := openSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reopened.Close() })
			var version, profiles int
			if err := reopened.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != schema {
				t.Fatalf("schema version after the refusal = %d (%v), want %d untouched", version, err, schema)
			}
			if err := reopened.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'profiles'`).Scan(&profiles); err != nil || profiles != 0 {
				t.Fatalf("the refused database has %d profiles tables (%v), want none", profiles, err)
			}
		})
	}
}

func TestTheSolMigrationRunsAfterTheProfileLadder(t *testing.T) {
	for _, start := range []struct {
		name   string
		schema int
		wants  []int
	}{
		{"an install at 153", 153, []int{154, 155, 156, 157, 158, ProfileConversionSchemaVersion, 160, 161, 162, 163, 164, 165, 166, 167}},
		{"a desktops install at 162", 162, []int{163, 164, 165, 166, 167}},
		{"a production desktops install at 163", 163, []int{164, 165, 166, 167}},
		{"an install at 164", 164, []int{165, 166, 167}},
		{"an install at 165", 165, []int{166, 167}},
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
			before := map[int]bool{}
			for _, version := range recordedVersions(t, db) {
				before[version] = true
			}
			desktops := []string(nil)
			if start.schema >= 158 {
				desktops = desktopRows(t, db)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			s, upgrade, err := Open(path)
			if err != nil {
				t.Fatalf("upgrade from %d: %v", start.schema, err)
			}
			t.Cleanup(func() { s.Close() })
			if upgrade.From != start.schema || upgrade.To != 167 {
				t.Fatalf("upgrade = %+v, want %d -> 167", upgrade, start.schema)
			}
			var applied []int
			for _, version := range recordedVersions(t, s.db) {
				if !before[version] {
					applied = append(applied, version)
				}
			}
			if !reflect.DeepEqual(applied, start.wants) {
				t.Fatalf("applied migrations %v, want %v", applied, start.wants)
			}
			if desktops != nil {
				if got := desktopRows(t, s.db); !reflect.DeepEqual(got, desktops) {
					t.Fatalf("desktops changed from %v to %v, want only the Sol migration applied", desktops, got)
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

func recordedVersions(t *testing.T, db *sql.DB) []int {
	t.Helper()
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			t.Fatal(err)
		}
		out = append(out, version)
	}
	return out
}
