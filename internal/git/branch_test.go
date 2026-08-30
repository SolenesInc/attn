package git

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestListRemoteBranches(t *testing.T) {
	t.Parallel()
	if os.Getenv("CI") != "" {
		t.Skip("Skipping remote branch test in CI")
	}

	dir, _ := os.Getwd()
	branches, err := ListRemoteBranches(dir)
	if err != nil {
		t.Fatalf("ListRemoteBranches failed: %v", err)
	}
	t.Logf("Found %d remote branches", len(branches))
}

func TestCheckoutRemoteBranch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	writeFile(t, dir, "file.txt", "initial")
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "initial")

	runGit(t, dir, "branch", "feature-x")

	err := CheckoutBranch(dir, "feature-x")
	if err != nil {
		t.Fatalf("CheckoutBranch failed: %v", err)
	}

	branch, _ := GetCurrentBranch(dir)
	if branch != "feature-x" {
		t.Errorf("Expected branch 'feature-x', got %q", branch)
	}
}

func TestRefExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")

	cases := []struct {
		ref  string
		want bool
	}{
		{"HEAD", true},
		{"main", true},
		{"origin/main", false}, // no remote configured
		{"does-not-exist", false},
	}
	for _, tc := range cases {
		if got := RefExists(dir, tc.ref); got != tc.want {
			t.Errorf("RefExists(%q) = %v, want %v", tc.ref, got, tc.want)
		}
	}
}

func TestGetCurrentBranchEmptyRepo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")

	branch, err := GetCurrentBranch(dir)
	if err != nil {
		t.Fatalf("GetCurrentBranch failed: %v", err)
	}
	if branch != "main" {
		t.Fatalf("expected main, got %q", branch)
	}
}

func TestListBranchesWithCommitsEmptyRepo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")

	branches, err := ListBranchesWithCommits(dir)
	if err != nil {
		t.Fatalf("ListBranchesWithCommits failed: %v", err)
	}
	if len(branches) != 0 {
		t.Fatalf("expected no committed branches, got %+v", branches)
	}
}

func TestListBranchesWithCommits(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	mainDir := filepath.Join(tmpDir, "main")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatalf("Failed to create main dir: %v", err)
	}

	runGit(t, mainDir, "init")
	runGit(t, mainDir, "config", "user.email", "test@test.com")
	runGit(t, mainDir, "config", "user.name", "Test")
	runGit(t, mainDir, "checkout", "-b", "main") // Ensure branch is named 'main'

	writeFile(t, mainDir, "file1.txt", "initial")
	runGit(t, mainDir, "add", "file1.txt")
	runGit(t, mainDir, "commit", "-m", "initial commit")

	runGit(t, mainDir, "branch", "feature-a")

	runGit(t, mainDir, "checkout", "-b", "feature-b")
	writeFile(t, mainDir, "file2.txt", "feature b")
	runGit(t, mainDir, "add", "file2.txt")
	runGit(t, mainDir, "commit", "-m", "add feature b")

	runGit(t, mainDir, "checkout", "main")

	wtDir := filepath.Join(tmpDir, "wt")
	runGit(t, mainDir, "worktree", "add", wtDir, "feature-a")

	branches, err := ListBranchesWithCommits(mainDir)
	if err != nil {
		t.Fatalf("ListBranchesWithCommits failed: %v", err)
	}

	// Only feature-b: main is current and feature-a is checked out in a worktree.
	if len(branches) != 1 {
		t.Fatalf("Expected 1 branch, got %d: %+v", len(branches), branches)
	}

	featureBBranch := branches[0]
	if featureBBranch.Name != "feature-b" {
		t.Fatalf("Expected feature-b, got %q", featureBBranch.Name)
	}

	if featureBBranch.IsCurrent {
		t.Error("Expected feature-b branch to not be marked as current")
	}

	if len(featureBBranch.CommitHash) != 7 {
		t.Errorf("Expected 7-char commit hash for feature-b, got %q", featureBBranch.CommitHash)
	}

	_, err = time.Parse(time.RFC3339, featureBBranch.CommitTime)
	if err != nil {
		t.Errorf("Expected ISO timestamp for feature-b commit time, got %q: %v", featureBBranch.CommitTime, err)
	}
}

func TestGetDefaultBranchPrefersConfiguredBase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")

	if got, _ := GetDefaultBranch(dir); got != "main" {
		t.Fatalf("GetDefaultBranch without config = %q, want main", got)
	}
	runGit(t, dir, "config", "attn.baseBranch", "next")
	if got, _ := GetDefaultBranch(dir); got != "next" {
		t.Fatalf("GetDefaultBranch with attn.baseBranch = %q, want next", got)
	}
}
