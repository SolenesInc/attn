package store

import (
	"path/filepath"
	"testing"
)

func TestRoleStoppingPointsFoldIntoInstructionsOnUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1793534250941633)
	if err != nil {
		t.Fatal(err)
	}
	role := func(instructions, stoppingPoint string) string {
		return `{"id":"build","name":"Build","icon":"","enabled":true,"description":"","instructions":"` + instructions + `","stopping_point":"` + stoppingPoint + `","default_choice_id":"default","choices":[{"id":"default","name":"Everyday","when":"","selection":{"harness":"codex"}}]}`
	}
	attnRole := `{"id":"pathfinder","builtin":"pathfinder","name":"","icon":"","enabled":true,"description":"","instructions":"","stopping_point":"","default_choice_id":"default","choices":[{"id":"default","name":"Default","when":"","selection":{"harness":""}}]}`
	revisions := []string{
		`{"enabled":true,"workflow_skill_enabled":true,"roles":[` + role("Keep {{literal}} intact", "Stop once the tests pass") + `,` + attnRole + `],"fallback":{"selection":{"harness":""},"instructions":""}}`,
		`{"enabled":true,"workflow_skill_enabled":true,"roles":[` + role("", "Stop at the report") + `],"fallback":{"selection":{"harness":""},"instructions":""}}`,
		`{"enabled":true,"workflow_skill_enabled":true,"roles":[` + role("Inspect every corner", "") + `],"fallback":{"selection":{"harness":""},"instructions":""}}`,
	}
	for i, config := range revisions {
		if _, err := s.db.Exec(`INSERT INTO delegation_preference_revisions (revision, config, created_at) VALUES (?, ?, '2026-11-01T00:00:00Z')`, i+1, config); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	for run := 1; run <= 2; run++ {
		upgraded, err := NewWithDB(path)
		if err != nil {
			t.Fatal(err)
		}
		want := map[int]string{
			1: "Keep {{literal}} intact\n\nStopping point: Stop once the tests pass",
			2: "Stopping point: Stop at the report",
			3: "Inspect every corner",
		}
		for revision, instructions := range want {
			row, found, err := readDelegationPreferencesRevision(upgraded.db, revision)
			if err != nil || !found {
				t.Fatalf("run %d revision %d: found=%v %v", run, revision, found, err)
			}
			if got := row.revision.Config.Roles[0].Instructions; got != instructions {
				t.Errorf("run %d revision %d instructions = %q, want %q", run, revision, got, instructions)
			}
		}
		var leftover int
		if err := upgraded.db.QueryRow(`SELECT COUNT(*) FROM delegation_preference_revisions WHERE instr(config, '"stopping_point"') > 0`).Scan(&leftover); err != nil || leftover != 0 {
			t.Errorf("run %d left %d revisions with a stopping point (%v)", run, leftover, err)
		}
		live, err := upgraded.GetDelegationPreferences()
		if err != nil || live.Revision != 3 {
			t.Fatalf("run %d live revision = %d, %v; the fold must not add a revision", run, live.Revision, err)
		}
		if _, err := upgraded.db.Exec(`DELETE FROM schema_migrations WHERE version = 1793534250941634`); err != nil {
			t.Fatal(err)
		}
		if err := upgraded.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
