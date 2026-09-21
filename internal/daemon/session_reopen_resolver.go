package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

var errStaleReopenGeneration = errors.New("stale reopen generation")

type reopenKey struct {
	SessionID string
	ClosedAt  string
}

type reopenBranchKey struct {
	Repository string
	Branch     string
	Lane       gitLane
}

type reopenGit interface {
	BranchInfo(context.Context, string) (*attngit.BranchInfo, error)
	BranchAvailability(context.Context, string, string) (branchInspection, error)
}

type sessionReopenResolver struct {
	daemon *Daemon
}

func (r sessionReopenResolver) ResolveEntry(
	ctx context.Context,
	entry protocol.SessionLedgerEntry,
	gitView reopenGit,
) (sessionReopenVerdict, error) {
	verdict := sessionReopenVerdict{
		SessionID: entry.ID,
		Entry:     &entry,
		Execution: r.daemon.reopenExecutionFromLedger(&entry),
		Live:      protocol.Deref(entry.ClosedAt) == "",
	}
	verdict.DirectoryState = inspectContinuationDirectory(verdict.Execution)
	r.daemon.planReopenPlacement(&verdict)

	if verdict.Live {
		verdict.Reason = fmt.Sprintf("session %s is running; focus it instead of reopening it", entry.ID)
		return verdict, nil
	}
	if !decideReopenHost(&verdict, r.daemon.endpointInfos()) {
		return verdict, nil
	}
	_, hasLaunchIntent := r.daemon.store.LaunchIntent(entry.ID)
	if err := r.daemon.decideReopenPlace(ctx, &verdict, hasLaunchIntent, gitView); err != nil {
		return sessionReopenVerdict{}, err
	}
	return verdict, nil
}

func (r sessionReopenResolver) ResolveClosed(
	ctx context.Context,
	key reopenKey,
	gitView reopenGit,
) (sessionReopenVerdict, error) {
	entry := r.daemon.store.SessionLedgerEntry(strings.TrimSpace(key.SessionID))
	if !reopenGenerationMatches(entry, key) {
		return sessionReopenVerdict{}, staleReopenGenerationError(key)
	}
	verdict, err := r.ResolveEntry(ctx, *entry, gitView)
	if err != nil {
		return sessionReopenVerdict{}, err
	}
	if !reopenGenerationMatches(r.daemon.store.SessionLedgerEntry(key.SessionID), key) {
		return sessionReopenVerdict{}, staleReopenGenerationError(key)
	}
	return verdict, nil
}

func reopenGenerationMatches(entry *protocol.SessionLedgerEntry, key reopenKey) bool {
	return entry != nil && entry.ID == key.SessionID &&
		strings.TrimSpace(protocol.Deref(entry.ClosedAt)) == strings.TrimSpace(key.ClosedAt) &&
		strings.TrimSpace(key.ClosedAt) != ""
}

func staleReopenGenerationError(key reopenKey) error {
	return fmt.Errorf("%w for session %s closed at %s", errStaleReopenGeneration, key.SessionID, key.ClosedAt)
}

type scheduledReopenGit struct {
	daemon  *Daemon
	lane    gitLane
	inspect func(context.Context, *attngit.Client, string, string) (branchInspection, error)
}

func (d *Daemon) scheduledReopenGit(lane gitLane) scheduledReopenGit {
	d.reopenGitMu.Lock()
	inspect := d.reopenInspect
	d.reopenGitMu.Unlock()
	if inspect == nil {
		inspect = inspectReopenBranchAdmitted
	}
	return scheduledReopenGit{daemon: d, lane: lane, inspect: inspect}
}

func (g scheduledReopenGit) BranchInfo(ctx context.Context, directory string) (*attngit.BranchInfo, error) {
	return gitValue(ctx, g.daemon.gitExecution(), gitTask{
		Kind: gitTaskReopen, Lane: g.lane, Effect: gitRead, Scope: directory,
	}, func(runCtx context.Context, client *attngit.Client) (*attngit.BranchInfo, error) {
		return client.GetBranchInfo(runCtx, directory)
	})
}

func (g scheduledReopenGit) BranchAvailability(
	ctx context.Context,
	repository string,
	branch string,
) (branchInspection, error) {
	if _, err := os.Stat(repository); err != nil {
		if os.IsNotExist(err) {
			return branchInspection{State: branchStateGone, RepoMissing: true}, nil
		}
		return branchInspection{}, fmt.Errorf("inspect repository %s: %w", repository, err)
	}
	key := reopenBranchKey{
		Repository: attngit.CanonicalizePath(repository),
		Branch:     strings.TrimSpace(branch),
		Lane:       g.lane,
	}
	return g.daemon.reopenBranchSharedCalls().Do(ctx, key, func(sharedCtx context.Context) (branchInspection, error) {
		return gitValue(sharedCtx, g.daemon.gitExecution(), gitTask{
			Kind: gitTaskReopen, Lane: g.lane, Effect: gitRead, Scope: key.Repository,
		}, func(runCtx context.Context, client *attngit.Client) (branchInspection, error) {
			return g.inspect(runCtx, client, key.Repository, key.Branch)
		})
	})
}

func (d *Daemon) reopenBranchSharedCalls() *sharedCalls[reopenBranchKey, branchInspection] {
	d.reopenGitMu.Lock()
	defer d.reopenGitMu.Unlock()
	if d.reopenBranches == nil {
		d.reopenBranches = newSharedCalls[reopenBranchKey, branchInspection](context.Background())
	}
	return d.reopenBranches
}

func inspectReopenBranchAdmitted(
	ctx context.Context,
	client *attngit.Client,
	repository string,
	branch string,
) (branchInspection, error) {
	inspection := branchInspection{State: branchStateGone}
	exists, err := client.RefExists(ctx, repository, branch)
	if err != nil {
		return branchInspection{}, err
	}
	if exists {
		inspection.State = branchStateLocal
	} else {
		remotes, listErr := client.ListRemotes(ctx, repository)
		if listErr != nil {
			return branchInspection{}, fmt.Errorf("read remotes of %s: %w", repository, listErr)
		}
		for _, remote := range remotes {
			remoteExists, refErr := client.RefExists(ctx, repository, remote+"/"+branch)
			if refErr != nil {
				return branchInspection{}, refErr
			}
			if !remoteExists {
				continue
			}
			inspection.State = branchStateRemoteOnly
			inspection.Remote = remote
			break
		}
	}
	worktrees, err := client.ObserveWorktrees(ctx, repository)
	if err != nil {
		return branchInspection{}, fmt.Errorf("read worktrees of %s: %w", repository, err)
	}
	for _, worktree := range worktrees {
		if strings.TrimSpace(worktree.Branch) != branch {
			continue
		}
		if worktree.Prunable {
			inspection.StaleRegistration = true
			continue
		}
		inspection.AlreadyCheckedOut = true
	}
	return inspection, nil
}
