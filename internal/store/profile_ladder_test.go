package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func TestADatabaseFromTheDesktopsBranchGetsNextsMigrationsWithoutConvertingAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	db, err := OpenDBAtSchemaVersion(path, 159)
	if err != nil {
		t.Fatalf("OpenDBAtSchemaVersion: %v", err)
	}
	var profileID string
	if err := db.QueryRow(`SELECT id FROM profiles`).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO sessions (id, label, directory, state, state_since, state_updated_at, last_seen, profile_id)
			VALUES ('agent', 'agent', '/fixture', 'launching', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z', '2026-09-01T02:00:00+02:00', '` + profileID + `')`,
		`ALTER TABLE sessions DROP COLUMN launched_at`,
		`DROP TABLE delegation_preference_revisions`,
		`CREATE TABLE delegation_preferences (id INTEGER PRIMARY KEY, config TEXT NOT NULL)`,
		`INSERT INTO delegation_preferences (id, config) VALUES (1, '{"revision": 3}')`,
		`DELETE FROM schema_migrations WHERE version >= 152`,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (152, ''), (153, ''), (154, ''), (155, '')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("shaping the desktops-branch database: %v\n%s", err, statement)
		}
	}
	before := desktopRows(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, upgrade, err := Open(path)
	if err != nil {
		t.Fatalf("opening the desktops-branch database: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if upgrade.From != 155 || upgrade.To != LatestSchemaVersion() {
		t.Fatalf("upgrade = %+v, want 155 to %d", upgrade, LatestSchemaVersion())
	}
	var versions []int
	rows, err := s.db.Query(`SELECT version FROM schema_migrations WHERE version >= 152 ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	rows.Close()
	if !reflect.DeepEqual(versions, []int{152, 153, 154, 155, 156, 157, 158, 159, 160}) {
		t.Fatalf("recorded versions = %v, want next's 152-155 and the profile ladder at 156-160", versions)
	}
	var launchedAt, lastSeen, owner string
	if err := s.db.QueryRow(`SELECT launched_at, last_seen, profile_id FROM sessions WHERE id = 'agent'`).Scan(&launchedAt, &lastSeen, &owner); err != nil {
		t.Fatalf("reading the agent after the upgrade: %v", err)
	}
	if launchedAt != "2026-09-01T00:00:00Z" || lastSeen != "2026-09-01T00:00:00Z" || owner != profileID {
		t.Errorf("agent launched_at=%q last_seen=%q profile=%q, want next's launch stamp, a UTC last-seen and its profile kept", launchedAt, lastSeen, owner)
	}
	var revision int
	if err := s.db.QueryRow(`SELECT revision FROM delegation_preference_revisions`).Scan(&revision); err != nil || revision != 3 {
		t.Errorf("delegation preference revisions carry %d (%v), want the stored revision 3", revision, err)
	}
	if after := desktopRows(t, s.db); !reflect.DeepEqual(after, before) {
		t.Errorf("desktops changed from %v to %v, want the conversion left alone", before, after)
	}
	assertWorkspaceSchemaRetired(t, s.db)
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
