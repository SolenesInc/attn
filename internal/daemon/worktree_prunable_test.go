package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestReconcileListedWorktreesDropsPrunableRows(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	t.Cleanup(d.stopEventBus)
	repo := "/repo/main"
	missing := "/repo/missing"
	d.store.AddWorktree(&store.Worktree{Path: missing, MainRepo: repo, Branch: "feature", CreatedAt: time.Now()})

	listed := d.reconcileListedWorktrees(repo, []attngit.WorktreeEntry{
		{Path: repo, Branch: "main"},
		{Path: missing, Branch: "feature", Prunable: true},
	})

	if len(listed) != 0 {
		t.Fatalf("listed worktrees = %+v, want no stale row", listed)
	}
	if row := d.store.GetWorktree(missing); row != nil {
		t.Fatalf("stale row remains in store: %+v", row)
	}
}

func TestCreateWorktreeFromBranchPrunesMissingRegistration(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "main")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "init", "-b", "main")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "init")
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
