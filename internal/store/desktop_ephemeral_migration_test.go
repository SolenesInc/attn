package store

import (
	"path/filepath"
	"testing"
)

func TestDesktopEphemeralMigrationBackfillsOnlySingleOwnerUnslottedOwnDesktops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1795722363489267-1)
	if err != nil {
		t.Fatal(err)
	}
	var profileID string
	if err := s.db.QueryRow(`SELECT id FROM profiles WHERE deleted_at = '' LIMIT 1`).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	insertRow := func(id, name, orderKey string) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO desktops (id, profile_id, name, order_key, tree_json, active_pane_id, revision, created_at, updated_at) VALUES (?, ?, ?, ?, '', '', 1, 'now', 'now')`, id, profileID, name, orderKey); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO automation_definitions (id, name, enabled, revision, spec_json, profile_id, created_at, updated_at, deleted_at) VALUES
		(10, 'Nightly check', 1, 1, '{}', ?, 'now', 'now', ''),
		(11, 'Slotted check', 1, 1, '{}', ?, 'now', 'now', ''),
		(12, 'Custom check', 1, 1, '{}', ?, 'now', 'now', ''),
		(13, 'Shared check', 1, 1, '{}', ?, 'now', 'now', ''),
		(14, 'Shared buddy', 1, 1, '{}', ?, 'now', 'now', '')`,
		profileID, profileID, profileID, profileID, profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO crew_members (member_key, name, profile_id, retired_at) VALUES ('alder', 'Alder', ?, '')`, profileID); err != nil {
		t.Fatal(err)
	}
	slottedID := profileID + "/desktop_5"
	insertRow("d-auto-own", "Nightly check", "b")
	insertRow("d-crew-own", "Alder", "c")
	insertRow(slottedID, "Slotted check", "d")
	insertRow("d-custom", "Ops", "e")
	insertRow("d-shared", "Shared check", "f")
	insertRow("d-user", "Focus", "g")
	if _, err := s.db.Exec(`INSERT INTO launch_desktops (kind, item_id, desktop_id, confirmed) VALUES
		('automation', '10', 'd-auto-own', 1),
		('crew', 'alder', 'd-crew-own', 1),
		('automation', '11', ?, 1),
		('automation', '12', 'd-custom', 1),
		('automation', '13', 'd-shared', 1),
		('automation', '14', 'd-shared', 1)`, slottedID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upgraded.Close() })

	want := map[string]bool{
		"d-auto-own": true,
		"d-crew-own": true,
		slottedID:    false,
		"d-custom":   false,
		"d-shared":   false,
		"d-user":     false,
	}
	for id, wantEphemeral := range want {
		desktop, err := upgraded.GetDesktop(id)
		if err != nil {
			t.Fatalf("GetDesktop(%s): %v", id, err)
		}
		if desktop.Ephemeral != wantEphemeral {
			t.Errorf("desktop %s ephemeral = %v, want %v", id, desktop.Ephemeral, wantEphemeral)
		}
	}
}
