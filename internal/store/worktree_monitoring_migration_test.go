package store

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestWorktreeMonitoringUpgradePreservesKnownRepositories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	db, err := OpenDBAtSchemaVersion(path, 1791301168635041)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, statement := range []string{
		`INSERT INTO worktrees (path, branch, main_repo, created_at, origin) VALUES
		('/unknown/wt', 'feature', '/unknown-origin', 'now', ''),
		('/git/wt', 'feature', '/git-origin', 'now', 'git'),
		('/attn/wt', 'feature', '/attn-origin', 'now', 'attn'),
		('/empty/wt', 'feature', '', 'now', '')`,
		`INSERT INTO sessions (id, label, directory, state_since, state_updated_at, last_seen, main_repo, closed_at) VALUES
		('live', 'live', '/live-session/subdir', 'now', 'now', 'now', '/live-session', ''),
		('closed', 'closed', '/closed-session', 'now', 'now', 'now', '/closed-session', 'now'),
		('legacy', 'legacy', '/unknown-origin/wt/nested', 'now', 'now', 'now', '', '')`,
		`INSERT INTO repo_integration_branches (main_repo, branch, source, resolved_at) VALUES
		('/past-monitoring', 'main', 'origin_head', 'now'),
		('/attn-origin', 'main', 'origin_head', 'now')`,
		`INSERT INTO worktree_sweep_log (id, path, main_repo, action, at) VALUES
		('removed', '/last-checkout', '/past-removal', 'removed', 'now'),
		('duplicate', '/attn-origin/wt', '/attn-origin', 'deleted', 'now')`,
		`INSERT INTO repos (repo) VALUES ('unrelated-pr-preference')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	want := []string{"/attn-origin", "/git-origin", "/live-session", "/past-monitoring", "/past-removal", "/unknown-origin"}
	if got := s.MonitoredWorktreeRepositories(); !slices.Equal(got, want) {
		t.Fatalf("upgraded monitoring = %v, want %v", got, want)
	}
}
