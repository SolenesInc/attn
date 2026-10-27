package daemon

import (
	"context"
	"fmt"
	"os"
	"strings"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

type reopenBranchKey struct {
	Repository string
	Branch     string
}

type reopenGit interface {
	BranchAvailability(context.Context, string, string) (branchInspection, error)
}

func (d *Daemon) resolveReopen(
	ctx context.Context,
	entry protocol.SessionLedgerEntry,
) (sessionReopenVerdict, error) {
	verdict := sessionReopenVerdict{
		SessionID:      entry.ID,
		Entry:          &entry,
		DirectoryState: directoryUnknown,
		Execution:      d.reopenExecutionFromLedger(&entry),
		Resume:         strings.TrimSpace(d.store.GetResumeSessionID(entry.ID)),
		Live:           protocol.Deref(entry.ClosedAt) == "" && d.sessionActive(entry.ID),
	}
	d.planReopenProfile(&verdict)

	if verdict.Live {
		verdict.Reason = fmt.Sprintf("session %s is running; focus it instead of reopening it", entry.ID)
		return verdict, nil
	}
	if verdict.ProfileDeleted {
		verdict.Reason = fmt.Sprintf("profile %s was deleted; this session cannot reopen in another profile", verdict.ProfileID)
		return verdict, nil
	}
	_, hasLaunchIntent := d.store.LaunchIntent(entry.ID)
	inspection, err := Call[protocol.ReopenInspection](ctx, d.router(), protocol.ReopenInspectMessage{
		Cmd: protocol.CmdReopenInspect, Host: protocol.Deref(entry.Host), SessionID: entry.ID,
		Cwd: verdict.Execution.Cwd, Agent: verdict.Execution.Agent, Conversation: verdict.Resume,
		Branch: verdict.Execution.Branch, Repository: verdict.Execution.RepositoryRoot, RepositorySubdir: verdict.Execution.RepositorySubdir,
		Link: d.store.SessionLink(entry.ID), HasLaunchIntent: hasLaunchIntent, BranchMerged: d.branchMerged(entry.ID, verdict.Execution.Branch),
	})
	if err != nil {
		return sessionReopenVerdict{}, err
	}
	machine := inspection.Verdict
	verdict.Reopenable, verdict.Actions = machine.Reopenable, machine.Actions
	verdict.DirectoryState, verdict.BranchState = machine.DirectoryState, protocol.Deref(machine.BranchState)
	verdict.Reason, verdict.Warning = protocol.Deref(machine.Reason), protocol.Deref(machine.Warning)
	verdict.RecreatePath = inspection.RecreatePath
	verdict.Inspection = branchInspection{State: verdict.BranchState, Remote: inspection.Remote, StaleRegistration: inspection.StaleRegistration}
	if protocol.Deref(entry.Host) != "" && verdict.DirectoryState == directoryMissing {
		verdict.Actions = nil
		verdict.Reason = fmt.Sprintf("its directory %s no longer exists on outpost %s", verdict.Execution.Cwd, protocol.Deref(entry.Host))
	}
	return verdict, nil
}

type scheduledReopenGit struct {
	daemon  *Daemon
	inspect func(context.Context, *attngit.Client, string, string) (branchInspection, error)
}

var reopenGitTask = gitTask{Kind: gitTaskReopen, Lane: gitInteractive}

func (d *Daemon) scheduledReopenGit() scheduledReopenGit {
	d.reopenGitMu.Lock()
	inspect := d.reopenInspect
	d.reopenGitMu.Unlock()
	if inspect == nil {
		inspect = inspectReopenBranchAdmitted
	}
	return scheduledReopenGit{daemon: d, inspect: inspect}
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
	}
	return g.daemon.reopenBranchSharedCalls().Do(ctx, key, func(sharedCtx context.Context) (branchInspection, error) {
		return gitValue(sharedCtx, g.daemon.gitExecution(), reopenGitTask, func(runCtx context.Context, client *attngit.Client) (branchInspection, error) {
			return g.inspect(runCtx, client, key.Repository, key.Branch)
		})
	})
}

func (d *Daemon) reopenBranchSharedCalls() *sharedCalls[reopenBranchKey, branchInspection] {
	d.reopenGitMu.Lock()
	defer d.reopenGitMu.Unlock()
	if d.reopenBranches == nil {
		d.reopenBranches = newSharedCalls[reopenBranchKey, branchInspection](&d.life, "reopenBranch")
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
