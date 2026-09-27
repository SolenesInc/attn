package store

import (
	"database/sql"
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
