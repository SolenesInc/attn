package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func newMachineReopenVerdict(target protocol.ReopenTarget) sessionReopenVerdict {
	return sessionReopenVerdict{
		SessionID:      target.SessionID,
		Execution:      garden.Dispatch{Cwd: target.Cwd, Agent: target.Agent, Branch: target.Branch, RepositoryRoot: target.Repository, RepositorySubdir: target.RepositorySubdir},
		Resume:         target.Conversation,
		DirectoryState: directoryUnknown,
	}
}

func (d *Daemon) inspectReopenHere(ctx context.Context, msg protocol.ReopenInspectMessage) (protocol.ReopenInspection, error) {
	verdict := newMachineReopenVerdict(msg.Target)
	verdict.Link, verdict.BranchMerged = msg.Link, msg.BranchMerged
	if strings.TrimSpace(msg.Target.Cwd) != "" {
		checkout, err := d.inspectCheckout(ctx, protocol.CheckoutInspectMessage{Cwd: msg.Target.Cwd})
		switch {
		case err != nil:
			verdict.DirectoryState = directoryUnavailable
			verdict.Reason = fmt.Sprintf("its directory cannot be opened: %v", err)
		case checkout.Exists && !checkout.IsDirectory:
			verdict.DirectoryState = directoryUnavailable
		case !checkout.Exists:
			verdict.DirectoryState = directoryMissing
		default:
			verdict.DirectoryState = directoryPresent
			if current, saved := strings.TrimSpace(checkout.Branch), strings.TrimSpace(msg.Target.Branch); current != "" && saved != "" && current != saved {
				verdict.Warning = fmt.Sprintf("it is on branch %s now; the session ran on %s", current, saved)
			}
		}
	}
	if err := d.decideReopenPlace(ctx, &verdict, msg.HasLaunchIntent, d.scheduledReopenGit()); err != nil {
		return protocol.ReopenInspection{}, err
	}
	return protocol.ReopenInspection{Verdict: *verdict.toProtocol(), RecreatePath: verdict.RecreatePath, Remote: verdict.Inspection.Remote, StaleRegistration: verdict.Inspection.StaleRegistration}, nil
}

func (d *Daemon) recreateReopenHere(ctx context.Context, msg protocol.ReopenWorktreeMessage) (protocol.ReopenWorktreeResult, error) {
	verdict := newMachineReopenVerdict(msg.Target)
	verdict.RecreatePath = msg.RecreatePath
	verdict.Inspection = branchInspection{Remote: msg.Remote, StaleRegistration: msg.StaleRegistration}
	var result protocol.ReopenWorktreeResult
	err := d.worktreeMaintenance.ProtectFromAutomaticCleanup(ctx, func(protection foregroundCleanupProtection) error {
		path, err := d.recreateReopenWorktreeProtected(protection, &verdict, msg.Action)
		if err != nil {
			return err
		}
		execution := verdict.Execution
		execution.Cwd = reopenDirectoryInsideWorktree(path, execution)
		if available, reason := d.reopenConversation(execution, verdict.Resume, false); !available {
			return fmt.Errorf("cannot reopen after restoring the worktree: %s", reason)
		}
		result = protocol.ReopenWorktreeResult{Path: path, Cwd: execution.Cwd}
		return nil
	})
	return result, err
}
