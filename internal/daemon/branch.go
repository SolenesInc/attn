package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) doCreateWorktreeFromBranch(msg *protocol.CreateWorktreeFromBranchMessage) (string, error) {
	var createdPath string
	err := d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		var createErr error
		createdPath, createErr = d.doCreateWorktreeFromBranchProtected(protection, msg)
		return createErr
	})
	return createdPath, err
}

func (d *Daemon) doCreateWorktreeFromBranchProtected(protection foregroundCleanupProtection, msg *protocol.CreateWorktreeFromBranchMessage) (string, error) {
	mainRepo, err := d.resolveMainRepo(protection.Context(), gitTask{Kind: gitTaskWorktreeMutation, Lane: gitInteractive}, msg.MainRepo)
	if err != nil {
		return "", err
	}
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

	if hookErr := d.dispatchWorktreeBeforeCreateHooks(mainRepo, localBranch, branch, requestedPath); hookErr != nil {
		return "", hookErr
	}
	providerPath, providerBranch, handled, providerErr := d.dispatchWorktreeCreateProvider(mainRepo, localBranch, branch, requestedPath)
	if providerErr != nil {
		return "", providerErr
	}
	createdPath := providerPath
	if handled {
		localBranch = providerBranch
	} else {
		mutationErr := d.gitExecution().Run(protection.Context(), gitTask{Kind: gitTaskWorktreeMutation, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) error {
			if pruneErr := client.PruneWorktrees(ctx, mainRepo); pruneErr != nil {
				return pruneErr
			}
			if isRemote {
				createdBranch, createErr := client.CreateWorktreeFromRemoteBranch(ctx, mainRepo, branch, path)
				if createErr == nil {
					localBranch = createdBranch
				}
				return createErr
			}
			return client.CreateWorktreeFromBranch(ctx, mainRepo, branch, path)
		})
		if mutationErr != nil {
			return "", mutationErr
		}
		createdPath = path
	}
	d.registerCreatedWorktree(protection, mainRepo, createdPath, localBranch)
	return createdPath, d.dispatchWorktreeAfterCreateHooks(mainRepo, createdPath, localBranch)
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
	go func() {
		branches, err := gitValue(context.Background(), d.gitExecution(), gitTask{Kind: gitTaskBranch, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) ([]git.BranchWithCommit, error) {
			return client.ListBranchesWithCommits(ctx, msg.MainRepo)
		})
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
	}()
}

func (d *Daemon) handleGetRepoInfoWS(client *wsClient, msg *protocol.GetRepoInfoMessage) {
	go func() {
		repo := git.CanonicalizePath(msg.Repo)
		type repoInfo struct {
			currentBranch string
			commitHash    string
			commitTime    string
			defaultBranch string
			worktrees     []git.WorktreeEntry
			worktreesErr  error
		}
		var info repoInfo
		var worktrees []protocol.Worktree
		err := d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
			var infoErr error
			info, infoErr = gitValue(protection.Context(), d.gitExecution(), gitTask{Kind: gitTaskRepositoryInfo, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) (repoInfo, error) {
				currentBranch, runErr := client.GetCurrentBranch(ctx, repo)
				if runErr != nil {
					return repoInfo{}, runErr
				}
				commitHash, commitTime := client.GetHeadCommitInfo(ctx, repo)
				defaultBranch, _ := client.GetDefaultBranch(ctx, repo)
				listedWorktrees, worktreesErr := client.ObserveLiveWorktrees(ctx, repo)
				return repoInfo{currentBranch: currentBranch, commitHash: commitHash, commitTime: commitTime, defaultBranch: defaultBranch, worktrees: listedWorktrees, worktreesErr: worktreesErr}, nil
			})
			if infoErr != nil {
				return infoErr
			}
			if info.worktreesErr != nil {
				worktrees = storedWorktreesAsProtocol(d.store.ListWorktreesByRepo(repo))
				return nil
			}
			worktrees = d.reconcileListedWorktrees(protection, repo, info.worktrees)
			return nil
		})
		if err != nil {
			d.sendToClient(client, &protocol.GetRepoInfoResultMessage{
				Event:      protocol.EventGetRepoInfoResult,
				EndpointID: msg.EndpointID,
				Success:    false,
				Error:      protocol.Ptr(err.Error()),
			})
			return
		}

		if info.defaultBranch == "" {
			info.defaultBranch = "main"
		}
		d.sendToClient(client, &protocol.GetRepoInfoResultMessage{
			Event:      protocol.EventGetRepoInfoResult,
			EndpointID: msg.EndpointID,
			Info: &protocol.RepoInfo{
				Repo:              repo,
				CurrentBranch:     info.currentBranch,
				CurrentCommitHash: info.commitHash,
				CurrentCommitTime: info.commitTime,
				DefaultBranch:     info.defaultBranch,
				Worktrees:         worktrees,
			},
			Success: true,
		})
	}()
}

func (d *Daemon) handleGetDefaultBranchWS(client *wsClient, msg *protocol.GetDefaultBranchMessage) {
	go func() {
		branch, err := gitValue(context.Background(), d.gitExecution(), gitTask{Kind: gitTaskBranch, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) (string, error) {
			return client.GetDefaultBranch(ctx, msg.Repo)
		})
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
	}()
}

func (d *Daemon) handleFetchRemotesWS(client *wsClient, msg *protocol.FetchRemotesMessage) {
	go func() {
		err := d.gitExecution().Run(context.Background(), gitTask{Kind: gitTaskBranch, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) error {
			return client.FetchRemotes(ctx, msg.Repo)
		})
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
	}()
}

func (d *Daemon) handleListRemoteBranchesWS(client *wsClient, msg *protocol.ListRemoteBranchesMessage) {
	go func() {
		branches, err := gitValue(context.Background(), d.gitExecution(), gitTask{Kind: gitTaskBranch, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) ([]string, error) {
			return client.ListRemoteBranches(ctx, msg.Repo)
		})
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
	}()
}

func (d *Daemon) handleEnsureRepoWS(client *wsClient, msg *protocol.EnsureRepoMessage) {
	go func() {
		result := &protocol.WebSocketEvent{
			Event:      protocol.EventEnsureRepoResult,
			TargetPath: protocol.Ptr(msg.TargetPath),
		}

		cloned, err := gitValue(context.Background(), d.gitExecution(), gitTask{Kind: gitTaskBranch, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) (bool, error) {
			cloned, runErr := client.EnsureRepo(ctx, msg.CloneURL, msg.TargetPath)
			if runErr != nil {
				return false, runErr
			}
			if runErr = client.FetchRemotes(ctx, msg.TargetPath); runErr != nil {
				return cloned, fmt.Errorf("repo exists but fetch failed: %w", runErr)
			}
			return cloned, nil
		})
		if err != nil {
			result.Success = protocol.Ptr(false)
			result.Error = protocol.Ptr(err.Error())
			result.Cloned = protocol.Ptr(false)
			d.logf("EnsureRepo failed for %s: %v", msg.TargetPath, err)
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
	}()
}
