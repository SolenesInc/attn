package daemon

import (
	"context"
	"strings"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) doCreateWorktreeFromBranch(msg *protocol.CreateWorktreeFromBranchMessage) (string, error) {
	var path string
	err := d.worktreeMaintenance.RunForeground(context.Background(), "create worktree from branch", func(context.Context) error {
		var err error
		path, err = d.doCreateWorktreeFromBranchForeground(msg)
		return err
	})
	return path, err
}

func (d *Daemon) doCreateWorktreeFromBranchForeground(msg *protocol.CreateWorktreeFromBranchMessage) (string, error) {
	mainRepo := git.ResolveMainRepoPath(msg.MainRepo)
	branch := msg.Branch

	localBranch := branch
	isRemote := strings.HasPrefix(branch, "origin/")
	if isRemote {
		localBranch = strings.TrimPrefix(branch, "origin/")
	}

	requestedPath := protocol.Deref(msg.Path)
	path := requestedPath
	if path == "" {
		path = git.GenerateWorktreePath(mainRepo, localBranch)
	}
	path = git.ExpandPath(path)

	if err := d.dispatchWorktreeBeforeCreateHooks(mainRepo, localBranch, branch, requestedPath); err != nil {
		return "", err
	}
	providerPath, providerBranch, handled, err := d.dispatchWorktreeCreateProvider(mainRepo, localBranch, branch, requestedPath)
	if err != nil {
		return "", err
	}
	if handled {
		d.registerCreatedWorktree(mainRepo, providerPath, providerBranch)
		if err := d.dispatchWorktreeAfterCreateHooks(mainRepo, providerPath, providerBranch); err != nil {
			return providerPath, err
		}
		return providerPath, nil
	}

	if isRemote {
		createdBranch, err := git.CreateWorktreeFromRemoteBranch(mainRepo, branch, path)
		if err != nil {
			return "", err
		}
		localBranch = createdBranch
	} else {
		if err := git.CreateWorktreeFromBranch(mainRepo, branch, path); err != nil {
			return "", err
		}
	}

	d.registerCreatedWorktree(mainRepo, path, localBranch)
	if err := d.dispatchWorktreeAfterCreateHooks(mainRepo, path, localBranch); err != nil {
		return path, err
	}
	return path, nil
}

func (d *Daemon) handleCreateWorktreeFromBranchWS(client *wsClient, msg *protocol.CreateWorktreeFromBranchMessage) {
	go func() {
		path, err := d.doCreateWorktreeFromBranch(msg)
		result := protocol.CreateWorktreeResultMessage{
			Event:   protocol.EventCreateWorktreeResult,
			Path:    protocol.Ptr(path),
			Success: err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Create worktree from branch failed for %s: %v", msg.Branch, err)
		} else {
			d.logf("Create worktree from branch succeeded: %s at %s", msg.Branch, path)
		}
		d.sendToClient(client, result)
	}()
}

func (d *Daemon) handleListBranchesWS(client *wsClient, msg *protocol.ListBranchesMessage) {
	go d.runWorktreeForeground("list branches", func(context.Context) {
		branches, err := git.ListBranchesWithCommits(msg.MainRepo)
		result := protocol.BranchesResultMessage{
			Event:   protocol.EventBranchesResult,
			Success: err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.Branches = make([]protocol.Branch, len(branches))
			for i, b := range branches {
				result.Branches[i] = b.ToProtocol()
			}
		}
		d.sendToClient(client, result)
	})
}

func (d *Daemon) handleGetRepoInfoWS(client *wsClient, msg *protocol.GetRepoInfoMessage) {
	go d.runWorktreeForeground("get repository info", func(ctx context.Context) {
		repo := git.CanonicalizePath(msg.Repo)

		currentBranch, err := git.GetCurrentBranch(repo)
		if err != nil {
			d.sendToClient(client, &protocol.GetRepoInfoResultMessage{
				Event:      protocol.EventGetRepoInfoResult,
				EndpointID: msg.EndpointID,
				Success:    false,
				Error:      protocol.Ptr(err.Error()),
			})
			return
		}

		commitHash, commitTime := git.GetHeadCommitInfo(repo)

		defaultBranch, _ := git.GetDefaultBranchContext(ctx, repo)
		if defaultBranch == "" {
			defaultBranch = "main"
		}

		worktrees := d.doListWorktreesForeground(repo)

		d.sendToClient(client, &protocol.GetRepoInfoResultMessage{
			Event:      protocol.EventGetRepoInfoResult,
			EndpointID: msg.EndpointID,
			Info: &protocol.RepoInfo{
				Repo:              repo,
				CurrentBranch:     currentBranch,
				CurrentCommitHash: commitHash,
				CurrentCommitTime: commitTime,
				DefaultBranch:     defaultBranch,
				Worktrees:         worktrees,
			},
			Success: true,
		})
	})
}

func (d *Daemon) handleGetDefaultBranchWS(client *wsClient, msg *protocol.GetDefaultBranchMessage) {
	go d.runWorktreeForeground("get default branch", func(ctx context.Context) {
		branch, err := git.GetDefaultBranchContext(ctx, msg.Repo)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventGetDefaultBranchResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.Branch = protocol.Ptr(branch)
		}
		d.sendToClient(client, result)
	})
}

func (d *Daemon) handleFetchRemotesWS(client *wsClient, msg *protocol.FetchRemotesMessage) {
	go d.runWorktreeForeground("fetch remotes", func(context.Context) {
		err := git.FetchRemotes(msg.Repo)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventFetchRemotesResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("FetchRemotes failed for %s: %v", msg.Repo, err)
		} else {
			d.logf("FetchRemotes succeeded for %s", msg.Repo)
		}
		d.sendToClient(client, result)
	})
}

func (d *Daemon) handleListRemoteBranchesWS(client *wsClient, msg *protocol.ListRemoteBranchesMessage) {
	go d.runWorktreeForeground("list remote branches", func(context.Context) {
		branches, err := git.ListRemoteBranches(msg.Repo)
		result := &protocol.WebSocketEvent{
			Event:   protocol.EventListRemoteBranchesResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			branchList := make([]protocol.Branch, len(branches))
			for i, b := range branches {
				branchList[i] = protocol.Branch{Name: b}
			}
			result.Branches = branchList
		}
		d.sendToClient(client, result)
	})
}

func (d *Daemon) handleEnsureRepoWS(client *wsClient, msg *protocol.EnsureRepoMessage) {
	go d.runWorktreeForeground("ensure repository", func(context.Context) {
		result := &protocol.WebSocketEvent{
			Event:      protocol.EventEnsureRepoResult,
			TargetPath: protocol.Ptr(msg.TargetPath),
		}

		cloned, err := git.EnsureRepo(msg.CloneURL, msg.TargetPath)
		if err != nil {
			result.Success = protocol.Ptr(false)
			result.Error = protocol.Ptr(err.Error())
			result.Cloned = protocol.Ptr(false)
			d.logf("EnsureRepo failed for %s: %v", msg.TargetPath, err)
			d.sendToClient(client, result)
			return
		}

		if err := git.FetchRemotes(msg.TargetPath); err != nil {
			result.Success = protocol.Ptr(false)
			result.Error = protocol.Ptr("repo exists but fetch failed: " + err.Error())
			result.Cloned = protocol.Ptr(cloned)
			d.logf("FetchRemotes failed for %s after ensure: %v", msg.TargetPath, err)
			d.sendToClient(client, result)
			return
		}

		result.Success = protocol.Ptr(true)
		result.Cloned = protocol.Ptr(cloned)
		if cloned {
			d.logf("EnsureRepo cloned %s to %s", msg.CloneURL, msg.TargetPath)
		} else {
			d.logf("EnsureRepo found existing repo at %s, fetched remotes", msg.TargetPath)
		}
		d.sendToClient(client, result)
	})
}
