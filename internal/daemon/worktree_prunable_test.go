package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestListWorktreesDropsPrunableRows(t *testing.T) {
	root, repo := initProviderTestRepo(t)
	missing := filepath.Join(root, "missing")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feature", missing)
	if err := os.RemoveAll(missing); err != nil {
		t.Fatal(err)
	}
	d := NewForTesting(filepath.Join(root, "attn.sock"))
	t.Cleanup(d.stopEventBus)
	d.store.AddWorktree(&store.Worktree{Path: missing, MainRepo: repo, Branch: "feature", CreatedAt: time.Now()})

	listed := d.doListWorktrees(repo)

	if len(listed) != 0 {
		t.Fatalf("listed worktrees = %+v, want no stale row", listed)
	}
	if row := d.store.GetWorktree(missing); row != nil {
		t.Fatalf("stale row remains in store: %+v", row)
	}
}

func TestCreateWorktreeFromBranchPrunesMissingRegistration(t *testing.T) {
	root, repo := initProviderTestRepo(t)
	worktree := filepath.Join(root, "reused")
	runGitDaemon(t, repo, "worktree", "add", "-b", "feature-stale", worktree)
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}

	d := NewForTesting(filepath.Join(root, "attn.sock"))
	t.Cleanup(d.stopEventBus)
	created, err := d.doCreateWorktreeFromBranch(&protocol.CreateWorktreeFromBranchMessage{
		MainRepo: repo,
		Branch:   "feature-stale",
		Path:     protocol.Ptr(worktree),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created != worktree {
		t.Fatalf("created path = %q, want %q", created, worktree)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("recreated worktree: %v", err)
	}
}
