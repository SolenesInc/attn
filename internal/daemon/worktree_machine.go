package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

const delegationWorktreeOwnerFile = "attn-delegation-owner"

func (d *Daemon) delegationWorktreeOwnerPath(worktreePath string) (string, error) {
	out, err := d.gitOutput(context.Background(), delegationGitTask, git.OpMetadata, worktreePath, "rev-parse", "--git-path", delegationWorktreeOwnerFile)
	if err != nil {
		return "", fmt.Errorf("resolve delegation worktree owner marker: %w", err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("resolve delegation worktree owner marker: git returned an empty path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(worktreePath, path)
	}
	return filepath.Clean(path), nil
}

func (d *Daemon) writeDelegationWorktreeOwner(worktreePath, token string) error {
	path, err := d.delegationWorktreeOwnerPath(worktreePath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("write delegation worktree owner marker: %w", err)
	}
	return nil
}

func (d *Daemon) verifyDelegationWorktreeOwner(worktreePath, token string) error {
	path, err := d.delegationWorktreeOwnerPath(worktreePath)
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read delegation worktree owner marker: %w", err)
	}
	if token == "" || strings.TrimSpace(string(contents)) != token {
		return fmt.Errorf("delegation worktree owner marker does not match")
	}
	return nil
}

func (d *Daemon) createWorktreeHere(ctx context.Context, msg protocol.CreateWorktreeMessage) (protocol.WorktreeCreation, error) {
	return d.createOwnedWorktree(ctx, msg.MainRepo, msg.Branch, msg.Path, msg.Owner, func(protection foregroundCleanupProtection) (string, error) {
		return d.doCreateWorktreeProtected(protection, &msg)
	})
}

func (d *Daemon) createWorktreeFromBranchHere(ctx context.Context, msg protocol.CreateWorktreeFromBranchMessage) (protocol.WorktreeCreation, error) {
	return d.createOwnedWorktree(ctx, msg.MainRepo, msg.Branch, msg.Path, msg.Owner, func(protection foregroundCleanupProtection) (string, error) {
		return d.doCreateWorktreeFromBranchProtected(protection, &msg)
	})
}

func (d *Daemon) createOwnedWorktree(ctx context.Context, repo, branch string, path *string, owner *protocol.DelegationWorktreeOwner, create func(foregroundCleanupProtection) (string, error)) (protocol.WorktreeCreation, error) {
	result := protocol.WorktreeCreation{}
	err := d.worktreeMaintenance.ProtectFromAutomaticCleanup(ctx, func(protection foregroundCleanupProtection) error {
		if owner != nil {
			if err := d.checkDelegationWorktree(protection, repo, branch, protocol.Deref(path), owner, &result); err != nil || result.Path != "" {
				return err
			}
		}
		createdPath, err := create(protection)
		if createdPath == "" {
			return err
		}
		result.Path = git.CanonicalizePath(createdPath)
		if err != nil {
			return err
		}
		if owner != nil {
			if requested := protocol.Deref(path); requested != "" && result.Path != git.CanonicalizePath(requested) {
				return fmt.Errorf("worktree provider returned %s, expected requested path %s; checkout was left in place", result.Path, requested)
			}
		}
		result.Branch, _ = gitValue(ctx, d.gitExecution(), delegationGitTask, func(ctx context.Context, client *git.Client) (string, error) {
			return client.GetCurrentBranch(ctx, result.Path)
		})
		if owner != nil && result.Branch != branch {
			return fmt.Errorf("worktree provider returned branch %q at %s, expected %q; checkout was left in place", result.Branch, result.Path, branch)
		}
		if owner != nil {
			return d.writeDelegationWorktreeOwner(result.Path, owner.Token)
		}
		return nil
	})
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	return result, nil
}

func (d *Daemon) checkDelegationWorktree(protection foregroundCleanupProtection, repo, branch, path string, owner *protocol.DelegationWorktreeOwner, result *protocol.WorktreeCreation) error {
	if path == "" {
		path = git.CanonicalizePath(git.GenerateWorktreePath(repo, branch))
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect delegated worktree path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("worktree path already exists and is not branch %q: %s", branch, path)
	}
	if pathRepo, pathErr := d.resolveMainRepo(protection.Context(), delegationGitTask, path); pathErr == nil && git.CanonicalizePath(pathRepo) != git.CanonicalizePath(repo) {
		return fmt.Errorf("repository placement conflict: selected repository resolves to %s, but --worktree-path resolves to %s; choose a worktree path from the selected repository", repo, pathRepo)
	}
	if owner.Recovering {
		if err := d.verifyDelegationWorktreeOwner(path, owner.Token); err != nil {
			return fmt.Errorf("worktree %s appeared while delegation preparation was interrupted; ownership cannot be proven (%v), so it was left untouched", path, err)
		}
		wt := d.readWorktreeFromGit(protection.Context(), path)
		if wt == nil || strings.TrimSpace(wt.Branch) != branch {
			return fmt.Errorf("worktree path already exists and is not branch %q: %s", branch, path)
		}
		result.Path, result.Branch = path, branch
		return nil
	}
	return fmt.Errorf("worktree %s already exists; creation cannot be reinterpreted as checkout reuse", path)
}
