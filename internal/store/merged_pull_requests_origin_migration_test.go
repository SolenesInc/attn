package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUpgradeForgetsMergedPullRequestsSeenWhileOriginReadItsLastURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1793669829108174)
	if err != nil {
		t.Fatal(err)
	}
	cet := time.FixedZone("CET", 60*60)
	before := time.Date(2026, 11, 2, 23, 30, 0, 0, time.UTC).In(cet)
	since := time.Date(2026, 11, 3, 16, 30, 0, 0, cet)
	settled, misread := "/src/shop", "/src/ledger"
	s.RecordRepoMergedBranches(settled, []MergedBranch{{Branch: "feat-cart", Number: 3, URL: "https://github.test/acme/shop/pull/3", HeadSHA: "a1b2c3d4"}}, before)
	s.SetRepoIntegrationBranch(settled, "main", "pull_requests", before)
	s.RecordRepoMergedBranches(misread, []MergedBranch{{Branch: "feat-login", Number: 7, URL: "https://github.test/acme/shop/pull/7", HeadSHA: "e83fa708"}}, since)
	s.SetRepoIntegrationBranch(misread, "release", "pull_requests", since)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if merged := s.RepoMergedBranches(misread); len(merged) != 0 {
		t.Errorf("after the upgrade %s still has merged pull requests %v that may belong to another origin URL", misread, merged)
	}
	if record := s.RepoIntegrationBranch(misread); record != nil {
		t.Errorf("after the upgrade %s still has integration branch %+v learned from those pull requests", misread, *record)
	}
	if merged := s.RepoMergedBranches(settled); len(merged) != 1 {
		t.Errorf("after the upgrade %s has merged pull requests %v, want feat-cart recorded before the misread kept", settled, merged)
	}
	if record := s.RepoIntegrationBranch(settled); record == nil || record.Branch != "main" {
		t.Errorf("after the upgrade %s has integration branch %+v, want main kept", settled, record)
	}
}
