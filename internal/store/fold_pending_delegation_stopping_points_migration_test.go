package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/delegationprefs"
)

func TestUpgradeKeepsFoldedStoppingPointsForPendingDelegationsAndRefusesPreUpgradeExports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1793534250941633)
	if err != nil {
		t.Fatal(err)
	}
	preUpgradeExport := `{"enabled":true,"workflow_skill_enabled":true,"revision":1,"roles":[{"id":"build","name":"Build","icon":"","enabled":true,"description":"","instructions":"Keep {{literal}} intact","stopping_point":"Stop once the tests pass","default_choice_id":"default","choices":[{"id":"default","name":"Everyday","when":"","selection":{"harness":"codex"}}]}],"fallback":{"selection":{"harness":""},"instructions":""}}`
	if _, err := s.db.Exec(`INSERT INTO delegation_preference_revisions (revision, config, created_at) VALUES (1, ?, '2026-11-01T00:00:00Z')`, preUpgradeExport); err != nil {
		t.Fatal(err)
	}
	snapshots := map[string]string{
		"with-stop":    `{"selection":{"harness":"codex"},"role_name":"Build","instructions":"Keep {{literal}} intact","stopping_point":"Stop once the tests pass","revision":1}`,
		"empty-stop":   `{"selection":{"harness":"codex"},"role_name":"Build","instructions":"Inspect every corner","stopping_point":"","revision":1}`,
		"no-stop-text": `{"selection":{"harness":"codex"},"role_name":"Build","instructions":"","stopping_point":"Stop at the report","revision":1}`,
	}
	for requestID, snapshot := range snapshots {
		if _, err := s.db.Exec(`INSERT INTO delegation_operations (request_id, operation_id, request_json, state, progress, session_id, resolved_preferences, created_at, updated_at)
			VALUES (?, ?, '{}', 'accepted', '', '', ?, '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`, requestID, "op-"+requestID, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()

	want := map[string]string{
		"with-stop":    "Keep {{literal}} intact\n\nStopping point: Stop once the tests pass",
		"empty-stop":   "Inspect every corner",
		"no-stop-text": "Stopping point: Stop at the report",
	}
	for requestID, instructions := range want {
		var raw string
		if err := upgraded.db.QueryRow(`SELECT resolved_preferences FROM delegation_operations WHERE request_id = ?`, requestID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var resolved delegationprefs.Resolved
		if err := json.Unmarshal([]byte(raw), &resolved); err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal([]byte(raw), &fields)
		if _, left := fields["stopping_point"]; left || resolved.Instructions != instructions {
			t.Errorf("%s snapshot after the upgrade = %s, want instructions %q and no stopping point", requestID, raw, instructions)
		}
	}

	live, err := upgraded.GetDelegationPreferences()
	if err != nil {
		t.Fatal(err)
	}
	if live.Revision != 2 || live.Roles[0].Instructions != want["with-stop"] {
		t.Fatalf("live table after the upgrade is revision %d with instructions %q; want revision 2 carrying the folded text", live.Revision, live.Roles[0].Instructions)
	}

	var stale delegationprefs.Config
	if err := json.Unmarshal([]byte(preUpgradeExport), &stale); err != nil {
		t.Fatal(err)
	}
	if _, err := upgraded.SaveDelegationPreferences(stale, DelegationPreferencesNote{}); !errors.Is(err, delegationprefs.ErrConflict) {
		t.Fatalf("applying a pre-upgrade export: %v, want it refused as stale", err)
	}

	rolledBack, err := upgraded.RollbackDelegationPreferences(nil, DelegationPreferencesNote{})
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Config.Roles[0].Instructions != want["with-stop"] {
		t.Errorf("rolling back past the upgrade lost the folded text: %q", rolledBack.Config.Roles[0].Instructions)
	}
}
