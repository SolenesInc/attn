package store

import (
	"path/filepath"
	"testing"
)

func TestPresentRetirementPreservesSharedStateAcrossUpgradeAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1791738052340741)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO presentations(id, session_id, title, kind, repo_path, created_at, address) VALUES ('obsolete', 'preserved', 'Checkout', 'changes', '/tmp', 'now', 'session:preserved')`,
		`INSERT INTO presentation_rounds(id, presentation_id, seq, manifest_yaml, base_sha, head_sha, created_at) VALUES ('round', 'obsolete', 1, 'version: 1', 'a', 'b', 'now')`,
		`INSERT INTO presentation_comments(id, round_id, filepath, line_start, line_end, side, content, created_at) VALUES ('comment', 'round', 'main.go', 1, 1, 'new', 'looks good', 'now')`,
		`INSERT INTO sessions(id, label, directory, state, state_since, state_updated_at, last_seen) VALUES ('preserved', 'Kept session', '/tmp', 'idle', 'now', 'now', 'now')`,
	} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for restart := 0; restart < 2; restart++ {
		upgraded, err := NewWithDB(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"presentations", "presentation_rounds", "presentation_comments"} {
			if hasTable(t, upgraded, table) {
				t.Errorf("retired table %s survived restart %d", table, restart)
			}
		}
		if session := upgraded.Get("preserved"); session == nil || session.Label != "Kept session" {
			t.Errorf("restart %d: kept session = %+v, want Kept session", restart, session)
		}
		if err := upgraded.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
