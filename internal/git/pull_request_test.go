package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureRemoteForIdentityUsesExistingAndAddsFork(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "commit", "--allow-empty", "-m", "init")
	runGit(t, repo, "remote", "add", "origin", "git@github.com:owner/repo.git")
	if remote, ok := RemoteForIdentity(repo, "github.com/owner/repo"); !ok || remote != "origin" {
		t.Fatalf("base remote = %q, %v", remote, ok)
	}
	remote, remoteURL, added, err := EnsureRemoteForIdentity(repo, "github.com", "fork/repo")
	if err != nil || !added || remote != "attn-fork-repo" || remoteURL != "https://github.com/fork/repo.git" {
		t.Fatalf("fork remote = %q %q added=%v err=%v", remote, remoteURL, added, err)
	}
	if again, _, addedAgain, err := EnsureRemoteForIdentity(repo, "github.com", "fork/repo"); err != nil || addedAgain || again != remote {
		t.Fatalf("second fork remote = %q added=%v err=%v", again, addedAgain, err)
	}
}

func TestPreserveWorktreeOnBackupCommitsTrackedAndUntrackedWithoutHooks(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "init")
	runGit(t, repo, "branch", "-M", "feature")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := StatusFingerprint(repo)
	if err != nil {
		t.Fatal(err)
	}
	sourceHead, _ := GetHeadCommit(repo)
	backupHead, err := PreserveWorktreeOnBackup(repo, "feature", "feature--attn-backup-test", sourceHead, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if backupHead == "" {
		t.Fatal("backup head is empty")
	}
	if clean, _ := IsWorktreeClean(repo); !clean {
		t.Fatal("backup worktree remained dirty")
	}
	files, err := runGitOutput(OpMetadata, repo, "show", "--format=", "--name-only", "HEAD")
	if err != nil || !strings.Contains(string(files), "tracked.txt") || !strings.Contains(string(files), "untracked.txt") {
		t.Fatalf("recovery commit files = %q err=%v", files, err)
	}
	if out, err := runGitOutput(OpMetadata, repo, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		t.Fatalf("backup unexpectedly has upstream %q", strings.TrimSpace(string(out)))
	}
}

func TestPreserveWorktreeOnBackupKeepsStagedStateWhenWorktreeRevertsIt(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	path := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(path, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "base")
	runGit(t, repo, "branch", "-M", "feature")
	if err := os.WriteFile(path, []byte("unique staged state\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "tracked.txt")
	if err := os.WriteFile(path, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := StatusFingerprint(repo)
	if err != nil {
		t.Fatal(err)
	}
	sourceHead, _ := GetHeadCommit(repo)
	backupHead, err := PreserveWorktreeOnBackup(repo, "feature", "feature--attn-backup-staged", sourceHead, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := runGitOutput(OpMetadata, repo, "show", backupHead+"^:tracked.txt")
	if err != nil || string(staged) != "unique staged state\n" {
		t.Fatalf("staged recovery content = %q, err=%v", staged, err)
	}
	working, err := runGitOutput(OpMetadata, repo, "show", backupHead+":tracked.txt")
	if err != nil || string(working) != "base\n" {
		t.Fatalf("working recovery content = %q, err=%v", working, err)
	}
	if clean, _ := IsWorktreeClean(repo); !clean {
		t.Fatal("backup worktree remained dirty")
	}
}

func TestIgnoredCheckoutCollisionsFindsAncestorCollision(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "target"), []byte("tracked target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "target")
	runGit(t, repo, "commit", "-m", "target")
	target, _ := GetHeadCommit(repo)
	runGit(t, repo, "rm", "target")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("target/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "target", "precious.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	collisions, err := IgnoredCheckoutCollisions(repo, target)
	if err != nil || len(collisions) != 1 || collisions[0] != "target/precious.txt" {
		t.Fatalf("collisions = %v, err=%v", collisions, err)
	}
}

func TestStatusFingerprintChangesWithDirtyFileContents(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := StatusFingerprint(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := StatusFingerprint(repo)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("fingerprint ignored dirty file contents")
	}
}

func TestBranchCompareAndSwapRefusesConcurrentMovement(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "commit", "--allow-empty", "-m", "first")
	first, _ := GetHeadCommit(repo)
	if err := CreateBranchAt(repo, "preserved", first); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "commit", "--allow-empty", "-m", "second")
	second, _ := GetHeadCommit(repo)
	if err := CreateBranchAt(repo, "preserved", second); err == nil {
		t.Fatal("create-only branch update overwrote an existing ref")
	}
	if err := DeleteBranchAt(repo, "preserved", second); err == nil {
		t.Fatal("conditional branch delete ignored the moved ref")
	}
	if head, ok, _ := BranchHead(repo, "preserved"); !ok || head != first {
		t.Fatalf("preserved branch = %q, exists=%v", head, ok)
	}
}

func TestFetchPullRequestCommitFromBaseRecoversDeletedHeadBranch(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	source := filepath.Join(root, "source")
	clone := filepath.Join(root, "clone")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init", "--bare", bare)
	runGit(t, source, "init")
	runGit(t, source, "commit", "--allow-empty", "-m", "base")
	runGit(t, source, "branch", "-M", "main")
	runGit(t, source, "remote", "add", "origin", bare)
	runGit(t, source, "push", "origin", "main")
	runGit(t, source, "switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(source, "feature.txt"), []byte("closed PR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "feature.txt")
	runGit(t, source, "commit", "-m", "feature")
	targetSHA, _ := GetHeadCommit(source)
	runGit(t, source, "push", "origin", "HEAD:refs/pull/42/head")
	runGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(t, root, "clone", "--no-local", bare, clone)
	if RefExists(clone, targetSHA) {
		t.Fatal("test clone unexpectedly has the pull request commit")
	}
	runGit(t, clone, "remote", "add", "head", bare)
	if err := FetchPullRequestHead(clone, "head", bare, "feature", targetSHA, ""); err == nil {
		t.Fatal("deleted head branch fetch succeeded")
	}
	if err := FetchPullRequestCommitFromBase(clone, "origin", bare, 42, targetSHA, ""); err != nil {
		t.Fatal(err)
	}
	if !RefExists(clone, targetSHA) {
		t.Fatalf("fallback did not resolve %s", targetSHA)
	}
}

func TestIgnoredCheckoutCollisionsFindsOnlyTargetPaths(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("generated.txt\nother.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "generated.txt"), []byte("tracked target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "-f", "generated.txt")
	runGit(t, repo, "commit", "-m", "target")
	target, _ := GetHeadCommit(repo)
	runGit(t, repo, "rm", "--cached", "generated.txt")
	if err := os.WriteFile(filepath.Join(repo, "other.txt"), []byte("ignored only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	collisions, err := IgnoredCheckoutCollisions(repo, target)
	if err != nil || len(collisions) != 1 || collisions[0] != "generated.txt" {
		t.Fatalf("collisions = %v, err=%v", collisions, err)
	}
}

func TestAcquirePullRequestCheckoutLockRejectsConcurrentCheckout(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	release, err := AcquirePullRequestCheckoutLock(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquirePullRequestCheckoutLock(repo); err == nil || !strings.Contains(err.Error(), "already changing") {
		t.Fatalf("second lock error = %v", err)
	}
	release()
	secondRelease, err := AcquirePullRequestCheckoutLock(repo)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	secondRelease()
}
