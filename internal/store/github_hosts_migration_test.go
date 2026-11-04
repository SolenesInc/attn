package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestUpgradeRemembersEveryGitHubHostItsRowsName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1793669829108174)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPRs([]*protocol.PR{{ID: "ghe-polled.test:acme/shop#1", Host: "ghe-polled.test", Repo: "acme/shop", Number: 1, URL: "https://ghe-polled.test/acme/shop/pull/1", Role: "author", State: "waiting"}}); err != nil {
		t.Fatal(err)
	}
	listedOnTheForgottenDay := time.Date(2026, 11, 3, 16, 30, 0, 0, time.UTC)
	s.RecordRepoMergedBranches("/src/ledger", []MergedBranch{{Branch: "feat-old", Number: 2, URL: "https://ghe-listed.test/acme/ledger/pull/2"}}, listedOnTheForgottenDay)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if merged := s.RepoMergedBranches("/src/ledger"); len(merged) != 0 {
		t.Fatalf("/src/ledger kept merged pull requests %v, want this test to cover a host named only by rows the upgrade forgets", merged)
	}
	for host, want := range map[string]bool{
		"github.com":      true,
		"ghe-polled.test": true,
		"ghe-listed.test": true,
		"forge.test":      false,
	} {
		if got := s.IsGitHubHost(host); got != want {
			t.Errorf("IsGitHubHost(%s) after the upgrade = %v, want %v", host, got, want)
		}
	}
}
