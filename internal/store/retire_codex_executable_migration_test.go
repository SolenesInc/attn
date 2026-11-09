package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestUpgradeClearsTheExecutableOnlyFromSavedCodexAutomations(t *testing.T) {
	const beforeRetirement = 1794195362271488 - 1
	saved := []struct {
		id         int
		spec, want string
	}{
		{1, `{"name":"pinned codex","launch":{"driver":"codex","executable":"/opt/codex"}}`, ""},
		{2, `{"name":"pinned claude","launch":{"driver":"claude","executable":"/opt/claude"}}`, "/opt/claude"},
		{3, `{"name":"plain codex","launch":{"driver":"codex"}}`, ""},
	}
	path := filepath.Join(t.TempDir(), "automations.db")
	old, err := newStoreAtVersion(path, beforeRetirement)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range saved {
		if _, err := old.db.Exec(`INSERT INTO automation_definitions(id,name,enabled,revision,spec_json,created_at,updated_at) VALUES(?,'a',1,1,?,'2026-11-01T10:00:00Z','2026-11-01T10:00:00Z')`, s.id, s.spec); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	for _, s := range saved {
		var specJSON string
		if err := upgraded.db.QueryRow(`SELECT spec_json FROM automation_definitions WHERE id=?`, s.id).Scan(&specJSON); err != nil {
			t.Fatal(err)
		}
		var spec struct {
			Name   string `json:"name"`
			Launch struct {
				Executable string `json:"executable"`
			} `json:"launch"`
		}
		if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
			t.Fatal(err)
		}
		if spec.Name == "" || spec.Launch.Executable != s.want {
			t.Errorf("definition %d after the upgrade = %s, want executable %q and the rest kept", s.id, specJSON, s.want)
		}
	}
}
