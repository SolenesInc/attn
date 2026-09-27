package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureDetachedWorktreeAtRevisionCreateAdoptAndProtectEvidence(t *testing.T) {
	mainDir := filepath.Join(t.TempDir(), "main")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")
	revision, err := NewClient().GetHeadCommit(context.Background(), mainDir)
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(filepath.Dir(mainDir), "detached")
	created, err := NewClient().EnsureDetachedWorktreeAtRevision(context.Background(), mainDir, worktree, revision)
	if err != nil || !created {
		t.Fatalf("create = %v, %v", created, err)
	}
	created, err = NewClient().EnsureDetachedWorktreeAtRevision(context.Background(), mainDir, worktree, revision)
	if err != nil || created {
		t.Fatalf("adopt = %v, %v", created, err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "evidence.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient().EnsureDetachedWorktreeAtRevision(context.Background(), mainDir, worktree, revision); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("dirty adoption err = %v", err)
	}
	if created, err := NewClient().EnsureAutomationSessionWorktree(context.Background(), mainDir, worktree, revision, "", true); err != nil || created {
		t.Fatalf("persisted session dirty adoption = %v, %v", created, err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "evidence.txt")); err != nil {
		t.Fatalf("dirty evidence was changed: %v", err)
	}

	attached := filepath.Join(filepath.Dir(mainDir), "attached")
	runGit(t, mainDir, "worktree", "add", "-b", "review-attached", attached, revision)
	if _, err := NewClient().EnsureDetachedWorktreeAtRevision(context.Background(), mainDir, attached, revision); err == nil || !strings.Contains(err.Error(), "attached to branch") {
		t.Fatalf("attached adoption err = %v", err)
	}
}

func TestEnsureDetachedWorktreeAtRevisionRecoversFreshStaleMetadata(t *testing.T) {
	mainDir := filepath.Join(t.TempDir(), "main")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, mainDir, "init")
	runGit(t, mainDir, "commit", "--allow-empty", "-m", "init")
	revision, err := NewClient().GetHeadCommit(context.Background(), mainDir)
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(filepath.Dir(mainDir), "interrupted")
	runGit(t, mainDir, "worktree", "add", "--detach", worktree, revision)
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	created, err := NewClient().EnsureDetachedWorktreeAtRevision(context.Background(), mainDir, worktree, revision)
	if err != nil || !created {
		t.Fatalf("fresh stale metadata recovery created=%v err=%v", created, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
