package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

func CanonicalizePath(path string) string {
	expanded := ExpandPath(path)
	if resolved, err := filepath.EvalSymlinks(expanded); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(expanded)
}

type BranchInfo struct {
	Branch     string
	IsWorktree bool
	MainRepo   string
	Repository string
}

func GetBranchInfo(dir string) (*BranchInfo, error) {
	return defaultClient.GetBranchInfo(context.Background(), dir)
}

func (c *Client) GetBranchInfo(ctx context.Context, dir string) (*BranchInfo, error) {
	info := &BranchInfo{}

	if !c.isGitRepo(ctx, dir) {
		return info, nil
	}

	branch, err := c.getCurrentBranch(ctx, dir)
	if err != nil {
		return info, nil
	}
	info.Branch = branch

	mainRepo, isWT := c.getWorktreeInfo(ctx, dir)
	info.IsWorktree = isWT
	info.MainRepo = mainRepo
	info.Repository, _ = c.RepositoryRoot(ctx, dir)

	return info, nil
}

func (c *Client) isGitRepo(ctx context.Context, dir string) bool {
	out, err := c.Output(ctx, OpMetadata, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func (c *Client) getCurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := c.Output(ctx, OpMetadata, dir, "symbolic-ref", "--short", "HEAD")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	out, err = c.Output(ctx, OpMetadata, dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func GetRepoRoot(dir string) (string, error) {
	return defaultClient.GetRepoRoot(context.Background(), dir)
}

func (c *Client) GetRepoRoot(ctx context.Context, dir string) (string, error) {
	out, err := c.Output(ctx, OpMetadata, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return CanonicalizePath(strings.TrimSpace(string(out))), nil
}

func sameDirectory(left string, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr != nil || rightErr != nil {
		return filepath.Clean(left) == filepath.Clean(right)
	}
	return os.SameFile(leftInfo, rightInfo)
}

func ResolvePickerRepoTarget(dir string) (repoRoot string, ok bool, err error) {
	return defaultClient.ResolvePickerRepoTarget(context.Background(), dir)
}

func (c *Client) ResolvePickerRepoTarget(ctx context.Context, dir string) (repoRoot string, ok bool, err error) {
	resolvedDir := CanonicalizePath(dir)
	worktreeRoot, err := c.GetRepoRoot(ctx, resolvedDir)
	if err != nil || worktreeRoot == "" {
		return "", false, nil
	}
	if !sameDirectory(resolvedDir, worktreeRoot) {
		return "", false, nil
	}
	if mainRepo := GetMainRepoFromWorktree(resolvedDir); mainRepo != "" {
		return CanonicalizePath(mainRepo), true, nil
	}
	return resolvedDir, true, nil
}

func GetHeadCommit(dir string) (string, error) {
	return defaultClient.GetHeadCommit(context.Background(), dir)
}

func (c *Client) GetHeadCommit(ctx context.Context, dir string) (string, error) {
	out, err := c.Output(ctx, OpMetadata, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Client) getWorktreeInfo(ctx context.Context, dir string) (mainRepo string, isWorktree bool) {
	out, err := c.Output(ctx, OpMetadata, dir, "rev-parse", "--git-dir")
	if err != nil {
		return "", false
	}
	gitDir := strings.TrimSpace(string(out))

	if strings.Contains(gitDir, "worktrees") {
		parts := strings.Split(gitDir, ".git/worktrees")
		if len(parts) > 0 {
			mainRepo = strings.TrimSuffix(parts[0], "/")
			if !filepath.IsAbs(mainRepo) {
				mainRepo = filepath.Join(dir, mainRepo)
			}
			mainRepo = filepath.Clean(mainRepo)
		}
		return mainRepo, true
	}

	return "", false
}
