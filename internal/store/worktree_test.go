package store

import (
	"testing"
	"time"
)

func TestWorktreeInventoryAndRefreshErrorPreserveTrustedObservation(t *testing.T) {
	store := New()
	defer store.Close()
	now := time.Now()
	path := "/projects/repo--feature"
	store.AddWorktree(&Worktree{Path: path, Branch: "old", MainRepo: "/projects/repo", CreatedAt: now.Add(-time.Hour)})
	store.RecordWorktreeObservation(path, WorktreeObservation{
		Branch: "old", HeadSHA: "old-head", Dirty: true, DirtyFiles: 3,
		MergedSignal: MergedSignalTree, LastActivityAt: now.Add(-time.Hour),
	}, now)

	store.ApplyWorktreeInventory(path, "new", "new-head", false, false)
	store.RecordWorktreeRefreshError(path, "canceled probe")
	got := store.GetWorktree(path)
	if got.Branch != "new" || got.HeadSHA != "new-head" {
		t.Fatalf("inventory identity = %q/%q", got.Branch, got.HeadSHA)
	}
	if got.ObservedAt != now.Format(time.RFC3339) || !got.Dirty || got.DirtyFiles != 3 || got.MergedSignal != MergedSignalTree {
		t.Fatalf("inventory/error changed trusted observation: %+v", got)
	}
}
