package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) inspectReopenHere(ctx context.Context, msg protocol.ReopenInspectMessage) (protocol.ReopenInspection, error) {
	verdict := sessionReopenVerdict{
		SessionID: msg.SessionID,
		Execution: garden.Dispatch{Cwd: msg.Cwd, Agent: msg.Agent, Branch: msg.Branch, RepositoryRoot: msg.Repository, RepositorySubdir: msg.RepositorySubdir},
		Resume:    msg.Conversation, Link: msg.Link, BranchMerged: msg.BranchMerged,
		DirectoryState: directoryUnknown,
	}
	if strings.TrimSpace(msg.Cwd) != "" {
		checkout, err := d.inspectCheckout(ctx, protocol.CheckoutInspectMessage{Cwd: msg.Cwd})
		switch {
		case err != nil || checkout.Exists && !checkout.IsDirectory:
			verdict.DirectoryState = directoryUnavailable
		case !checkout.Exists:
			verdict.DirectoryState = directoryMissing
		default:
			verdict.DirectoryState = directoryPresent
			if current, saved := strings.TrimSpace(checkout.Branch), strings.TrimSpace(msg.Branch); current != "" && saved != "" && current != saved {
				verdict.Warning = fmt.Sprintf("it is on branch %s now; the session ran on %s", current, saved)
			}
		}
	}
	if err := d.decideReopenPlace(ctx, &verdict, msg.HasLaunchIntent, d.scheduledReopenGit()); err != nil {
		return protocol.ReopenInspection{}, err
	}
	return protocol.ReopenInspection{Verdict: *verdict.toProtocol(), RecreatePath: verdict.RecreatePath, Remote: verdict.Inspection.Remote, StaleRegistration: verdict.Inspection.StaleRegistration}, nil
}
