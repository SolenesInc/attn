package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

type stubReopenGit struct {
	branchInfo         func(context.Context, string) (*attngit.BranchInfo, error)
	branchAvailability func(context.Context, string, string) (branchInspection, error)
}

func (g stubReopenGit) BranchInfo(ctx context.Context, directory string) (*attngit.BranchInfo, error) {
	if g.branchInfo == nil {
		return &attngit.BranchInfo{}, nil
	}
	return g.branchInfo(ctx, directory)
}

func (g stubReopenGit) BranchAvailability(
	ctx context.Context,
	repository string,
	branch string,
) (branchInspection, error) {
	if g.branchAvailability == nil {
		return branchInspection{}, errors.New("unexpected branch availability read")
	}
	return g.branchAvailability(ctx, repository, branch)
}

func TestSessionReopenResolverRejectsAChangedCloseGeneration(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-stale-generation")
	repository := t.TempDir()
	closeReopenSession(t, d, reopenSession{
		ID: "stale-generation", Directory: filepath.Join(t.TempDir(), "missing"), Branch: "feat/stale",
		Repo: repository, Agent: "codex", Resume: "conv-stale-generation",
	})
	entry := d.store.SessionLedgerEntry("stale-generation")
	key := reopenKey{SessionID: entry.ID, ClosedAt: protocol.Deref(entry.ClosedAt)}
	started := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := d.resolveClosedReopen(context.Background(), key, stubReopenGit{
			branchAvailability: func(context.Context, string, string) (branchInspection, error) {
				close(started)
				<-release
				return branchInspection{State: branchStateLocal}, nil
			},
		})
		result <- err
	}()
	<-started
	if _, reopened, err := d.store.ReopenSession(key.SessionID); err != nil || !reopened {
		t.Fatalf("change close generation = reopened %v, err %v", reopened, err)
	}
	close(release)
	if err := <-result; !errors.Is(err, errStaleReopenGeneration) {
		t.Fatalf("resolveClosedReopen() error = %v, want %v", err, errStaleReopenGeneration)
	}
}
