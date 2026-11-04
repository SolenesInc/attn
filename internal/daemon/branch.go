package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) doCreateWorktreeFromBranch(msg *protocol.CreateWorktreeFromBranchMessage) (string, error) {
	result, err := Call[protocol.WorktreeCreation](context.Background(), d.router(), *msg)
	if err == nil && result.Error != nil {
		err = errors.New(*result.Error)
	}
	return result.Path, err
}

func (d *Daemon) doCreateWorktreeFromBranchProtected(protection foregroundCleanupProtection, msg *protocol.CreateWorktreeFromBranchMessage) (string, error) {
	mainRepo, err := d.resolveMainRepo(protection.Context(), gitTask{Kind: gitTaskWorktreeMutation, Lane: gitInteractive}, msg.MainRepo)
	if err != nil {
		return "", err
	}
	d.store.MonitorWorktreeRepository(mainRepo)
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
	d.life.Go("handleCreateWorktreeFromBranchWS", func() {
		path, err := d.doCreateWorktreeFromBranch(msg)
		result := protocol.CreateWorktreeResultMessage{
			Event:     protocol.EventCreateWorktreeResult,
			RequestID: msg.RequestID,
			Path:      protocol.Ptr(path),
			Success:   err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Create worktree from branch failed for %s: %v", msg.Branch, err)
		} else {
			d.logf("Create worktree from branch succeeded: %s at %s", msg.Branch, path)
		}
		d.sendToClient(client, result)
	})
}

func (d *Daemon) handleGetRepoInfoWS(client *wsClient, msg *protocol.GetRepoInfoMessage) {
	d.life.Go("handleGetRepoInfoWS", func() {
		result, err := Call[protocol.GetRepoInfoResultMessage](context.Background(), d.router(), *msg)
		if err != nil {
			result = protocol.GetRepoInfoResultMessage{Event: protocol.EventGetRepoInfoResult, Error: protocol.Ptr(err.Error())}
		}
		result.RequestID = msg.RequestID
		d.sendToClient(client, &result)
	})
}

func (d *Daemon) readRepoInfoHere(ctx context.Context, msg protocol.GetRepoInfoMessage) (protocol.GetRepoInfoResultMessage, error) {
	repo := git.CanonicalizePath(msg.Repo)
	type repoInfo struct {
		currentBranch string
		commitHash    string
		commitTime    string
		defaultBranch string
		worktrees     []git.WorktreeEntry
		worktreesErr  error
	}
	info, err := gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskRepositoryInfo, Lane: gitInteractive}, func(ctx context.Context, client *git.Client) (repoInfo, error) {
		currentBranch, runErr := client.GetCurrentBranch(ctx, repo)
		if runErr != nil {
			return repoInfo{}, runErr
		}
		commitHash, commitTime := client.GetHeadCommitInfo(ctx, repo)
		branch, runErr := client.GetDefaultBranch(ctx, repo)
		if runErr != nil {
			return repoInfo{}, runErr
		}
		defaultBranch := currentBranch
		for _, ref := range []string{"origin/" + branch, branch} {
			exists, refErr := client.RefExists(ctx, repo, ref)
			if refErr != nil {
				return repoInfo{}, refErr
			}
			if exists {
				defaultBranch = ref
				break
			}
		}
		listedWorktrees, worktreesErr := client.ObserveLiveWorktrees(ctx, repo)
		return repoInfo{currentBranch: currentBranch, commitHash: commitHash, commitTime: commitTime, defaultBranch: defaultBranch, worktrees: listedWorktrees, worktreesErr: worktreesErr}, nil
	})
	if err != nil {
		return protocol.GetRepoInfoResultMessage{}, err
	}

	var worktrees []protocol.Worktree
	if info.worktreesErr != nil {
		worktrees = storedWorktreesAsProtocol(d.store.ListWorktreesByRepo(repo))
	} else {
		worktrees = d.listedWorktreesAsProtocol(repo, info.worktrees)
	}
	return protocol.GetRepoInfoResultMessage{
		Event: protocol.EventGetRepoInfoResult,
		Info: &protocol.RepoInfo{
			Repo:              repo,
			CurrentBranch:     info.currentBranch,
			CurrentCommitHash: info.commitHash,
			CurrentCommitTime: info.commitTime,
			DefaultBranch:     info.defaultBranch,
			Worktrees:         worktrees,
		},
		Success: true,
	}, nil
}

func (d *Daemon) handleFetchRemotesWS(client *wsClient, msg *protocol.FetchRemotesMessage) {
	d.life.Go("handleFetchRemotesWS", func() {
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
	})
}

func (d *Daemon) handleEnsureRepoWS(client *wsClient, msg *protocol.EnsureRepoMessage) {
	d.life.Go("handleEnsureRepoWS", func() {
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
	})
}
