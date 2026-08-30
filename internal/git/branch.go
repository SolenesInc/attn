package git

import (
	"fmt"
	"os"
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
	out, err := runGitOutput(OpMetadata, repoDir, "branch", "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("git branch failed: %w", err)
	}

	allBranches := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(allBranches) == 1 && allBranches[0] == "" {
		return nil, nil
	}

	worktrees, err := ListWorktrees(repoDir)
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
	currentBranch, _ := GetCurrentBranch(repoDir)

	out, err := runGitOutput(OpMetadata, repoDir, "branch", "--format=%(refname:short)|%(committerdate:iso-strict)|%(objectname:short)")
	if err != nil {
		return nil, fmt.Errorf("git branch failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}

	worktrees, err := ListWorktrees(repoDir)
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
	flag := "-d"
	if force {
		flag = "-D"
	}

	if out, err := runGitCombined(OpMetadata, repoDir, "branch", flag, branch); err != nil {
		return fmt.Errorf("git branch %s failed: %s", flag, out)
	}
	return nil
}

func SwitchBranch(repoDir, branch string) error {
	if out, err := runGitCombined(OpWorktree, repoDir, "checkout", branch); err != nil {
		return fmt.Errorf("git checkout failed: %s", out)
	}
	return nil
}

func CreateBranch(repoDir, branch string) error {
	if out, err := runGitCombined(OpMetadata, repoDir, "branch", branch); err != nil {
		return fmt.Errorf("git branch failed: %s", out)
	}
	return nil
}

func GetCurrentBranch(repoDir string) (string, error) {
	out, err := runGitOutput(OpMetadata, repoDir, "symbolic-ref", "--short", "HEAD")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	out, err = runGitOutput(OpMetadata, repoDir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git current branch failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func ListRemotes(repoDir string) ([]string, error) {
	resolvedDir, err := ResolveRepoDir(repoDir)
	if err != nil {
		return nil, err
	}
	out, err := runGitOutput(OpMetadata, resolvedDir, "remote")
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

// `git worktree add` errors hard on an unknown start ref.
func RefExists(repoDir, ref string) bool {
	resolvedDir, err := ResolveRepoDir(repoDir)
	if err != nil {
		return false
	}
	return runGitNoOutput(OpMetadata, resolvedDir, "rev-parse", "--verify", "--quiet", ref+"^{commit}") == nil
}

func FetchRemoteBranch(repoDir, remote, branch string) error {
	resolvedDir, err := ResolveRepoDir(repoDir)
	if err != nil {
		return err
	}
	if out, err := runGitCombined(OpNetwork, resolvedDir, "fetch", remote, branch); err != nil {
		outStr := strings.TrimSpace(string(out))
		if outStr == "" {
			return fmt.Errorf("git fetch failed: %w", err)
		}
		return fmt.Errorf("git fetch failed: %s (%w)", outStr, err)
	}
	return nil
}

// Recovery may run after the PR ref advanced, so an already-present snapshot is
// never replaced with the moving FETCH_HEAD.
func EnsurePullRequestRevision(repoDir, remote string, number int, expectedSHA, authorization string) error {
	resolvedDir, err := ResolveRepoDir(repoDir)
	if err != nil {
		return err
	}
	ref := fmt.Sprintf("refs/pull/%d/head", number)
	if RefExists(resolvedDir, expectedSHA) {
		return nil
	}
	remoteURL, err := runGitOutput(OpMetadata, resolvedDir, "remote", "get-url", remote)
	if err != nil {
		return fmt.Errorf("read pull request remote: %w", err)
	}
	authorization, err = authorizationForGitURL(strings.TrimSpace(string(remoteURL)), authorization)
	if err != nil {
		return err
	}
	if out, err := runGitCombinedWithHTTPAuthorization(OpNetwork, resolvedDir, strings.TrimSpace(string(remoteURL)), authorization, "fetch", "--no-tags", remote, ref); err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("fetch pull request head: %s", message)
	}
	if !RefExists(resolvedDir, expectedSHA) {
		return fmt.Errorf("snapshotted pull request revision %s is unavailable after fetching %s", expectedSHA, ref)
	}
	return nil
}

func FetchRemotes(repoDir string) error {
	resolvedDir, err := ResolveRepoDir(repoDir)
	if err != nil {
		return err
	}
	if out, err := runGitCombined(OpNetwork, resolvedDir, "fetch", "--all", "--prune"); err != nil {
		outStr := strings.TrimSpace(string(out))
		if outStr == "" {
			return fmt.Errorf("git fetch failed: %w", err)
		}
		return fmt.Errorf("git fetch failed: %s (%w)", outStr, err)
	}
	return nil
}

func ListRemoteBranches(repoDir string) ([]string, error) {
	out, err := runGitOutput(OpMetadata, repoDir, "branch", "-r", "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("git branch -r failed: %w", err)
	}

	remoteBranches := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(remoteBranches) == 1 && remoteBranches[0] == "" {
		return nil, nil
	}

	localOut, err := runGitOutput(OpMetadata, repoDir, "branch", "--format=%(refname:short)")
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
	if _, err := runGitCombined(OpWorktree, repoDir, "checkout", branch); err == nil {
		return nil
	}

	if out, err := runGitCombined(OpWorktree, repoDir, "checkout", "-b", branch, "origin/"+branch); err != nil {
		return fmt.Errorf("git checkout failed: %s", out)
	}
	return nil
}

func GetHeadCommitInfo(repoDir string) (hash string, time string) {
	out, err := runGitOutput(OpMetadata, repoDir, "log", "-1", "--format=%h|%cI")
	if err != nil {
		return "", ""
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

// GetDefaultBranch is the branch new worktrees start from: `git config
// attn.baseBranch` when set, else origin/HEAD, else main or master.
func GetDefaultBranch(repoDir string) (string, error) {
	if out, err := runGitOutput(OpMetadata, repoDir, "config", "--get", "attn.baseBranch"); err == nil {
		if branch := strings.TrimSpace(string(out)); branch != "" {
			return branch, nil
		}
	}
	out, err := runGitOutput(OpMetadata, repoDir, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err == nil {
		ref := strings.TrimSpace(string(out))
		parts := strings.Split(ref, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1], nil
		}
	}

	for _, branch := range []string{"main", "master"} {
		if err := runGitNoOutput(OpMetadata, repoDir, "rev-parse", "--verify", branch); err == nil {
			return branch, nil
		}
	}

	return "main", nil
}
