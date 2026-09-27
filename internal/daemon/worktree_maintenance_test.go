package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestTrackedRepositoriesUsesStoredMainRepoAndBackfillsLegacySessions(t *testing.T) {
	d := sweepDaemon(t)
	d.store.Add(&protocol.Session{ID: "known", Directory: "/missing/session", MainRepo: protocol.Ptr("/missing/main")})
	repos, err := d.trackedRepositoriesContext(context.Background())
	if err != nil || len(repos) != 1 || repos[0] != "/missing/main" {
		t.Fatalf("stored main repo discovery = %v, %v", repos, err)
	}

	root := t.TempDir()
	mainRepo := filepath.Join(root, "main")
	worktree := filepath.Join(root, "worktree")
	if err := os.MkdirAll(filepath.Join(worktree, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	d.store.AddWorktree(&store.Worktree{Path: worktree, MainRepo: mainRepo, CreatedAt: time.Now()})
	d.store.Add(&protocol.Session{ID: "legacy", Directory: filepath.Join(worktree, "nested")})
	if _, err := d.trackedRepositoriesContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := protocol.Deref(d.store.Get("legacy").MainRepo); got != mainRepo {
		t.Fatalf("legacy main repo = %q, want %q", got, mainRepo)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := d.trackedRepositoriesContext(context.Background()); err != nil {
		t.Fatalf("backfilled session rediscovered filesystem: %v", err)
	}
}
