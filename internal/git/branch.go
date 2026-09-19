package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

type BranchWithCommit struct {
	Name       string
	CommitHash string
	CommitTime string
	IsCurrent  bool
}

func (b BranchWithCommit) ToProtocol() protocol.Branch {
	return protocol.Branch{
		Name:       b.Name,
		CommitHash: &b.CommitHash,
		CommitTime: &b.CommitTime,
		IsCurrent:  &b.IsCurrent,
	}
}

func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func ListBranches(repoDir string) ([]string, error) {
	return defaultClient.ListBranches(context.Background(), repoDir)
}

func (c *Client) ListBranches(ctx context.Context, repoDir string) ([]string, error) {
	out, err := c.Output(ctx, OpMetadata, repoDir, "branch", "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("git branch failed: %w", err)
	}

	allBranches := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(allBranches) == 1 && allBranches[0] == "" {
		return nil, nil
	}

	worktrees, err := c.ListWorktrees(ctx, repoDir)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}

	checkedOut := make(map[string]bool)
	for _, wt := range worktrees {
		if wt.Branch != "" {
			checkedOut[wt.Branch] = true
		}
	}

	var available []string
	for _, branch := range allBranches {
		if !checkedOut[branch] {
			available = append(available, branch)
		}
	}

	return available, nil
}

func ListBranchesWithCommits(repoDir string) ([]BranchWithCommit, error) {
	return defaultClient.ListBranchesWithCommits(context.Background(), repoDir)
}

func (c *Client) ListBranchesWithCommits(ctx context.Context, repoDir string) ([]BranchWithCommit, error) {
	currentBranch, _ := c.GetCurrentBranch(ctx, repoDir)

	out, err := c.Output(ctx, OpMetadata, repoDir, "branch", "--format=%(refname:short)|%(committerdate:iso-strict)|%(objectname:short)")
	if err != nil {
		return nil, fmt.Errorf("git branch failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}

	worktrees, err := c.ListWorktrees(ctx, repoDir)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}

	checkedOut := make(map[string]bool)
	for _, wt := range worktrees {
		if wt.Branch != "" {
			checkedOut[wt.Branch] = true
		}
	}

	var result []BranchWithCommit
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) < 3 {
			continue
		}
		name := parts[0]
		if checkedOut[name] {
			continue
		}
		result = append(result, BranchWithCommit{
			Name:       name,
			CommitTime: parts[1],
			CommitHash: parts[2],
			IsCurrent:  name == currentBranch,
		})
	}

	return result, nil
}

func DeleteBranch(repoDir, branch string, force bool) error {
	return defaultClient.DeleteBranch(context.Background(), repoDir, branch, force)
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

func SwitchBranch(repoDir, branch string) error {
	return defaultClient.SwitchBranch(context.Background(), repoDir, branch)
}

func (c *Client) SwitchBranch(ctx context.Context, repoDir, branch string) error {
	if out, err := c.Combined(ctx, OpWorktree, repoDir, "checkout", branch); err != nil {
		return fmt.Errorf("git checkout failed: %s", out)
	}
	return nil
}

func CreateBranch(repoDir, branch string) error {
	return defaultClient.CreateBranch(context.Background(), repoDir, branch)
}

func (c *Client) CreateBranch(ctx context.Context, repoDir, branch string) error {
	if out, err := c.Combined(ctx, OpMetadata, repoDir, "branch", branch); err != nil {
		return fmt.Errorf("git branch failed: %s", out)
	}
	return nil
}

func GetCurrentBranch(repoDir string) (string, error) {
	return defaultClient.GetCurrentBranch(context.Background(), repoDir)
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

func ListRemotes(repoDir string) ([]string, error) {
	return defaultClient.ListRemotes(context.Background(), repoDir)
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

func RefExists(repoDir, ref string) bool {
	exists, _ := defaultClient.RefExists(context.Background(), repoDir, ref)
	return exists
}

func RefExistsContext(ctx context.Context, repoDir, ref string) (bool, error) {
	return defaultClient.RefExists(ctx, repoDir, ref)
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

func FetchRemoteBranch(repoDir, remote, branch string) error {
	return defaultClient.FetchRemoteBranch(context.Background(), repoDir, remote, branch)
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

func EnsurePullRequestRevision(repoDir, remote string, number int, expectedSHA, authorization string) error {
	return defaultClient.EnsurePullRequestRevision(context.Background(), repoDir, remote, number, expectedSHA, authorization)
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

func FetchRemotes(repoDir string) error {
	return defaultClient.FetchRemotes(context.Background(), repoDir)
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

func ListRemoteBranches(repoDir string) ([]string, error) {
	return defaultClient.ListRemoteBranches(context.Background(), repoDir)
}

func (c *Client) ListRemoteBranches(ctx context.Context, repoDir string) ([]string, error) {
	out, err := c.Output(ctx, OpMetadata, repoDir, "branch", "-r", "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("git branch -r failed: %w", err)
	}

	remoteBranches := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(remoteBranches) == 1 && remoteBranches[0] == "" {
		return nil, nil
	}

	localOut, err := c.Output(ctx, OpMetadata, repoDir, "branch", "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("git branch failed: %w", err)
	}

	localBranches := make(map[string]bool)
	for _, b := range strings.Split(strings.TrimSpace(string(localOut)), "\n") {
		if b != "" {
			localBranches[b] = true
		}
	}

	var available []string
	for _, remote := range remoteBranches {
		if strings.Contains(remote, "HEAD") {
			continue
		}
		name := strings.TrimPrefix(remote, "origin/")
		if !localBranches[name] {
			available = append(available, name)
		}
	}

	return available, nil
}

func CheckoutBranch(repoDir, branch string) error {
	return defaultClient.CheckoutBranch(context.Background(), repoDir, branch)
}

func (c *Client) CheckoutBranch(ctx context.Context, repoDir, branch string) error {
	if _, err := c.Combined(ctx, OpWorktree, repoDir, "checkout", branch); err == nil {
		return nil
	}

	if out, err := c.Combined(ctx, OpWorktree, repoDir, "checkout", "-b", branch, "origin/"+branch); err != nil {
		return fmt.Errorf("git checkout failed: %s", out)
	}
	return nil
}

func GetHeadCommitInfo(repoDir string) (hash string, time string) {
	return defaultClient.GetHeadCommitInfo(context.Background(), repoDir)
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

func GetDefaultBranch(repoDir string) (string, error) {
	return defaultClient.GetDefaultBranch(context.Background(), repoDir)
}

func GetDefaultBranchContext(ctx context.Context, repoDir string) (string, error) {
	return defaultClient.GetDefaultBranch(ctx, repoDir)
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
		parts := strings.Split(ref, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1], nil
		}
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
