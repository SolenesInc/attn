package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureManagedCloneDoesNotReplaceMismatchedExistingTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "evidence")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewClient().EnsureManagedClone(context.Background(), "https://github.com/owner/repo.git", target, "github.com/owner/repo", ""); err == nil {
		t.Fatal("expected non-repository target failure")
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "keep" {
		t.Fatalf("existing target was changed: %q %v", content, err)
	}
}

func TestEnsureManagedCloneReturnsPublishedPathThroughSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	realParent := filepath.Join(root, "real-instance")
	if err := os.MkdirAll(realParent, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(root, "linked-instance")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatal(err)
	}

	staging := filepath.Join(linkedParent, "cache", ".clone-staged", "repo")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, staging, "init")
	runGit(t, staging, "commit", "--allow-empty", "-m", "init")
	runGit(t, staging, "remote", "add", "origin", "https://github.com/owner/repo.git")
	target := filepath.Join(linkedParent, "cache", "repo")
	mainRepo, err := NewClient().publishManagedClone(context.Background(), staging, target, "github.com/owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if mainRepo != CanonicalizePath(target) {
		t.Fatalf("main repo = %q, want published %q", mainRepo, CanonicalizePath(target))
	}
	if _, err := os.Stat(mainRepo); err != nil {
		t.Fatalf("returned repository path is not live: %v", err)
	}
}
