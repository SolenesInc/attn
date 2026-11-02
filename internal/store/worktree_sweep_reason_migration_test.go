package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeDropsSeedIDsFromStoredSweepReasons(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1793563943810738)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		path   string
		status WorktreeSweepStatus
		reason string
	}{
		{"/work/shop--owned", WorktreeSweepKeptOpenSeed, "open seed s-aaaaaa, s-bbbbbb points at it"},
		{"/work/shop--dirty", WorktreeSweepKeptDirty, "2 uncommitted or untracked file(s)"},
	} {
		if _, err := s.db.Exec(`INSERT INTO worktrees (path, branch, main_repo, created_at, sweep_status, sweep_reason) VALUES (?, ?, '/work/shop', '', ?, ?)`,
			row.path, filepath.Base(row.path), string(row.status), row.reason); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owned := s.GetWorktree("/work/shop--owned")
	if owned == nil || owned.SweepStatus != WorktreeSweepKeptOpenSeed || strings.Contains(owned.SweepReason, "s-") {
		t.Errorf("after the upgrade the seed's worktree reads %+v; want kept_open_seed naming no seed", owned)
	}
	if dirty := s.GetWorktree("/work/shop--dirty"); dirty == nil || dirty.SweepReason != "2 uncommitted or untracked file(s)" {
		t.Errorf("after the upgrade the dirty worktree reads %+v; want its reason unchanged", dirty)
	}
}
