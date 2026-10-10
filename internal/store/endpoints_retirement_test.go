package store

import (
	"path/filepath"
	"testing"
)

func TestEndpointsRetirementKeepsSessionsAcrossUpgrade(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(dbPath, 1791301168635041)
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
	var label string
	if err := upgraded.db.QueryRow(`SELECT label FROM sessions WHERE id = 'session-1'`).Scan(&label); err != nil || label != "Preserved" {
		t.Fatalf("session after the upgrade = %q (%v), want Preserved", label, err)
	}
}
