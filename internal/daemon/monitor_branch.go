package daemon

import (
	"context"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func branchFromSession(session *protocol.Session) attngit.BranchInfo {
	return attngit.BranchInfo{Branch: protocol.Deref(session.Branch), IsWorktree: protocol.Deref(session.IsWorktree),
		MainRepo: protocol.Deref(session.MainRepo), Repository: protocol.Deref(session.Repository)}
}

func (e *monitorEntry) checkBranch(d *Daemon) {
	e.mu.Lock()
	directory := e.directory
	e.mu.Unlock()
	if directory == "" {
		return
	}
	branch, err := d.readBranchInfo(context.Background(), gitTask{Kind: gitTaskSessionIdentity, Lane: gitDeferred}, directory)
	if err != nil {
		return
	}
	e.mu.Lock()
	if e.branch == *branch {
		e.mu.Unlock()
		return
	}
	e.branch = *branch
	view := e.viewLocked()
	e.mu.Unlock()
	e.sink.Applied(view)
}
