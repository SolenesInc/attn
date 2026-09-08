package daemon

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automation"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

type delegationPRTarget struct {
	host       string
	ownerRepo  string
	number     int
	source     string
	mainRepo   string
	receipt    *protocol.DelegatePullRequestReceipt
	authorizer string
}

var fullCommitSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func validateDelegationPullRequestRequest(msg *protocol.DelegateMessage, placement string) error {
	if strings.TrimSpace(protocol.Deref(msg.PullRequest)) == "" {
		return nil
	}
	if placement != delegationPlacementNew || msg.Handover != nil || msg.Worktree == nil {
		return fmt.Errorf("pull request checkout applies only to a new worktree launch")
	}
	if strings.TrimSpace(protocol.Deref(msg.Worktree.Repo)) == "" {
		return fmt.Errorf("pull request checkout requires --repo")
	}
	if strings.TrimSpace(msg.Worktree.Branch) != "" || strings.TrimSpace(protocol.Deref(msg.Worktree.StartingFrom)) != "" || protocol.Deref(msg.Worktree.ExistingBranch) {
		return fmt.Errorf("pull request checkout cannot be combined with a branch or starting ref")
	}
	return nil
}

func parseDelegationPRSource(source, repo string) (host, ownerRepo string, number int, err error) {
	source = strings.TrimSpace(source)
	if n, parseErr := strconv.Atoi(source); parseErr == nil && n > 0 {
		host, ownerRepo = attngit.OriginHostOwnerRepo(repo)
		if host == "" || ownerRepo == "" {
			return "", "", 0, fmt.Errorf("--repo has no parseable origin for numeric pull request %d", n)
		}
		return host, ownerRepo, n, nil
	}
	host, owner, repository, number, parseErr := automation.ParsePullRequestURL(source)
	if parseErr != nil {
		return "", "", 0, fmt.Errorf("invalid pull request %q: %w", source, parseErr)
	}
	ownerRepo = owner + "/" + repository
	want := strings.ToLower(host + "/" + ownerRepo)
	found := false
	for _, identity := range attngit.RemoteHostOwnerRepos(repo) {
		if strings.ToLower(identity) == want {
			found = true
			break
		}
	}
	if !found {
		return "", "", 0, fmt.Errorf("pull request URL repository %s does not match a remote of --repo %s", want, repo)
	}
	return host, ownerRepo, number, nil
}

func (d *Daemon) resolveDelegationPullRequest(source, repo, operationID string, saved *protocol.DelegatePullRequestReceipt) (*delegationPRTarget, error) {
	repo, err := resolveDelegationRepository(repo, "--repo")
	if err != nil {
		return nil, err
	}
	host, ownerRepo, number, err := parseDelegationPRSource(source, repo)
	if err != nil {
		return nil, err
	}
	if saved != nil {
		if saved.Source != source || saved.Number != number || !strings.EqualFold(saved.BaseRepository, host+"/"+ownerRepo) {
			return nil, fmt.Errorf("persisted pull request receipt does not match this delegation request")
		}
		authorization := ""
		if client, ok := d.ghRegistry.Get(host); ok && client != nil {
			authorization = client.GitHTTPSAuthorizationHeader()
		}
		return &delegationPRTarget{host: host, ownerRepo: ownerRepo, number: number, source: source, mainRepo: repo, receipt: saved, authorizer: authorization}, nil
	}
	if d.ghRegistry == nil {
		return nil, fmt.Errorf("GitHub host %s is not configured", host)
	}
	client, ok := d.ghRegistry.Get(host)
	if !ok || client == nil {
		return nil, fmt.Errorf("GitHub host %s is not configured", host)
	}
	snapshot, err := client.FetchPullRequestSnapshot(ownerRepo, number)
	if err != nil {
		return nil, fmt.Errorf("resolve pull request %s/%s#%d: %w", host, ownerRepo, number, err)
	}
	baseIdentity, baseErr := automation.CanonicalRepositoryIdentity(host + "/" + snapshot.BaseRepository)
	headIdentity, headErr := automation.CanonicalRepositoryIdentity(host + "/" + snapshot.HeadRepository)
	urlHost, urlOwner, urlRepo, urlNumber, urlErr := automation.ParsePullRequestURL(snapshot.URL)
	if snapshot.Number != number || baseErr != nil || !strings.EqualFold(baseIdentity, host+"/"+ownerRepo) ||
		headErr != nil || urlErr != nil || urlHost != host || urlOwner+"/"+urlRepo != ownerRepo || urlNumber != number {
		return nil, fmt.Errorf("GitHub response does not match pull request %s/%s#%d", host, ownerRepo, number)
	}
	if !fullCommitSHA.MatchString(snapshot.HeadSHA) || snapshot.HeadRef == "" {
		return nil, fmt.Errorf("pull request %s/%s#%d has no resolvable head", host, ownerRepo, number)
	}
	state := snapshot.State
	if snapshot.Merged {
		state = "merged"
	} else if snapshot.Draft {
		state = "draft"
	}
	receipt := protocol.DelegatePullRequestReceipt{
		Source: source, URL: strings.TrimSuffix(snapshot.URL, "/"), Number: number, State: strings.ToLower(state),
		BaseRepository: baseIdentity, HeadRepository: headIdentity,
		HeadBranch: snapshot.HeadRef, HeadSHA: strings.ToLower(snapshot.HeadSHA), LocalBranch: snapshot.HeadRef,
	}
	if operationID != "" {
		receiptPtr, err := d.store.SaveDelegationPullRequestReceipt(operationID, receipt, time.Now())
		if err != nil {
			return nil, err
		}
		receipt = *receiptPtr
	}
	return &delegationPRTarget{host: host, ownerRepo: ownerRepo, number: number, source: source, mainRepo: repo, receipt: &receipt, authorizer: client.GitHTTPSAuthorizationHeader()}, nil
}

func (d *Daemon) liveDelegationPRBranchOwner(mainRepo, branch string) (path, session string, found bool, err error) {
	worktrees, err := attngit.ObserveWorktrees(mainRepo)
	if err != nil {
		return "", "", false, err
	}
	for _, worktree := range worktrees {
		if worktree.Branch != branch {
			continue
		}
		for _, candidate := range d.store.List("") {
			root, rootErr := attngit.GetRepoRoot(candidate.Directory)
			if rootErr != nil || attngit.CanonicalizePath(root) != attngit.CanonicalizePath(worktree.Path) {
				continue
			}
			if d.sessionHasLiveWorker(candidate.ID) {
				name := strings.TrimSpace(candidate.Label)
				if name == "" {
					name = candidate.ID
				}
				return worktree.Path, name, true, nil
			}
		}
	}
	return "", "", false, nil
}

func delegationPRBackupName(repo, branch string) string {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	base := branch + "--attn-backup-" + stamp
	name := base
	for suffix := 2; ; suffix++ {
		if _, exists, _ := attngit.BranchHead(repo, name); !exists {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, suffix)
	}
}

func (d *Daemon) saveDelegationPRProgress(operationID string, receipt *protocol.DelegatePullRequestReceipt) error {
	if operationID == "" {
		return nil
	}
	return d.store.UpdateDelegationPullRequestReceipt(operationID, *receipt, time.Now())
}

func advanceDelegationPRBranch(repo, branch, target, expectedOld string) error {
	head, exists, err := attngit.BranchHead(repo, branch)
	if err != nil || !exists {
		return fmt.Errorf("pull request branch %s disappeared while preserving its previous state", branch)
	}
	if head == target {
		return nil
	}
	if head != expectedOld {
		return fmt.Errorf("pull request branch %s moved from %s to %s; preserved state was left untouched", branch, expectedOld, head)
	}
	return attngit.UpdateBranch(repo, branch, target, expectedOld)
}

func verifyDelegationPROwnedWorktree(path, ownedPath string, worktreeOwned bool, worktreeToken string) (bool, error) {
	path = attngit.CanonicalizePath(path)
	if strings.TrimSpace(ownedPath) != "" {
		ownedPath = attngit.CanonicalizePath(ownedPath)
	}
	if !worktreeOwned {
		if ownedPath != "" && ownedPath == path {
			return false, fmt.Errorf("worktree %s appeared while delegation preparation was interrupted; ownership cannot be proven, so it was left untouched", path)
		}
		return false, nil
	}
	if ownedPath == "" || ownedPath != path {
		return false, fmt.Errorf("delegation-owned worktree moved from %s to %s; ownership cannot be proven", ownedPath, path)
	}
	if err := verifyDelegationWorktreeOwner(path, worktreeToken); err != nil {
		return false, fmt.Errorf("worktree %s was created before delegation preparation was interrupted, but its current ownership cannot be proven (%v), so it was left untouched", path, err)
	}
	return true, nil
}

func (d *Daemon) verifyDelegationPRLaunchReceipt(target *delegationPRTarget, receipt *protocol.DelegatePullRequestReceipt) error {
	if receipt == nil || receipt.WorktreePath == "" || receipt.VerifiedHead == "" || receipt.CheckoutFingerprint == nil {
		return fmt.Errorf("pull request launch receipt is incomplete")
	}
	if ownerPath, owner, found, err := d.liveDelegationPRBranchOwner(target.mainRepo, receipt.LocalBranch); err != nil {
		return err
	} else if found {
		return fmt.Errorf("pull request %s branch %s is in %s and owned by live attn session %s; steer or end that session, then retry", receipt.URL, receipt.LocalBranch, ownerPath, owner)
	}
	head, err := attngit.GetHeadCommit(receipt.WorktreePath)
	if err != nil || !strings.EqualFold(head, receipt.HeadSHA) {
		return fmt.Errorf("pull request %s checkout %s is at %s, want persisted head %s", receipt.URL, receipt.WorktreePath, head, receipt.HeadSHA)
	}
	branch, err := attngit.GetCurrentBranch(receipt.WorktreePath)
	if err != nil || branch != receipt.LocalBranch {
		return fmt.Errorf("pull request %s checkout %s is on branch %s, want %s", receipt.URL, receipt.WorktreePath, branch, receipt.LocalBranch)
	}
	clean, err := attngit.IsWorktreeClean(receipt.WorktreePath)
	if err != nil || !clean {
		return fmt.Errorf("pull request %s checkout %s changed after verification; refusing to launch", receipt.URL, receipt.WorktreePath)
	}
	fingerprint, err := attngit.StatusFingerprint(receipt.WorktreePath)
	if err != nil || fingerprint != protocol.Deref(receipt.CheckoutFingerprint) {
		return fmt.Errorf("pull request %s checkout %s changed after verification; refusing to launch", receipt.URL, receipt.WorktreePath)
	}
	headRepo := strings.TrimPrefix(receipt.HeadRepository, target.host+"/")
	remote, ok := attngit.RemoteForIdentity(target.mainRepo, target.host+"/"+headRepo)
	if !ok {
		return fmt.Errorf("pull request head remote %s is missing before launch", receipt.HeadRepository)
	}
	upstream, err := attngit.BranchUpstream(receipt.WorktreePath, receipt.LocalBranch)
	if err != nil || upstream != remote+"/"+receipt.HeadBranch {
		return fmt.Errorf("pull request branch %s upstream is %s, want %s/%s", receipt.LocalBranch, upstream, remote, receipt.HeadBranch)
	}
	return nil
}

func (d *Daemon) materializeDelegationPullRequest(target *delegationPRTarget, request *protocol.DelegateWorktreeRequest, operationID, ownedPath string, worktreeOwned bool, worktreeToken string) (string, bool, error) {
	receipt := target.receipt
	branch := receipt.HeadBranch
	if ownerPath, owner, found, err := d.liveDelegationPRBranchOwner(target.mainRepo, branch); err != nil {
		return "", false, fmt.Errorf("inspect pull request branch ownership: %w", err)
	} else if found {
		return "", false, fmt.Errorf("pull request %s branch %s is in %s and owned by live attn session %s; steer or end that session, then retry", receipt.URL, branch, ownerPath, owner)
	}
	releaseLock, err := attngit.AcquirePullRequestCheckoutLock(target.mainRepo)
	if err != nil {
		return "", false, err
	}
	defer releaseLock()
	if ownerPath, owner, found, err := d.liveDelegationPRBranchOwner(target.mainRepo, branch); err != nil {
		return "", false, fmt.Errorf("inspect pull request branch ownership under checkout lock: %w", err)
	} else if found {
		return "", false, fmt.Errorf("pull request %s branch %s is in %s and owned by live attn session %s; steer or end that session, then retry", receipt.URL, branch, ownerPath, owner)
	}
	if receipt.VerifiedHead != "" {
		created := false
		if receipt.Disposition == "created" {
			created, err = verifyDelegationPROwnedWorktree(receipt.WorktreePath, ownedPath, worktreeOwned, worktreeToken)
			if err != nil {
				return "", false, err
			}
		}
		if err := d.verifyDelegationPRLaunchReceipt(target, receipt); err != nil {
			return "", false, err
		}
		return receipt.WorktreePath, created, nil
	}
	if protocol.Deref(receipt.BackupBranch) != "" && receipt.WorktreePath != "" {
		backup := protocol.Deref(receipt.BackupBranch)
		currentBranch, _ := attngit.GetCurrentBranch(receipt.WorktreePath)
		currentHead, _ := attngit.GetHeadCommit(receipt.WorktreePath)
		if currentBranch != branch || currentHead != receipt.HeadSHA {
			backupHead, err := attngit.PreserveWorktreeOnBackup(receipt.WorktreePath, branch, backup,
				protocol.Deref(receipt.BackupSourceHead), protocol.Deref(receipt.BackupFingerprint))
			if err != nil {
				return "", false, err
			}
			if savedHead := protocol.Deref(receipt.BackupHead); savedHead != "" && savedHead != backupHead {
				return "", false, fmt.Errorf("backup branch %s moved from verified head %s to %s; state was left untouched", backup, savedHead, backupHead)
			}
			receipt.BackupHead = protocol.Ptr(backupHead)
			if err := d.saveDelegationPRProgress(operationID, receipt); err != nil {
				return "", false, err
			}
			if err := advanceDelegationPRBranch(target.mainRepo, branch, receipt.HeadSHA, protocol.Deref(receipt.BackupSourceHead)); err != nil {
				return "", false, err
			}
			if err := attngit.SwitchWorktreeToBranch(receipt.WorktreePath, branch, receipt.HeadSHA); err != nil {
				return "", false, err
			}
		} else if backupHead, ok, backupErr := attngit.BranchHead(target.mainRepo, backup); backupErr != nil || !ok || backupHead != protocol.Deref(receipt.BackupHead) {
			return "", false, fmt.Errorf("backup branch %s is not at its verified head %s; state was left untouched", backup, protocol.Deref(receipt.BackupHead))
		}
	}

	worktrees, err := attngit.ObserveWorktrees(target.mainRepo)
	if err != nil {
		return "", false, err
	}
	branchWorktree := ""
	branchWorktreeOwned := false
	for _, worktree := range worktrees {
		if worktree.Branch == branch {
			branchWorktree = worktree.Path
			break
		}
	}
	if branchWorktree != "" {
		if requested := strings.TrimSpace(protocol.Deref(request.Path)); requested != "" && attngit.CanonicalizePath(requested) != attngit.CanonicalizePath(branchWorktree) {
			return "", false, fmt.Errorf("pull request branch %s already uses %s, not requested worktree path %s", branch, branchWorktree, requested)
		}
		if operation := attngit.RepositoryOperationInProgress(branchWorktree); operation != "" {
			return "", false, fmt.Errorf("pull request branch %s has an in-progress %s in %s; state was left untouched", branch, operation, branchWorktree)
		}
		branchWorktreeOwned, err = verifyDelegationPROwnedWorktree(branchWorktree, ownedPath, worktreeOwned, worktreeToken)
		if err != nil {
			return "", false, err
		}
	}
	worktreeFingerprint := ""
	if branchWorktree != "" {
		worktreeFingerprint, err = attngit.StatusFingerprint(branchWorktree)
		if err != nil {
			return "", false, err
		}
	}

	headRepo := strings.TrimPrefix(receipt.HeadRepository, target.host+"/")
	remote, remoteURL, added, err := attngit.EnsureRemoteForIdentity(target.mainRepo, target.host, headRepo)
	if err != nil {
		return "", false, err
	}
	keepRemote := false
	defer func() {
		if added && !keepRemote {
			attngit.RemoveRemote(target.mainRepo, remote)
		}
	}()
	fetch := attngit.FetchPullRequestHead
	if d.delegationPRFetch != nil {
		fetch = d.delegationPRFetch
	}
	if fetchErr := fetch(target.mainRepo, remote, remoteURL, branch, receipt.HeadSHA, target.authorizer); fetchErr != nil {
		if !attngit.RefExists(target.mainRepo, receipt.HeadSHA) {
			baseRemote, baseURL, _, baseErr := attngit.EnsureRemoteForIdentity(target.mainRepo, target.host, target.ownerRepo)
			if baseErr != nil {
				return "", false, fmt.Errorf("pull request %s expected head %s is unavailable: %v; %w", receipt.URL, receipt.HeadSHA, fetchErr, baseErr)
			}
			if fallbackErr := attngit.FetchPullRequestCommitFromBase(target.mainRepo, baseRemote, baseURL, receipt.Number, receipt.HeadSHA, target.authorizer); fallbackErr != nil {
				return "", false, fmt.Errorf("pull request %s expected head %s is unavailable: %v; %w", receipt.URL, receipt.HeadSHA, fetchErr, fallbackErr)
			}
		}
		if err := attngit.UpdateRemoteTrackingBranch(target.mainRepo, remote, branch, receipt.HeadSHA); err != nil {
			return "", false, err
		}
	}

	if branchWorktree != "" {
		if ownerPath, owner, found, err := d.liveDelegationPRBranchOwner(target.mainRepo, branch); err != nil {
			return "", false, err
		} else if found {
			return "", false, fmt.Errorf("pull request %s branch %s became owned by live attn session %s in %s; state was left untouched", receipt.URL, branch, owner, ownerPath)
		}
		if current, fingerprintErr := attngit.StatusFingerprint(branchWorktree); fingerprintErr != nil || current != worktreeFingerprint {
			return "", false, fmt.Errorf("worktree state changed while resolving pull request %s; state was left untouched", receipt.URL)
		}
		collisions, err := attngit.IgnoredCheckoutCollisions(branchWorktree, receipt.HeadSHA)
		if err != nil {
			return "", false, err
		}
		if len(collisions) > 0 {
			return "", false, fmt.Errorf("ignored files in %s collide with pull request %s: %s; state was left untouched", branchWorktree, receipt.URL, strings.Join(collisions, ", "))
		}
		head, err := attngit.GetHeadCommit(branchWorktree)
		if err != nil {
			return "", false, err
		}
		clean, err := attngit.IsWorktreeClean(branchWorktree)
		if err != nil {
			return "", false, err
		}
		if clean && strings.EqualFold(head, receipt.HeadSHA) {
			if protocol.Deref(receipt.BackupBranch) == "" {
				receipt.Disposition = "reused"
			} else {
				receipt.Disposition = "preserved"
			}
		} else if clean && attngit.IsAncestor(target.mainRepo, head, receipt.HeadSHA) {
			if err := attngit.FastForwardWorktree(branchWorktree, branch, head, receipt.HeadSHA); err != nil {
				return "", false, err
			}
			receipt.Disposition = "fast-forwarded"
		} else {
			backup := protocol.Deref(receipt.BackupBranch)
			if backup == "" {
				backup = delegationPRBackupName(target.mainRepo, branch)
				receipt.BackupBranch = protocol.Ptr(backup)
				receipt.WorktreePath = branchWorktree
				receipt.BackupSourceHead = protocol.Ptr(head)
				receipt.BackupFingerprint = protocol.Ptr(worktreeFingerprint)
				if err := d.saveDelegationPRProgress(operationID, receipt); err != nil {
					return "", false, fmt.Errorf("record planned pull request backup %s: %w", backup, err)
				}
			}
			backupHead, err := attngit.PreserveWorktreeOnBackup(branchWorktree, branch, backup, head, worktreeFingerprint)
			if err != nil {
				return "", false, err
			}
			receipt.BackupHead = protocol.Ptr(backupHead)
			if err := d.saveDelegationPRProgress(operationID, receipt); err != nil {
				return "", false, fmt.Errorf("record verified pull request backup %s at %s: %w", backup, backupHead, err)
			}
			if err := advanceDelegationPRBranch(target.mainRepo, branch, receipt.HeadSHA, head); err != nil {
				return "", false, err
			}
			if err := attngit.SwitchWorktreeToBranch(branchWorktree, branch, receipt.HeadSHA); err != nil {
				return "", false, err
			}
			receipt.Disposition = "preserved"
		}
		if branchWorktreeOwned {
			receipt.Disposition = "created"
		}
		path, created, err := d.finishDelegationPRCheckout(target, branchWorktree, branchWorktreeOwned, request, operationID)
		if err == nil {
			keepRemote = true
		}
		return path, created, err
	}

	if ownerPath, owner, found, err := d.liveDelegationPRBranchOwner(target.mainRepo, branch); err != nil {
		return "", false, err
	} else if found {
		return "", false, fmt.Errorf("pull request %s branch %s became owned by live attn session %s in %s; state was left untouched", receipt.URL, branch, owner, ownerPath)
	}
	if oldHead, exists, err := attngit.BranchHead(target.mainRepo, branch); err != nil {
		return "", false, err
	} else if exists {
		if !strings.EqualFold(oldHead, receipt.HeadSHA) && !attngit.IsAncestor(target.mainRepo, oldHead, receipt.HeadSHA) {
			if savedSource := protocol.Deref(receipt.BackupSourceHead); savedSource != "" && savedSource != oldHead {
				return "", false, fmt.Errorf("pull request branch %s moved from planned backup source %s to %s; state was left untouched", branch, savedSource, oldHead)
			}
			backup := protocol.Deref(receipt.BackupBranch)
			if backup == "" {
				backup = delegationPRBackupName(target.mainRepo, branch)
				receipt.BackupBranch = protocol.Ptr(backup)
				receipt.BackupSourceHead = protocol.Ptr(oldHead)
				if err := d.saveDelegationPRProgress(operationID, receipt); err != nil {
					return "", false, err
				}
			}
			if existingBackup, ok, backupErr := attngit.BranchHead(target.mainRepo, backup); backupErr != nil {
				return "", false, backupErr
			} else if ok {
				expectedBackup := protocol.Deref(receipt.BackupHead)
				if expectedBackup == "" {
					expectedBackup = protocol.Deref(receipt.BackupSourceHead)
				}
				if existingBackup != expectedBackup {
					return "", false, fmt.Errorf("backup branch %s moved from %s to %s; state was left untouched", backup, expectedBackup, existingBackup)
				}
			} else if err := attngit.CreateBranchAt(target.mainRepo, backup, oldHead); err != nil {
				return "", false, err
			}
			attngit.UnsetBranchUpstream(target.mainRepo, backup)
			verifiedBackup, ok, verifyErr := attngit.BranchHead(target.mainRepo, backup)
			if verifyErr != nil || !ok || verifiedBackup != oldHead {
				return "", false, fmt.Errorf("verify backup branch %s at %s", backup, oldHead)
			}
			receipt.BackupHead = protocol.Ptr(verifiedBackup)
			if err := d.saveDelegationPRProgress(operationID, receipt); err != nil {
				return "", false, err
			}
			receipt.Disposition = "preserved"
		}
		if err := attngit.DeleteBranchAt(target.mainRepo, branch, oldHead); err != nil {
			return "", false, err
		}
	}
	request.Branch = branch
	request.StartingFrom = protocol.Ptr(receipt.HeadSHA)
	path, created, err := d.createDelegationWorktree(target.mainRepo, target.mainRepo, request, operationID, ownedPath, worktreeOwned, worktreeToken, false, true)
	if err != nil {
		return "", false, err
	}
	receipt.Disposition = "created"
	if !created {
		receipt.Disposition = "reused"
	}
	finishedPath, finishedCreated, err := d.finishDelegationPRCheckout(target, path, created, request, operationID)
	if err != nil && created {
		rollback := d.newDelegationRollback()
		rollback.onWorktreeCreated(path)
		return "", false, rollback.fail(err)
	}
	if err == nil {
		keepRemote = true
	}
	return finishedPath, finishedCreated, err
}

func (d *Daemon) finishDelegationPRCheckout(target *delegationPRTarget, path string, created bool, request *protocol.DelegateWorktreeRequest, operationID string) (string, bool, error) {
	receipt := target.receipt
	if requested := strings.TrimSpace(protocol.Deref(request.Path)); requested != "" && attngit.CanonicalizePath(requested) != attngit.CanonicalizePath(path) {
		return "", false, fmt.Errorf("pull request branch %s already uses %s, not requested worktree path %s", receipt.HeadBranch, path, requested)
	}
	head, err := attngit.GetHeadCommit(path)
	if err != nil {
		return "", false, fmt.Errorf("verify pull request checkout HEAD: %w", err)
	}
	if !strings.EqualFold(head, receipt.HeadSHA) {
		return "", false, fmt.Errorf("pull request checkout HEAD mismatch in %s: got %s want %s", path, head, receipt.HeadSHA)
	}
	branch, err := attngit.GetCurrentBranch(path)
	if err != nil || branch != receipt.LocalBranch {
		return "", false, fmt.Errorf("pull request checkout branch mismatch in %s: got %s want %s", path, branch, receipt.LocalBranch)
	}
	headRepo := strings.TrimPrefix(receipt.HeadRepository, target.host+"/")
	remote, ok := attngit.RemoteForIdentity(target.mainRepo, target.host+"/"+headRepo)
	if !ok {
		return "", false, fmt.Errorf("pull request head remote %s is missing after checkout", receipt.HeadRepository)
	}
	if err := attngit.SetBranchUpstream(target.mainRepo, receipt.LocalBranch, remote, receipt.HeadBranch); err != nil {
		return "", false, err
	}
	clean, err := attngit.IsWorktreeClean(path)
	if err != nil || !clean {
		return "", false, fmt.Errorf("pull request checkout %s is dirty after materialization", path)
	}
	fingerprint, err := attngit.StatusFingerprint(path)
	if err != nil {
		return "", false, err
	}
	receipt.WorktreePath = attngit.CanonicalizePath(path)
	receipt.VerifiedHead = head
	receipt.CheckoutFingerprint = protocol.Ptr(fingerprint)
	if receipt.Disposition == "" {
		if created {
			receipt.Disposition = "created"
		} else {
			receipt.Disposition = "reused"
		}
	}
	if err := d.saveDelegationPRProgress(operationID, receipt); err != nil {
		return "", false, err
	}
	return path, created, nil
}
