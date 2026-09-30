package git

import (
	"context"
	"encoding/hex"
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

func (c *Client) GetBranchInfo(ctx context.Context, dir string) (*BranchInfo, error) {
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"} {
		if os.Getenv(name) != "" {
			return c.branchInfoFromGit(ctx, dir)
		}
	}
	if info, ok := c.branchInfoFromFiles(ctx, dir); ok {
		return info, nil
	}
	return c.branchInfoFromGit(ctx, dir)
}

func (c *Client) branchInfoFromFiles(ctx context.Context, dir string) (*BranchInfo, bool) {
	root := CanonicalizePath(dir)
	var gitDir string
	for {
		if context.Cause(ctx) != nil {
			return nil, false
		}
		if _, err := os.Stat(filepath.Join(root, "HEAD")); err == nil {
			if objects, err := os.Stat(filepath.Join(root, "objects")); err == nil && objects.IsDir() {
				return nil, false
			}
		}
		gitPath := filepath.Join(root, ".git")
		entry, err := os.Stat(gitPath)
		if err == nil {
			gitDir = gitPath
			if !entry.IsDir() {
				content, err := os.ReadFile(gitPath)
				if err != nil || !strings.HasPrefix(string(content), "gitdir: ") {
					return nil, false
				}
				gitDir = strings.TrimSpace(strings.TrimPrefix(string(content), "gitdir: "))
				if gitDir == "" {
					return nil, false
				}
				if !filepath.IsAbs(gitDir) {
					gitDir = filepath.Join(root, gitDir)
				}
			}
			break
		}
		if !os.IsNotExist(err) {
			return nil, false
		}
		parent := filepath.Dir(root)
		if parent == root {
			return nil, false
		}
		root = parent
	}
	gitDir = filepath.Clean(gitDir)
	commonDir := gitDir
	if content, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = strings.TrimSpace(string(content))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
	}
	if !recognizesWorktreeConfig(commonDir, root) {
		return nil, false
	}
	if _, err := os.Stat(filepath.Join(gitDir, "config.worktree")); !os.IsNotExist(err) {
		return nil, false
	}
	// Reftable's HEAD can be a stub; git owns decoding that backend.
	for _, refDir := range []string{gitDir, commonDir} {
		if _, err := os.Stat(filepath.Join(refDir, "reftable")); !os.IsNotExist(err) {
			return nil, false
		}
	}
	content, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return nil, false
	}
	head := strings.TrimSpace(string(content))
	info := &BranchInfo{}
	switch {
	case strings.HasPrefix(head, "ref: refs/heads/"):
		info.Branch = strings.TrimPrefix(head, "ref: refs/heads/")
		if info.Branch == "" || info.Branch == ".invalid" {
			return nil, false
		}
	case len(head) == 40 || len(head) == 64:
		if _, err := hex.DecodeString(head); err != nil {
			return nil, false
		}
		info.Branch = head[:7]
	default:
		return nil, false
	}
	if filepath.Base(filepath.Dir(gitDir)) == "worktrees" {
		mainGitDir := filepath.Dir(filepath.Dir(gitDir))
		if filepath.Base(mainGitDir) != ".git" {
			return nil, false
		}
		info.MainRepo = filepath.Dir(mainGitDir)
		info.IsWorktree = true
	}
	info.Repository, _ = c.RepositoryRoot(ctx, dir)
	return info, true
}

// Accept simple local config; git resolves includes and other config forms.
func recognizesWorktreeConfig(gitDir, root string) bool {
	content, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		return false
	}
	core, worktree := false, false
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			if strings.HasPrefix(strings.ToLower(line), "[include") {
				return false
			}
			core = strings.EqualFold(line, "[core]")
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		if !core {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "worktree":
			path := strings.TrimSpace(value)
			if !filepath.IsAbs(path) {
				path = filepath.Join(gitDir, path)
			}
			if CanonicalizePath(path) != root {
				return false
			}
		case "bare":
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "false", "no", "off", "0":
				worktree = true
			default:
				return false
			}
		}
	}
	return worktree
}

func (c *Client) branchInfoFromGit(ctx context.Context, dir string) (*BranchInfo, error) {
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
