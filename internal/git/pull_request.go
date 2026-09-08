package git

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

var remoteNamePart = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func RemoteForIdentity(repoDir, identity string) (string, bool) {
	identity = strings.ToLower(strings.TrimSpace(identity))
	out, err := runGitOutput(OpMetadata, repoDir, "remote")
	if err != nil {
		return "", false
	}
	for _, remote := range strings.Fields(string(out)) {
		raw, err := runGitOutput(OpMetadata, repoDir, "remote", "get-url", remote)
		if err != nil {
			continue
		}
		host, ownerRepo := hostOwnerRepoFromRemote(strings.TrimSpace(string(raw)))
		if strings.ToLower(host+"/"+ownerRepo) == identity {
			return remote, true
		}
	}
	return "", false
}

func EnsureRemoteForIdentity(repoDir, host, ownerRepo string) (string, string, bool, error) {
	identity := strings.ToLower(strings.TrimSpace(host + "/" + ownerRepo))
	if remote, ok := RemoteForIdentity(repoDir, identity); ok {
		raw, err := runGitOutput(OpMetadata, repoDir, "remote", "get-url", remote)
		return remote, strings.TrimSpace(string(raw)), false, err
	}
	base := "attn-" + remoteNamePart.ReplaceAllString(strings.ReplaceAll(strings.ToLower(ownerRepo), "/", "-"), "-")
	if base == "attn-" {
		base = "attn-pr-head"
	}
	remote := base
	for suffix := 2; ; suffix++ {
		if _, err := runGitOutput(OpMetadata, repoDir, "remote", "get-url", remote); err != nil {
			break
		}
		remote = fmt.Sprintf("%s-%d", base, suffix)
	}
	remoteURL := "https://" + host + "/" + ownerRepo + ".git"
	if out, err := runGitCombined(OpMetadata, repoDir, "remote", "add", remote, remoteURL); err != nil {
		return "", "", false, fmt.Errorf("add pull request head remote: %s", strings.TrimSpace(string(out)))
	}
	return remote, remoteURL, true, nil
}

func RemoveRemote(repoDir, remote string) {
	_, _ = runGitCombined(OpMetadata, repoDir, "remote", "remove", remote)
}

func FetchPullRequestHead(repoDir, remote, remoteURL, branch, expectedSHA, authorization string) error {
	if err := runGitNoOutput(OpMetadata, repoDir, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("invalid pull request head branch %q", branch)
	}
	if err := ensureRemoteTracksBranch(repoDir, remote, branch); err != nil {
		return err
	}
	target := "refs/remotes/" + remote + "/" + branch
	refspec := "+refs/heads/" + branch + ":" + target
	if out, err := runGitCombinedWithHTTPAuthorization(OpNetwork, repoDir, remoteURL, authorization, "fetch", "--no-tags", remote, refspec); err != nil && !RefExists(repoDir, expectedSHA) {
		return fmt.Errorf("fetch pull request head %s/%s: %s", remote, branch, strings.TrimSpace(string(out)))
	}
	if !RefExists(repoDir, expectedSHA) {
		return fmt.Errorf("resolved pull request head %s is unavailable after fetching %s/%s", expectedSHA, remote, branch)
	}
	if err := UpdateRemoteTrackingBranch(repoDir, remote, branch, expectedSHA); err != nil {
		return err
	}
	return nil
}

func ensureRemoteTracksBranch(repoDir, remote, branch string) error {
	target := "refs/remotes/" + remote + "/" + branch
	key := "remote." + remote + ".fetch"
	out, err := runGitOutput(OpMetadata, repoDir, "config", "--get-all", key)
	if err == nil {
		for _, refspec := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if fetchRefspecTracks(refspec, target) {
				return nil
			}
		}
	}
	refspec := "+refs/heads/" + branch + ":" + target
	if out, err := runGitCombined(OpMetadata, repoDir, "config", "--add", key, refspec); err != nil {
		return fmt.Errorf("track pull request head %s/%s: %s", remote, branch, strings.TrimSpace(string(out)))
	}
	return nil
}

func fetchRefspecTracks(refspec, target string) bool {
	refspec = strings.TrimPrefix(strings.TrimSpace(refspec), "+")
	if strings.HasPrefix(refspec, "^") {
		return false
	}
	_, destination, ok := strings.Cut(refspec, ":")
	if !ok {
		return false
	}
	if destination == target {
		return true
	}
	if strings.Count(destination, "*") != 1 {
		return false
	}
	prefix, suffix, _ := strings.Cut(destination, "*")
	return strings.HasPrefix(target, prefix) && strings.HasSuffix(target, suffix)
}

func FetchPullRequestCommitFromBase(repoDir, remote, remoteURL string, number int, expectedSHA, authorization string) error {
	target := fmt.Sprintf("refs/attn/pull-requests/%d/%s", number, expectedSHA)
	refspec := fmt.Sprintf("+refs/pull/%d/head:%s", number, target)
	firstOutput, firstErr := runGitCombinedWithHTTPAuthorization(OpNetwork, repoDir, remoteURL, authorization, "fetch", "--no-tags", remote, refspec)
	if firstErr == nil && RefExists(repoDir, expectedSHA) {
		return nil
	}
	shaRefspec := "+" + expectedSHA + ":" + target
	secondOutput, secondErr := runGitCombinedWithHTTPAuthorization(OpNetwork, repoDir, remoteURL, authorization, "fetch", "--no-tags", remote, shaRefspec)
	if secondErr == nil && RefExists(repoDir, expectedSHA) {
		return nil
	}
	return fmt.Errorf("fetch pull request commit %s from %s: %s; %s", expectedSHA, remote,
		strings.TrimSpace(string(firstOutput)), strings.TrimSpace(string(secondOutput)))
}

func UpdateRemoteTrackingBranch(repoDir, remote, branch, sha string) error {
	target := "refs/remotes/" + remote + "/" + branch
	if out, err := runGitCombined(OpMetadata, repoDir, "update-ref", target, sha); err != nil {
		return fmt.Errorf("record pull request head %s/%s at %s: %s", remote, branch, sha, strings.TrimSpace(string(out)))
	}
	return nil
}

func BranchHead(repoDir, branch string) (string, bool, error) {
	out, err := runGitOutput(OpMetadata, repoDir, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return "", false, nil
	}
	return strings.TrimSpace(string(out)), true, nil
}

func UpdateBranch(repoDir, branch, sha, expectedOld string) error {
	args := []string{"update-ref", "refs/heads/" + branch, sha}
	if expectedOld != "" {
		args = append(args, expectedOld)
	}
	if out, err := runGitCombined(OpMetadata, repoDir, args...); err != nil {
		return fmt.Errorf("move branch %s: %s", branch, strings.TrimSpace(string(out)))
	}
	return nil
}

func CreateBranchAt(repoDir, branch, sha string) error {
	return UpdateBranch(repoDir, branch, sha, strings.Repeat("0", 40))
}

func DeleteBranchAt(repoDir, branch, expectedHead string) error {
	if out, err := runGitCombined(OpMetadata, repoDir, "update-ref", "-d", "refs/heads/"+branch, expectedHead); err != nil {
		return fmt.Errorf("delete branch %s at %s: %s", branch, expectedHead, strings.TrimSpace(string(out)))
	}
	return nil
}

func SetBranchUpstream(repoDir, branch, remote, remoteBranch string) error {
	if out, err := runGitCombined(OpMetadata, repoDir, "branch", "--set-upstream-to", remote+"/"+remoteBranch, branch); err != nil {
		return fmt.Errorf("set %s upstream to %s/%s: %s", branch, remote, remoteBranch, strings.TrimSpace(string(out)))
	}
	return nil
}

func BranchUpstream(repoDir, branch string) (string, error) {
	out, err := runGitOutput(OpMetadata, repoDir, "rev-parse", "--abbrev-ref", branch+"@{upstream}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func UnsetBranchUpstream(repoDir, branch string) {
	_, _ = runGitCombined(OpMetadata, repoDir, "branch", "--unset-upstream", branch)
}

func RepositoryOperationInProgress(worktree string) string {
	checks := []struct{ path, name string }{
		{"MERGE_HEAD", "merge"}, {"CHERRY_PICK_HEAD", "cherry-pick"},
		{"rebase-merge", "rebase"}, {"rebase-apply", "rebase"},
	}
	for _, check := range checks {
		pathOut, err := runGitOutput(OpMetadata, worktree, "rev-parse", "--git-path", check.path)
		if err != nil {
			continue
		}
		path := strings.TrimSpace(string(pathOut))
		if !filepath.IsAbs(path) {
			path = filepath.Join(worktree, path)
		}
		if _, err := os.Stat(path); err == nil {
			return check.name
		}
	}
	return ""
}

func AcquirePullRequestCheckoutLock(repoDir string) (func(), error) {
	commonDir, err := runGitOutput(OpMetadata, repoDir, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("locate Git common directory: %w", err)
	}
	path := strings.TrimSpace(string(commonDir))
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoDir, path)
	}
	path = filepath.Clean(path)
	lockPath := filepath.Join(path, "attn-pr-checkout.lock")
	file, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock pull request checkout: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("another pull request checkout is already changing this repository")
	}
	if err := file.Truncate(0); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("record pull request checkout lock: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("record pull request checkout lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func StatusFingerprint(worktree string) (string, error) {
	head, err := GetHeadCommit(worktree)
	if err != nil {
		return "", err
	}
	return statusFingerprintAtHead(worktree, head)
}

func statusFingerprintAtHead(worktree, head string) (string, error) {
	index, err := runGitOutput(OpStatus, worktree, "diff", "--cached", head, "--raw", "--full-index", "--no-renames", "-z")
	if err != nil {
		return "", err
	}
	tracked, err := runGitOutput(OpStatus, worktree, "diff", "--name-only", "--no-renames", "-z")
	if err != nil {
		return "", err
	}
	untracked, err := runGitOutput(OpStatus, worktree, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	paths := map[string]bool{}
	for _, output := range [][]byte{tracked, untracked} {
		for _, path := range strings.Split(string(output), "\x00") {
			if path != "" {
				paths[path] = true
			}
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "head\x00%s\x00operation\x00%s\x00index\x00", head, RepositoryOperationInProgress(worktree))
	_, _ = hash.Write(index)
	_, _ = hash.Write([]byte("\x00"))
	for _, path := range ordered {
		_, _ = fmt.Fprintf(hash, "path\x00%s\x00", path)
		fullPath := filepath.Join(worktree, filepath.FromSlash(path))
		info, statErr := os.Lstat(fullPath)
		if os.IsNotExist(statErr) {
			_, _ = hash.Write([]byte("missing\x00"))
			continue
		}
		if statErr != nil {
			return "", statErr
		}
		_, _ = fmt.Fprintf(hash, "mode\x00%o\x00", info.Mode())
		if info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(fullPath)
			if readErr != nil {
				return "", readErr
			}
			_, _ = fmt.Fprintf(hash, "symlink\x00%s\x00", target)
			continue
		}
		if info.IsDir() {
			submoduleHead, _ := runGitOutput(OpMetadata, fullPath, "rev-parse", "HEAD")
			_, _ = fmt.Fprintf(hash, "directory\x00%s\x00", strings.TrimSpace(string(submoduleHead)))
			continue
		}
		content, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			return "", readErr
		}
		_, _ = hash.Write(content)
		_, _ = hash.Write([]byte("\x00"))
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func IgnoredCheckoutCollisions(worktree, targetSHA string) ([]string, error) {
	ignored, err := runGitOutput(OpStatus, worktree, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	tree, err := runGitOutput(OpMetadata, worktree, "ls-tree", "-r", "--name-only", "-z", targetSHA)
	if err != nil {
		return nil, err
	}
	var targets []string
	for _, path := range strings.Split(string(tree), "\x00") {
		if path != "" {
			targets = append(targets, path)
		}
	}
	var collisions []string
	for _, ignoredPath := range strings.Split(string(ignored), "\x00") {
		if ignoredPath == "" {
			continue
		}
		for _, targetPath := range targets {
			if ignoredPath == targetPath || strings.HasPrefix(ignoredPath, targetPath+"/") || strings.HasPrefix(targetPath, ignoredPath+"/") {
				collisions = append(collisions, ignoredPath)
				break
			}
		}
	}
	sort.Strings(collisions)
	return collisions, nil
}

func PreserveWorktreeOnBackup(worktree, branch, backup, sourceHead, expectedFingerprint string) (string, error) {
	currentBranch, err := GetCurrentBranch(worktree)
	if err != nil {
		return "", err
	}
	currentHead, err := GetHeadCommit(worktree)
	if err != nil {
		return "", err
	}
	if currentBranch == backup && currentHead != sourceHead {
		if !isRecoveryCommitChain(worktree, currentHead, sourceHead, branch) {
			return "", fmt.Errorf("backup branch %s exists but its recovery commit could not be verified; preserved state remains in %s", backup, worktree)
		}
		if clean, cleanErr := IsWorktreeClean(worktree); cleanErr == nil && clean {
			return currentHead, nil
		}
		if current, fingerprintErr := statusFingerprintAtHead(worktree, sourceHead); fingerprintErr != nil || current != expectedFingerprint {
			return "", fmt.Errorf("worktree state changed after recording backup %s; preserved state remains in %s", backup, worktree)
		}
		if out, resetErr := runGitCombined(OpWorktree, worktree, "reset", "--hard", currentHead); resetErr != nil {
			return "", fmt.Errorf("finish backup branch %s: %s; preserved state remains in %s", backup, strings.TrimSpace(string(out)), worktree)
		}
		return currentHead, nil
	}
	if currentBranch != branch && currentBranch != backup {
		return "", fmt.Errorf("backup branch %s was planned, but %s is on branch %s; state was left untouched", backup, worktree, currentBranch)
	}
	if currentHead != sourceHead {
		return "", fmt.Errorf("backup branch %s expected source HEAD %s, got %s; state was left untouched", backup, sourceHead, currentHead)
	}
	if current, err := StatusFingerprint(worktree); err != nil || current != expectedFingerprint {
		return "", fmt.Errorf("worktree state changed while preparing backup %s; preserved state remains in %s", backup, worktree)
	}
	if currentBranch == branch {
		if backupHead, exists, branchErr := BranchHead(worktree, backup); branchErr != nil {
			return "", branchErr
		} else if exists && backupHead != sourceHead {
			return "", fmt.Errorf("backup branch %s already exists at %s, want %s; state was left untouched", backup, backupHead, sourceHead)
		} else if !exists {
			if err := CreateBranchAt(worktree, backup, sourceHead); err != nil {
				return "", err
			}
		}
		if out, err := runGitCombined(OpWorktree, worktree, "switch", backup); err != nil {
			return "", fmt.Errorf("switch to backup branch %s: %s", backup, strings.TrimSpace(string(out)))
		}
	}
	UnsetBranchUpstream(worktree, backup)
	if current, err := StatusFingerprint(worktree); err != nil || current != expectedFingerprint {
		return "", fmt.Errorf("worktree state changed after creating backup %s; preserved state remains in %s", backup, worktree)
	}
	clean, err := IsWorktreeClean(worktree)
	if err != nil {
		return "", err
	}
	if !clean {
		parent := sourceHead
		headTree, treeErr := runGitOutput(OpMetadata, worktree, "rev-parse", sourceHead+"^{tree}")
		if treeErr != nil {
			return "", fmt.Errorf("read source tree for backup %s: %w", backup, treeErr)
		}
		indexTree, treeErr := runGitOutput(OpMetadata, worktree, "write-tree")
		if treeErr != nil {
			return "", fmt.Errorf("write staged recovery tree on backup %s: %w", backup, treeErr)
		}
		if strings.TrimSpace(string(indexTree)) != strings.TrimSpace(string(headTree)) {
			commit, commitErr := createRecoveryCommit(worktree, strings.TrimSpace(string(indexTree)), parent, "attn recovery: preserve staged "+branch)
			if commitErr != nil {
				return "", commitErr
			}
			parent = commit
		}
		workingTree, err := writeWorktreeTree(worktree, strings.TrimSpace(string(indexTree)))
		if err != nil {
			return "", fmt.Errorf("write working recovery tree on backup %s: %w; preserved state remains in %s", backup, err, worktree)
		}
		parentTree, parentTreeErr := runGitOutput(OpMetadata, worktree, "rev-parse", parent+"^{tree}")
		if parentTreeErr != nil {
			return "", fmt.Errorf("read recovery parent tree on backup %s: %w", backup, parentTreeErr)
		}
		if workingTree != strings.TrimSpace(string(parentTree)) {
			commit, commitErr := createRecoveryCommit(worktree, workingTree, parent, "attn recovery: preserve "+branch)
			if commitErr != nil {
				return "", commitErr
			}
			parent = commit
		}
		if err := UpdateBranch(worktree, backup, parent, sourceHead); err != nil {
			return "", fmt.Errorf("record recovery commits on backup %s: %w", backup, err)
		}
		if current, fingerprintErr := statusFingerprintAtHead(worktree, sourceHead); fingerprintErr != nil || current != expectedFingerprint {
			return "", fmt.Errorf("worktree state changed while recording backup %s; preserved state remains in %s", backup, worktree)
		}
		if out, resetErr := runGitCombined(OpWorktree, worktree, "reset", "--hard", parent); resetErr != nil {
			return "", fmt.Errorf("finish backup branch %s: %s; preserved state remains in %s", backup, strings.TrimSpace(string(out)), worktree)
		}
		committedTree, treeErr := runGitOutput(OpMetadata, worktree, "rev-parse", parent+"^{tree}")
		if treeErr != nil || strings.TrimSpace(string(committedTree)) != workingTree {
			return "", fmt.Errorf("verify recovery tree on backup %s; preserved state remains in %s", backup, worktree)
		}
	}
	backupHead, ok, err := BranchHead(worktree, backup)
	if err != nil || !ok {
		return "", fmt.Errorf("verify backup branch %s: %v; preserved state remains in %s", backup, err, worktree)
	}
	if currentBranch, branchErr := GetCurrentBranch(worktree); branchErr != nil || currentBranch != backup {
		return "", fmt.Errorf("verify checked-out backup branch %s; preserved state remains in %s", backup, worktree)
	}
	if head, headErr := GetHeadCommit(worktree); headErr != nil || head != backupHead {
		return "", fmt.Errorf("verify backup HEAD %s; preserved state remains in %s", backup, worktree)
	}
	if clean, cleanErr := IsWorktreeClean(worktree); cleanErr != nil || !clean {
		return "", fmt.Errorf("backup branch %s did not capture every non-ignored change; preserved state remains in %s", backup, worktree)
	}
	if _, upstreamErr := runGitOutput(OpMetadata, worktree, "rev-parse", "--abbrev-ref", "@{upstream}"); upstreamErr == nil {
		return "", fmt.Errorf("backup branch %s still has an upstream; preserved state remains in %s", backup, worktree)
	}
	return backupHead, nil
}

func SwitchWorktreeToBranch(worktree, branch, sha string) error {
	branchHead, exists, err := BranchHead(worktree, branch)
	if err != nil || !exists || branchHead != sha {
		return fmt.Errorf("restore pull request branch %s: branch is at %s, want %s", branch, branchHead, sha)
	}
	if out, err := runGitCombined(OpWorktree, worktree, "switch", branch); err != nil {
		return fmt.Errorf("restore pull request branch %s: %s", branch, strings.TrimSpace(string(out)))
	}
	return nil
}

func FastForwardWorktree(worktree, branch, expectedHead, target string) error {
	currentBranch, err := GetCurrentBranch(worktree)
	if err != nil || currentBranch != branch {
		return fmt.Errorf("fast-forward pull request branch %s: current branch is %s", branch, currentBranch)
	}
	currentHead, err := GetHeadCommit(worktree)
	if err != nil || currentHead != expectedHead {
		return fmt.Errorf("fast-forward pull request branch %s: HEAD moved from %s to %s", branch, expectedHead, currentHead)
	}
	if out, err := runGitCombined(OpWorktree, worktree, "merge", "--ff-only", target); err != nil {
		return fmt.Errorf("fast-forward pull request branch %s: %s", branch, strings.TrimSpace(string(out)))
	}
	if head, err := GetHeadCommit(worktree); err != nil || head != target {
		return fmt.Errorf("verify fast-forwarded pull request branch %s at %s", branch, target)
	}
	return nil
}

func createRecoveryCommit(worktree, tree, parent, subject string) (string, error) {
	out, err := runGitOutput(OpWorktree, worktree,
		"-c", "commit.gpgSign=false", "-c", "user.name=attn recovery", "-c", "user.email=attn@localhost",
		"commit-tree", tree, "-p", parent, "-m", subject)
	if err != nil {
		return "", fmt.Errorf("create recovery commit: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func writeWorktreeTree(worktree, indexTree string) (string, error) {
	file, err := os.CreateTemp("", "attn-pr-index-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	defer os.Remove(path)
	env := map[string]string{"GIT_INDEX_FILE": path}
	if _, err := runGitCommandWithTimeoutAndEnv(OpWorktree, defaultTimeout(OpWorktree), worktree, nil, true, env, "read-tree", indexTree); err != nil {
		return "", err
	}
	if out, err := runGitCommandWithTimeoutAndEnv(OpWorktree, defaultTimeout(OpWorktree), worktree, nil, true, env, "add", "-A"); err != nil {
		return "", fmt.Errorf("stage working files: %s", strings.TrimSpace(string(out)))
	}
	out, err := runGitCommandWithTimeoutAndEnv(OpMetadata, defaultTimeout(OpMetadata), worktree, nil, false, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func isRecoveryCommitChain(worktree, head, source, branch string) bool {
	parents, subject, ok := commitMetadata(worktree, head)
	if !ok || len(parents) != 1 {
		return false
	}
	if parents[0] == source && (subject == "attn recovery: preserve "+branch || subject == "attn recovery: preserve staged "+branch) {
		return true
	}
	if subject != "attn recovery: preserve "+branch {
		return false
	}
	stagedParents, stagedSubject, stagedOK := commitMetadata(worktree, parents[0])
	return stagedOK && len(stagedParents) == 1 && stagedParents[0] == source && stagedSubject == "attn recovery: preserve staged "+branch
}

func commitMetadata(worktree, commit string) ([]string, string, bool) {
	out, err := runGitOutput(OpMetadata, worktree, "show", "-s", "--format=%P%x00%s", commit)
	if err != nil {
		return nil, "", false
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "\x00", 2)
	if len(parts) != 2 {
		return nil, "", false
	}
	return strings.Fields(parts[0]), parts[1], true
}
