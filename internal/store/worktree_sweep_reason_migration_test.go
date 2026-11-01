package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		s.AddWorktree(&Worktree{Path: row.path, Branch: filepath.Base(row.path), MainRepo: "/work/shop"})
		s.SetWorktreeSweep(row.path, row.status, row.reason, time.Time{})
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
