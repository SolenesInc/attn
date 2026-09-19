package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type WorktreeEntry struct {
	Path     string
	Branch   string
	Prunable bool
}

func ListWorktrees(repoDir string) ([]WorktreeEntry, error) {
	return defaultClient.ListWorktrees(context.Background(), repoDir)
}

func (c *Client) ListWorktrees(ctx context.Context, repoDir string) ([]WorktreeEntry, error) {
	_ = c.PruneWorktrees(ctx, repoDir)
	return c.ObserveWorktrees(ctx, repoDir)
}

func ObserveWorktrees(repoDir string) ([]WorktreeEntry, error) {
	return defaultClient.ObserveWorktrees(context.Background(), repoDir)
}

func (c *Client) ObserveWorktrees(ctx context.Context, repoDir string) ([]WorktreeEntry, error) {
	out, err := c.Output(ctx, OpWorktree, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var worktrees []WorktreeEntry
	var current WorktreeEntry

	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = WorktreeEntry{Path: CanonicalizePath(strings.TrimPrefix(line, "worktree "))}
		case strings.HasPrefix(line, "branch refs/heads/"):
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case strings.TrimSpace(line) == "prunable" || strings.HasPrefix(line, "prunable "):
			current.Prunable = true
		}
	}

	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees, nil
}

func PruneWorktrees(repoDir string) error {
	return defaultClient.PruneWorktrees(context.Background(), repoDir)
}

func (c *Client) PruneWorktrees(ctx context.Context, repoDir string) error {
	return c.NoOutput(ctx, OpWorktree, repoDir, "worktree", "prune")
}

func CreateWorktree(repoDir, branch, path string) error {
	return defaultClient.CreateWorktree(context.Background(), repoDir, branch, path)
}

func (c *Client) CreateWorktree(ctx context.Context, repoDir, branch, path string) error {
	if out, err := c.Combined(ctx, OpWorktree, repoDir, "worktree", "add", "-b", branch, CanonicalizePath(path)); err != nil {
		return fmt.Errorf("git worktree add failed: %s", out)
	}
	return nil
}

func CreateWorktreeFromPoint(repoDir, branch, path, startingFrom string) error {
	return defaultClient.CreateWorktreeFromPoint(context.Background(), repoDir, branch, path, startingFrom)
}

func (c *Client) CreateWorktreeFromPoint(ctx context.Context, repoDir, branch, path, startingFrom string) error {
	args := []string{"worktree", "add", "-b", branch, CanonicalizePath(path)}
	if startingFrom != "" {
		args = append(args, startingFrom)
	}
	if out, err := c.Combined(ctx, OpWorktree, repoDir, args...); err != nil {
		return fmt.Errorf("git worktree add failed: %s", out)
	}
	return nil
}

func EnsureDetachedWorktreeAtRevision(repoDir, path, revision string) (bool, error) {
	return defaultClient.EnsureDetachedWorktreeAtRevision(context.Background(), repoDir, path, revision)
}

func EnsureDetachedWorktreeAtRevisionWithHTTPAuthorization(repoDir, path, revision, authorization string) (bool, error) {
	return defaultClient.EnsureDetachedWorktreeAtRevisionWithHTTPAuthorization(context.Background(), repoDir, path, revision, authorization)
}

func EnsureAutomationSessionWorktree(repoDir, path, revision, authorization string, sessionPersisted bool) (bool, error) {
	return defaultClient.EnsureAutomationSessionWorktree(context.Background(), repoDir, path, revision, authorization, sessionPersisted)
}

func (c *Client) EnsureDetachedWorktreeAtRevision(ctx context.Context, repoDir, path, revision string) (bool, error) {
	return c.EnsureDetachedWorktreeAtRevisionWithHTTPAuthorization(ctx, repoDir, path, revision, "")
}

func (c *Client) EnsureDetachedWorktreeAtRevisionWithHTTPAuthorization(ctx context.Context, repoDir, path, revision, authorization string) (bool, error) {
	return c.ensureDetachedWorktreeAtRevision(ctx, repoDir, path, revision, authorization, false)
}

func (c *Client) EnsureAutomationSessionWorktree(ctx context.Context, repoDir, path, revision, authorization string, sessionPersisted bool) (bool, error) {
	return c.ensureDetachedWorktreeAtRevision(ctx, repoDir, path, revision, authorization, sessionPersisted)
}

func (c *Client) ensureDetachedWorktreeAtRevision(ctx context.Context, repoDir, path, revision, authorization string, sessionPersisted bool) (bool, error) {
	repoDir = c.ResolveMainRepoPath(ctx, repoDir)
	path = CanonicalizePath(path)
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("automation worktree path is not a directory: %s", path)
		}
		mainRepo := c.ResolveMainRepoPath(ctx, path)
		if !sameDirectory(mainRepo, repoDir) {
			return false, fmt.Errorf("automation worktree repository mismatch: got %s want %s", mainRepo, repoDir)
		}
		if sessionPersisted {
			return false, nil
		}
		head, err := c.GetHeadCommit(ctx, path)
		if err != nil {
			return false, fmt.Errorf("read automation worktree HEAD: %w", err)
		}
		if !strings.EqualFold(head, revision) {
			return false, fmt.Errorf("automation worktree revision mismatch: got %s want %s", head, revision)
		}
		if branch, err := c.Output(ctx, OpMetadata, path, "symbolic-ref", "--quiet", "HEAD"); err == nil {
			return false, fmt.Errorf("automation worktree is attached to branch %s; expected detached HEAD", strings.TrimSpace(string(branch)))
		}
		clean, err := c.IsWorktreeClean(ctx, path)
		if err != nil {
			return false, fmt.Errorf("inspect automation worktree: %w", err)
		}
		if !clean {
			return false, fmt.Errorf("automation worktree has local changes: %s", path)
		}
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect automation worktree path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("create automation worktree parent: %w", err)
	}
	_ = c.NoOutput(ctx, OpWorktree, repoDir, "worktree", "prune", "--expire", "now")
	remoteURL := ""
	if authorization != "" {
		if out, err := c.Output(ctx, OpMetadata, repoDir, "remote", "get-url", "origin"); err == nil {
			remoteURL = strings.TrimSpace(string(out))
		}
	}
	if out, err := c.combinedWithHTTPAuthorization(ctx, OpWorktree, repoDir, remoteURL, authorization, "worktree", "add", "--detach", path, revision); err != nil {
		return false, fmt.Errorf("git worktree add --detach failed: %s", strings.TrimSpace(string(out)))
	}
	return true, nil
}

func CreateWorktreeFromBranch(repoDir, branch, path string) error {
	return defaultClient.CreateWorktreeFromBranch(context.Background(), repoDir, branch, path)
}

func (c *Client) CreateWorktreeFromBranch(ctx context.Context, repoDir, branch, path string) error {
	resolvedDir, err := c.ResolveRepoDir(ctx, repoDir)
	if err != nil {
		return err
	}
	if out, err := c.Combined(ctx, OpWorktree, resolvedDir, "worktree", "add", ExpandPath(path), branch); err != nil {
		return fmt.Errorf("git worktree add failed: %s", out)
	}
	return nil
}

func CreateWorktreeFromRemoteBranch(repoDir, remoteBranch, path string) (string, error) {
	return defaultClient.CreateWorktreeFromRemoteBranch(context.Background(), repoDir, remoteBranch, path)
}

func (c *Client) CreateWorktreeFromRemoteBranch(ctx context.Context, repoDir, remoteBranch, path string) (string, error) {
	localBranch := remoteBranch
	if idx := strings.Index(remoteBranch, "/"); idx != -1 {
		localBranch = remoteBranch[idx+1:]
	}

	resolvedDir, err := c.ResolveRepoDir(ctx, repoDir)
	if err != nil {
		return "", err
	}
	if out, err := c.Combined(ctx, OpWorktree, resolvedDir, "worktree", "add", ExpandPath(path), "-b", localBranch, remoteBranch); err != nil {
		return "", fmt.Errorf("git worktree add failed: %s", out)
	}
	return localBranch, nil
}

func DeleteWorktree(repoDir, path string, force bool) error {
	return defaultClient.DeleteWorktree(context.Background(), repoDir, path, force)
}

func (c *Client) DeleteWorktree(ctx context.Context, repoDir, path string, force bool) error {
	path = CanonicalizePath(path)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if out, err := c.Combined(ctx, OpWorktree, repoDir, "worktree", "prune"); err != nil {
			return fmt.Errorf("git worktree prune failed: %s", out)
		}
		return nil
	}

	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	if out, err := c.Combined(ctx, OpWorktree, repoDir, args...); err != nil {
		return fmt.Errorf("git worktree remove failed: %s", out)
	}

	_ = c.NoOutput(ctx, OpWorktree, repoDir, "worktree", "prune")

	return nil
}

func GetMainRepoFromWorktree(worktreePath string) string {
	gitPath := filepath.Join(worktreePath, ".git")

	info, err := os.Stat(gitPath)
	if err != nil || info.IsDir() {
		return ""
	}

	content, err := os.ReadFile(gitPath)
	if err != nil {
		return ""
	}

	line := strings.TrimSpace(string(content))
	if !strings.HasPrefix(line, "gitdir: ") {
		return ""
	}

	gitdir := strings.TrimPrefix(line, "gitdir: ")

	idx := strings.Index(gitdir, "/.git/worktrees/")
	if idx == -1 {
		return ""
	}

	return gitdir[:idx]
}

func IsWorktreeClean(path string) (bool, error) {
	return defaultClient.IsWorktreeClean(context.Background(), path)
}

func (c *Client) IsWorktreeClean(ctx context.Context, path string) (bool, error) {
	out, err := c.Output(ctx, OpStatus, CanonicalizePath(path), "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(out))) == 0, nil
}

func GenerateWorktreePath(mainRepo, branch string) string {
	repoName := filepath.Base(mainRepo)
	safeBranch := strings.ReplaceAll(branch, "/", "-")
	return filepath.Join(filepath.Dir(mainRepo), repoName+"--"+safeBranch)
}

func ResolveMainRepoPath(repoPath string) string {
	return defaultClient.ResolveMainRepoPath(context.Background(), repoPath)
}

func (c *Client) ResolveMainRepoPath(ctx context.Context, repoPath string) string {
	expanded := ExpandPath(repoPath)
	if mainRepo := GetMainRepoFromWorktree(expanded); mainRepo != "" {
		return filepath.Clean(mainRepo)
	}

	resolved, err := c.ResolveRepoDir(ctx, expanded)
	if err == nil {
		return resolved
	}

	return filepath.Clean(expanded)
}

func RepositoryRoot(dir string) string {
	root, _ := defaultClient.RepositoryRoot(context.Background(), dir)
	return root
}

func RepositoryRootContext(ctx context.Context, dir string) (string, error) {
	return defaultClient.RepositoryRoot(ctx, dir)
}

func (c *Client) RepositoryRoot(ctx context.Context, dir string) (string, error) {
	current := CanonicalizePath(dir)
	for {
		if cause := context.Cause(ctx); cause != nil {
			return "", cause
		}
		info, err := os.Lstat(filepath.Join(current, ".git"))
		if err == nil {
			if !info.IsDir() {
				if main := GetMainRepoFromWorktree(current); main != "" {
					return CanonicalizePath(main), nil
				}
			}
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil
		}
		current = parent
	}
}
