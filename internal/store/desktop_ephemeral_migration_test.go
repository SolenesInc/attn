package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/profiles"
)

func TestDesktopEphemeralRepairPreservesLegacyLaunchDesktops(t *testing.T) {
	for _, alreadyUpgraded := range []bool{false, true} {
		t.Run(fmt.Sprintf("already_upgraded=%t", alreadyUpgraded), func(t *testing.T) {
			checkLegacyLaunchDesktopRepair(t, alreadyUpgraded)
		})
	}
}

func checkLegacyLaunchDesktopRepair(t *testing.T, alreadyUpgraded bool) {
	t.Helper()
	const originalMigration = 1795722363489267
	const legacyTime = "2025-01-01T12:34:56.123456789Z"

	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, originalMigration-1)
	if err != nil {
		t.Fatal(err)
	}
	var profileID string
	if err := s.db.QueryRow(`SELECT id FROM profiles WHERE deleted_at = '' LIMIT 1`).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	insertRow := func(id, name, orderKey string) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO desktops (id, profile_id, name, order_key, tree_json, active_pane_id, revision, created_at, updated_at) VALUES (?, ?, ?, ?, '', '', 1, ?, ?)`, id, profileID, name, orderKey, legacyTime, legacyTime); err != nil {
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
	insertRow("d-explicit", "Nightly check", "b")
	insertRow("d-crew-own", "Alder", "c")
	insertRow(slottedID, "Slotted check", "d")
	insertRow("d-custom", "Ops", "e")
	insertRow("d-shared", "Shared check", "f")
	insertRow("d-user", "Focus", "g")
	if _, err := s.db.Exec(`INSERT INTO launch_desktops (kind, item_id, desktop_id, confirmed) VALUES
		('automation', '10', 'd-explicit', 1),
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

	want := map[string]bool{
		"d-explicit": false,
		"d-crew-own": false,
		slottedID:    false,
		"d-custom":   false,
		"d-shared":   false,
		"d-user":     false,
	}
	if alreadyUpgraded {
		intermediate, err := newStoreAtVersion(path, originalMigration)
		if err != nil {
			t.Fatal(err)
		}
		previous, err := intermediate.GetDesktop("d-explicit")
		if err != nil || !previous.Ephemeral {
			t.Fatalf("the first migration left the same-named desktop as %+v (%v)", previous, err)
		}
		var raw string
		if err := intermediate.db.QueryRow(`SELECT applied_at FROM schema_migrations WHERE version = ?`, originalMigration).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		applied, err := time.Parse(time.DateTime, raw)
		if err != nil {
			t.Fatal(err)
		}
		sameSecond := applied.Add(time.Second - time.Nanosecond).Format(sortableTimeFormat)
		if _, err := intermediate.db.Exec(`UPDATE desktops SET created_at = ? WHERE id = 'd-explicit'`, sameSecond); err != nil {
			t.Fatal(err)
		}
		later := applied.Add(time.Second).Format(sortableTimeFormat)
		if _, err := intermediate.db.Exec(`INSERT INTO desktops (id, profile_id, name, order_key, tree_json, active_pane_id, revision, created_at, updated_at, ephemeral)
			VALUES ('d-new', ?, 'New launch', 'h', '', '', 1, ?, ?, 1)`, profileID, later, later); err != nil {
			t.Fatal(err)
		}
		if err := intermediate.Close(); err != nil {
			t.Fatal(err)
		}
		want["d-new"] = true
	}

	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upgraded.Close() })

	for id, wantEphemeral := range want {
		desktop, err := upgraded.GetDesktop(id)
		if err != nil {
			t.Fatalf("GetDesktop(%s): %v", id, err)
		}
		if desktop.Ephemeral != wantEphemeral {
			t.Errorf("desktop %s ephemeral = %v, want %v", id, desktop.Ephemeral, wantEphemeral)
		}
	}
	if _, _, err := upgraded.RemoveEmptyDesktops(0); err != nil {
		t.Fatal(err)
	}
	for id, ephemeral := range want {
		_, err := upgraded.GetDesktop(id)
		if ephemeral != profiles.IsNotFound(err) || (!ephemeral && err != nil) {
			t.Errorf("after cleanup GetDesktop(%s), ephemeral=%t, error=%v", id, ephemeral, err)
		}
	}
}
