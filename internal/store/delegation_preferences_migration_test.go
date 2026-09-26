package store

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/victorarias/attn/internal/delegationprefs"
)

func TestDelegationPreferencesMigrationCarriesTheSavedTableIntoHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newSeededStore(path)
	if err != nil {
		t.Fatal(err)
	}
	saved := delegationprefs.Config{Enabled: true, Revision: 3, Roles: []delegationprefs.Role{{ID: "build", Name: "Build", Enabled: true, Description: "Implement changes", Instructions: "Keep {{literal}} intact", DefaultChoiceID: "default", Choices: []delegationprefs.Choice{{ID: "default", Name: "Everyday", Selection: delegationprefs.Selection{Harness: "codex", Model: "test-model", Effort: "medium"}}}}}, Fallback: delegationprefs.Fallback{Selection: delegationprefs.Selection{Harness: "copilot"}}}
	raw, _ := json.Marshal(saved)
	if _, err := s.db.Exec(`
		DROP TABLE delegation_preference_revisions;
		CREATE TABLE delegation_preferences (id INTEGER PRIMARY KEY CHECK (id = 1), config TEXT NOT NULL);
		ALTER TABLE delegation_operations DROP COLUMN resolved_preferences;
		DELETE FROM schema_migrations WHERE version >= 138;
	`); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO delegation_preferences (id, config) VALUES (1, ?)`, string(raw)); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := newSeededStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	var count int
	if err := migrated.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'delegation_preferences'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("delegation_preferences table: count=%d err=%v", count, err)
	}
	if err := migrated.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('delegation_operations') WHERE name = 'resolved_preferences'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("resolved_preferences column: count=%d err=%v", count, err)
	}
	live, err := migrated.GetDelegationPreferences()
	if err != nil || !reflect.DeepEqual(live, saved) {
		t.Fatalf("carried table: %+v, %v", live, err)
	}
	if _, err := migrated.RollbackDelegationPreferences(nil, DelegationPreferencesNote{}); err == nil {
		t.Fatal("rolled back past the start of the carried history")
	}
}
