package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func (c *Client) DeleteBranch(ctx context.Context, repoDir, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}

	if out, err := c.Combined(ctx, OpMetadata, repoDir, "branch", flag, branch); err != nil {
		return fmt.Errorf("git branch %s failed: %s", flag, out)
	}
	return nil
}

func (c *Client) SwitchBranch(ctx context.Context, repoDir, branch string) error {
	if out, err := c.Combined(ctx, OpWorktree, repoDir, "checkout", branch); err != nil {
		return fmt.Errorf("git checkout failed: %s", out)
	}
	return nil
}

func (c *Client) CreateBranch(ctx context.Context, repoDir, branch string) error {
	if out, err := c.Combined(ctx, OpMetadata, repoDir, "branch", branch); err != nil {
		return fmt.Errorf("git branch failed: %s", out)
	}
	return nil
}

func (c *Client) GetCurrentBranch(ctx context.Context, repoDir string) (string, error) {
	out, err := c.Output(ctx, OpMetadata, repoDir, "symbolic-ref", "--short", "HEAD")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	out, err = c.Output(ctx, OpMetadata, repoDir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git current branch failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Client) ListRemotes(ctx context.Context, repoDir string) ([]string, error) {
	resolvedDir, err := c.ResolveRepoDir(ctx, repoDir)
	if err != nil {
		return nil, err
	}
	out, err := c.Output(ctx, OpMetadata, resolvedDir, "remote")
	if err != nil {
		return nil, fmt.Errorf("git remote failed: %w", err)
	}
	var remotes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			remotes = append(remotes, line)
		}
	}
	return remotes, nil
}

func (c *Client) RefExists(ctx context.Context, repoDir, ref string) (bool, error) {
	resolvedDir, err := c.ResolveRepoDir(ctx, repoDir)
	if err != nil {
		return false, err
	}
	err = c.NoOutput(ctx, OpMetadata, resolvedDir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if cause := context.Cause(ctx); cause != nil {
		return false, cause
	}
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func (c *Client) FetchRemoteBranch(ctx context.Context, repoDir, remote, branch string) error {
	resolvedDir, err := c.ResolveRepoDir(ctx, repoDir)
	if err != nil {
		return err
	}
	if out, err := c.Combined(ctx, OpNetwork, resolvedDir, "fetch", remote, branch); err != nil {
		outStr := strings.TrimSpace(string(out))
		if outStr == "" {
			return fmt.Errorf("git fetch failed: %w", err)
		}
		return fmt.Errorf("git fetch failed: %s (%w)", outStr, err)
	}
	return nil
}

func (c *Client) EnsurePullRequestRevision(ctx context.Context, repoDir, remote string, number int, expectedSHA, authorization string) error {
	resolvedDir, err := c.ResolveRepoDir(ctx, repoDir)
	if err != nil {
		return err
	}
	ref := fmt.Sprintf("refs/pull/%d/head", number)
	if exists, _ := c.RefExists(ctx, resolvedDir, expectedSHA); exists {
		return nil
	}
	remoteURL, err := c.Output(ctx, OpMetadata, resolvedDir, "remote", "get-url", remote)
	if err != nil {
		return fmt.Errorf("read pull request remote: %w", err)
	}
	if missingHTTPSAuthorization(string(remoteURL), authorization) {
		return ErrAuthorizationMissing
	}
	authorization, err = authorizationForGitURL(strings.TrimSpace(string(remoteURL)), authorization)
	if err != nil {
		return err
	}
	if out, err := c.combinedWithHTTPAuthorization(ctx, OpNetwork, resolvedDir, strings.TrimSpace(string(remoteURL)), authorization, "fetch", "--no-tags", remote, ref); err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("fetch pull request head: %s", message)
	}
	if exists, _ := c.RefExists(ctx, resolvedDir, expectedSHA); !exists {
		return fmt.Errorf("snapshotted pull request revision %s is unavailable after fetching %s", expectedSHA, ref)
	}
	return nil
}

func (c *Client) FetchRemotes(ctx context.Context, repoDir string) error {
	resolvedDir, err := c.ResolveRepoDir(ctx, repoDir)
	if err != nil {
		return err
	}
	if out, err := c.Combined(ctx, OpNetwork, resolvedDir, "fetch", "--all", "--prune"); err != nil {
		outStr := strings.TrimSpace(string(out))
		if outStr == "" {
			return fmt.Errorf("git fetch failed: %w", err)
		}
		return fmt.Errorf("git fetch failed: %s (%w)", outStr, err)
	}
	return nil
}

func (c *Client) GetHeadCommitInfo(ctx context.Context, repoDir string) (hash string, time string) {
	out, err := c.Output(ctx, OpMetadata, repoDir, "log", "-1", "--format=%h|%cI")
	if err != nil {
		return "", ""
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

func (c *Client) GetDefaultBranch(ctx context.Context, repoDir string) (string, error) {
	if out, err := c.Output(ctx, OpMetadata, repoDir, "config", "--get", "attn.baseBranch"); err == nil {
		if branch := strings.TrimSpace(string(out)); branch != "" {
			return branch, nil
		}
	} else if cause := context.Cause(ctx); cause != nil {
		return "", cause
	}
	out, err := c.Output(ctx, OpMetadata, repoDir, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err == nil {
		ref := strings.TrimSpace(string(out))
		return strings.TrimPrefix(ref, "refs/remotes/origin/"), nil
	} else if cause := context.Cause(ctx); cause != nil {
		return "", cause
	}

	for _, branch := range []string{"main", "master"} {
		if err := c.NoOutput(ctx, OpMetadata, repoDir, "rev-parse", "--verify", branch); err == nil {
			return branch, nil
		} else if cause := context.Cause(ctx); cause != nil {
			return "", cause
		}
	}

	return "main", nil
}
