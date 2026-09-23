package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
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

func TestSessionReopenResolverPolicyMatrix(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-resolver-matrix")
	present := t.TempDir()
	closeReopenSession(t, d, reopenSession{
		ID: "present-without-conversation", Directory: present, Agent: "codex",
	})
	plainMissing := filepath.Join(t.TempDir(), "plain-missing")
	closeReopenSession(t, d, reopenSession{
		ID: "plain-missing", Directory: plainMissing, Agent: "codex", Resume: "conv-resolver-matrix",
	})
	repository := t.TempDir()
	worktreeMissing := filepath.Join(t.TempDir(), "worktree-missing")
	closeReopenSession(t, d, reopenSession{
		ID: "repository-missing", Directory: worktreeMissing, Branch: "feat/missing",
		Repo: repository, Agent: "codex", Resume: "conv-resolver-matrix",
	})

	gitView := stubReopenGit{branchAvailability: func(context.Context, string, string) (branchInspection, error) {
		return branchInspection{State: branchStateGone, RepoMissing: true}, nil
	}}
	cases := []struct {
		sessionID string
		actions   []protocol.SessionReopenAction
		reason    string
	}{
		{
			sessionID: "present-without-conversation",
			actions:   []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshSamePlace},
			reason:    "nothing to resume",
		},
		{
			sessionID: "plain-missing",
			actions:   []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere},
			reason:    "not a worktree",
		},
		{
			sessionID: "repository-missing",
			reason:    "repository is gone",
		},
	}
	for _, tc := range cases {
		t.Run(tc.sessionID, func(t *testing.T) {
			entry := d.store.SessionLedgerEntry(tc.sessionID)
			verdict, err := d.resolveReopen(context.Background(), *entry, gitView)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := actionNames(verdict.Actions), actionNames(tc.actions); !slices.Equal(got, want) {
				t.Errorf("actions = %v, want %v", got, want)
			}
			if !strings.Contains(verdict.Reason, tc.reason) {
				t.Errorf("reason = %q, want it to contain %q", verdict.Reason, tc.reason)
			}
		})
	}
}

func TestSessionReopenResolverDistinguishesOperationalFailureFromRefusal(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-resolver-failure")
	repository := t.TempDir()
	closeReopenSession(t, d, reopenSession{
		ID: "resolver-failure", Directory: filepath.Join(t.TempDir(), "missing"), Branch: "feat/failure",
		Repo: repository, Agent: "codex", Resume: "conv-resolver-failure",
	})
	entry := d.store.SessionLedgerEntry("resolver-failure")
	errGitUnavailable := errors.New("git unavailable")
	_, err := d.resolveReopen(context.Background(), *entry, stubReopenGit{
		branchAvailability: func(context.Context, string, string) (branchInspection, error) {
			return branchInspection{}, errGitUnavailable
		},
	})
	if !errors.Is(err, errGitUnavailable) {
		t.Fatalf("operational error = %v, want %v", err, errGitUnavailable)
	}

	verdict, err := d.resolveReopen(context.Background(), *entry, stubReopenGit{
		branchAvailability: func(context.Context, string, string) (branchInspection, error) {
			return branchInspection{State: branchStateGone, RepoMissing: true}, nil
		},
	})
	if err != nil {
		t.Fatalf("policy refusal returned an operational error: %v", err)
	}
	if verdict.Reopenable || len(verdict.Actions) != 0 || !strings.Contains(verdict.Reason, "repository is gone") {
		t.Fatalf("refusal verdict = %+v", verdict)
	}
}

func TestSessionReopenResolverKeepsAnAdvisoryBranchWarningFailureNonfatal(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-warning-failure")
	closeReopenSession(t, d, reopenSession{
		ID: "warning-failure", Directory: t.TempDir(), Branch: "feat/saved",
		Agent: "codex", Resume: "conv-warning-failure",
	})
	entry := d.store.SessionLedgerEntry("warning-failure")
	verdict, err := d.resolveReopen(context.Background(), *entry, stubReopenGit{
		branchInfo: func(context.Context, string) (*attngit.BranchInfo, error) {
			return nil, errors.New("branch warning unavailable")
		},
	})
	if err != nil {
		t.Fatalf("advisory warning read blocked eligibility: %v", err)
	}
	if !verdict.Reopenable || !verdict.offers(protocol.SessionReopenActionReopen) {
		t.Fatalf("verdict = %+v, want reopen preserved without the warning", verdict)
	}
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
