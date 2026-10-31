package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const (
	continuationSourceExecution = "execution"

	directoryPresent     = "present"
	directoryMissing     = "missing"
	directoryUnavailable = "unavailable"
	directoryUnknown     = "unknown"

	handoverReuseCwd       = "reuse_cwd"
	handoverRecreateBranch = "recreate_branch"
	handoverNeedsPlacement = "placement_required"
)

type seedContinuation struct {
	Execution         garden.Execution
	Resume            string
	Source            string
	LedgerAvailable   bool
	SessionLive       bool
	DirectoryState    string
	KeptConversation  *protocol.KeptConversation
	ResumeAvailable   bool
	ResumeReason      string
	HandoverPlacement string
	PlacementReason   string
}

func (d *Daemon) gardenTime() time.Time {
	if d.gardenNow != nil {
		return d.gardenNow().UTC()
	}
	return time.Now().UTC()
}

func formatGardenTime(at time.Time) string {
	return at.UTC().Format(time.RFC3339Nano)
}

func (d *Daemon) initializeSeedLifecycle(seed garden.Seed) garden.Seed {
	if strings.TrimSpace(seed.StateChangedAt) == "" {
		seed.StateChangedAt = formatGardenTime(d.gardenTime())
	}
	return seed
}

func snapshotGardenExecution(session *protocol.Session, now time.Time) garden.Execution {
	if session == nil {
		return garden.Execution{}
	}
	execution := garden.Execution{
		SessionID:      protocol.TrimID(session.ID),
		Cwd:            strings.TrimSpace(session.Directory),
		Agent:          strings.TrimSpace(session.Agent),
		CapturedAt:     formatGardenTime(now),
		Branch:         strings.TrimSpace(protocol.Deref(session.Branch)),
		RepositoryRoot: strings.TrimSpace(protocol.Deref(session.MainRepo)),
	}
	return execution
}

func (d *Daemon) observedGardenExecution(session *protocol.Session, now time.Time) garden.Execution {
	execution := snapshotGardenExecution(session, now)
	if execution.Cwd == "" {
		return execution
	}
	var checkoutRoot, mainRepo, branch string
	err := d.gitExecution().Run(context.Background(), gitTask{Kind: gitTaskGarden, Lane: gitDeferred}, func(ctx context.Context, client *attngit.Client) error {
		root, rootErr := client.GetRepoRoot(ctx, execution.Cwd)
		if rootErr != nil {
			return rootErr
		}
		checkoutRoot = root
		mainRepo = client.ResolveMainRepoPath(ctx, root)
		if info, _ := client.GetBranchInfo(ctx, execution.Cwd); info != nil {
			branch = info.Branch
		}
		return nil
	})
	if err != nil || checkoutRoot == "" {
		execution.Branch = strings.TrimSpace(protocol.Deref(session.Branch))
		return execution
	}
	canonicalCwd := attngit.CanonicalizePath(execution.Cwd)
	canonicalRoot := attngit.CanonicalizePath(checkoutRoot)
	if rel, relErr := filepath.Rel(canonicalRoot, canonicalCwd); relErr == nil && rel != "." && rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		execution.RepositorySubdir = rel
	}
	execution.RepositoryRoot = mainRepo
	if branch != "" {
		execution.Branch = strings.TrimSpace(branch)
	}
	if execution.Branch == "" {
		execution.Branch = strings.TrimSpace(protocol.Deref(session.Branch))
	}
	return execution
}

func mergeGardenExecution(current, observed garden.Execution) garden.Execution {
	next := current
	if observed.SessionID != "" {
		next.SessionID = observed.SessionID
	}
	if observed.Cwd != "" {
		next.Cwd = observed.Cwd
	}
	if observed.Agent != "" {
		next.Agent = observed.Agent
	}
	if observed.RepositoryRoot != "" {
		next.RepositoryRoot = observed.RepositoryRoot
	}
	if observed.RepositorySubdir != "" {
		next.RepositorySubdir = observed.RepositorySubdir
	}
	if observed.Branch != "" {
		next.Branch = observed.Branch
	}
	if observed.CapturedAt != "" {
		next.CapturedAt = observed.CapturedAt
	}
	return next
}

func (d *Daemon) updateGardenDispatch(
	sessionID protocol.SessionID,
	update func(garden.Execution) (garden.Execution, bool, error),
) (garden.Execution, error) {
	sessionID = protocol.TrimID(sessionID)
	if sessionID == "" {
		return garden.Execution{}, errors.New("garden execution needs a session id")
	}
	schema, err := d.dispatchesCollection()
	if err != nil {
		return garden.Execution{}, err
	}
	const attempts = 3
	var lastErr error
	for range attempts {
		current, doc, found, readErr := d.gardenDispatchDocument(sessionID)
		if readErr != nil {
			return garden.Execution{}, readErr
		}
		if !found {
			current.SessionID = sessionID
		}
		next, changed, updateErr := update(current)
		if updateErr != nil {
			return garden.Execution{}, updateErr
		}
		next.SessionID = sessionID
		if !changed {
			return current, nil
		}
		body, encodeErr := next.Encode()
		if encodeErr != nil {
			return garden.Execution{}, encodeErr
		}
		expected := docstore.ExpectAbsent
		if found {
			expected = doc.Rev
		}
		fact := documentChangedFact(garden.Namespace, garden.CollectionDispatches, string(sessionID), false)
		commit := store.DocumentCommit{Write: store.DocumentWrite{
			Schema: *schema, ID: string(sessionID), Body: body, Expected: &expected,
		}, Fact: fact}
		var written store.DocumentWriteResult
		var writeErr error
		if current.Crown == "" && next.Crown != "" {
			var results []store.DocumentWriteResult
			watcher, _ := next.Dispatcher.Party()
			d.lockGardenRoles()
			results, writeErr = d.store.CommitGardenDispatchWrites([]store.DocumentCommit{commit}, store.SeedWatch{
				Watcher: watcher, SeedID: next.Crown,
			}, d.gardenTime())
			d.unlockGardenRoles()
			if writeErr == nil {
				written = results[0]
			}
		} else {
			written, writeErr = d.store.CommitDocumentWrite(commit.Write, fact, d.gardenTime())
		}
		if writeErr != nil {
			if docstore.IsConflict(writeErr) {
				lastErr = writeErr
				continue
			}
			return garden.Execution{}, writeErr
		}
		d.announceCommittedWrite(fact, written.Seq)
		d.rememberDispatchProjection(sessionID, next, written.Rev)
		return next, nil
	}
	return garden.Execution{}, fmt.Errorf(
		"dispatch %s changed under all %d metadata refresh attempts: %w", sessionID, attempts, lastErr)
}

func activeDispatchCrown(dispatch garden.Execution) string {
	if protocol.TrimID(dispatch.SupersededBy) != "" {
		return ""
	}
	return strings.TrimSpace(dispatch.Crown)
}

func (d *Daemon) captureGardenSessionExecution(session *protocol.Session) (garden.Execution, error) {
	if session == nil || protocol.TrimID(session.ID) == "" {
		return garden.Execution{}, errors.New("garden execution needs a tracked session")
	}
	startedAt := d.gardenTime()
	observed := d.observedGardenExecution(session, startedAt)
	return d.updateGardenDispatch(session.ID, func(current garden.Execution) (garden.Execution, bool, error) {
		if crown := activeDispatchCrown(current); crown != "" {
			seed, _, err := d.readSeed(crown)
			if err != nil {
				return current, false, err
			}
			profileID, err := d.store.SessionProfileID(session.ID)
			if err != nil {
				return current, false, err
			}
			if seed.ProfileID != profileID {
				return current, false, nil
			}
		}
		if capturedAt, err := time.Parse(time.RFC3339Nano, current.CapturedAt); err == nil && capturedAt.After(startedAt) {
			return current, false, nil
		}
		return mergeGardenExecution(current, observed), true, nil
	})
}

func (d *Daemon) captureGardenSessionSnapshot(session *protocol.Session) (garden.Execution, error) {
	return d.updateGardenDispatch(session.ID, func(current garden.Execution) (garden.Execution, bool, error) {
		if crown := activeDispatchCrown(current); crown != "" {
			seed, _, err := d.readSeed(crown)
			if err != nil {
				return current, false, err
			}
			profileID, err := d.store.SessionProfileID(session.ID)
			if err != nil {
				return current, false, err
			}
			if seed.ProfileID != profileID {
				return current, false, nil
			}
		}
		observed := snapshotGardenExecution(session, d.gardenTime())
		return mergeGardenExecution(current, observed), true, nil
	})
}

func (d *Daemon) ensureGardenExecution(sessionID protocol.SessionID) (garden.Execution, error) {
	session := d.store.Get(protocol.TrimID(sessionID))
	if session == nil {
		if execution, ok := d.gardenDispatch(sessionID); ok && strings.TrimSpace(execution.Cwd) != "" && strings.TrimSpace(execution.Agent) != "" {
			return execution, nil
		}
		return garden.Execution{}, fmt.Errorf("session %s is not tracked, so its execution context cannot be saved", protocol.TrimID(sessionID))
	}
	return d.captureGardenSessionExecution(session)
}

func (d *Daemon) captureGardenExecutionsInDirectory(path string) {
	wanted := attngit.CanonicalizePath(path)
	for _, session := range d.store.List("") {
		if !pathAtOrBelow(session.Directory, wanted) {
			continue
		}
		if _, err := d.captureGardenSessionExecution(session); err != nil {
			d.logf("garden: preserving execution %s before directory removal: %v", session.ID, err)
		}
	}
}

func pathAtOrBelow(path, root string) bool {
	path = attngit.CanonicalizePath(path)
	root = attngit.CanonicalizePath(root)
	if path == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (d *Daemon) gardenKeepsBranch(repository, branch string) bool {
	repository = attngit.CanonicalizePath(repository)
	branch = strings.TrimSpace(branch)
	if repository == "" || branch == "" {
		return false
	}
	after := ""
	for {
		read, _, err := d.runDocQuery(docstore.Query{
			Namespace: garden.Namespace, Collection: garden.CollectionSeeds,
			Limit: docstore.MaxLimit, After: after,
		})
		if err != nil {
			d.logf("garden: checking whether %s still owns branch %s: %v", repository, branch, err)
			return true
		}
		for _, doc := range read.Documents {
			seed, err := garden.Decode(doc.Body)
			if err != nil || garden.Closed(seed.Status) || protocol.TrimID(seed.LastExecutionID) == "" {
				continue
			}
			execution, ok := d.gardenDispatch(seed.LastExecutionID)
			if ok && attngit.CanonicalizePath(execution.RepositoryRoot) == repository &&
				strings.TrimSpace(execution.Branch) == branch {
				return true
			}
		}
		if len(read.Documents) < docstore.MaxLimit {
			return false
		}
		after = read.Documents[len(read.Documents)-1].ID
	}
}

func (d *Daemon) normalizedSeedContinuation(seed garden.Seed) (garden.Execution, string, bool) {
	if executionID := protocol.TrimID(seed.LastExecutionID); executionID != "" {
		if execution, ok := d.gardenDispatch(executionID); ok {
			entry := d.store.SessionLedgerEntry(executionID)
			foreign := entry != nil && entry.ProfileID != seed.ProfileID
			if entry == nil {
				if live := d.store.Get(executionID); live != nil {
					foreign = live.ProfileID != seed.ProfileID
				}
			}
			if foreign {
				return garden.Execution{SessionID: executionID}, continuationSourceExecution, true
			}
			if entry != nil && entry.ProfileID == seed.ProfileID {
				execution.SessionID = entry.ID
				execution.Cwd = entry.Directory
				execution.Agent = entry.Agent
			}
			return execution, continuationSourceExecution, true
		}
	}
	return garden.Execution{}, "", false
}

func inspectContinuationDirectory(execution garden.Execution) string {
	if strings.TrimSpace(execution.Cwd) == "" {
		return directoryUnknown
	}
	info, err := os.Stat(execution.Cwd)
	switch {
	case err == nil && info.IsDir():
		return directoryPresent
	case err == nil:
		return directoryUnavailable
	case os.IsNotExist(err):
		return directoryMissing
	default:
		return directoryUnavailable
	}
}

func savedWorktreeRoot(execution garden.Execution) (string, bool) {
	cwd := attngit.CanonicalizePath(strings.TrimSpace(execution.Cwd))
	if cwd == "" {
		return "", false
	}
	subdir := filepath.Clean(strings.TrimSpace(execution.RepositorySubdir))
	if subdir == "" || subdir == "." {
		return cwd, true
	}
	if filepath.IsAbs(subdir) || subdir == ".." || strings.HasPrefix(subdir, ".."+string(filepath.Separator)) {
		return "", false
	}
	root := cwd
	for range strings.Split(subdir, string(filepath.Separator)) {
		root = filepath.Dir(root)
	}
	root = attngit.CanonicalizePath(root)
	if attngit.CanonicalizePath(filepath.Join(root, subdir)) != cwd {
		return "", false
	}
	return root, true
}

func (d *Daemon) branchCanBeRecreated(execution garden.Execution) (string, bool, string) {
	repo := strings.TrimSpace(execution.RepositoryRoot)
	branch := strings.TrimSpace(execution.Branch)
	if repo == "" || branch == "" {
		return "", false, "the saved execution has no verified repository and branch"
	}
	target, ok := savedWorktreeRoot(execution)
	if !ok {
		return "", false, "the saved worktree location could not be reconstructed"
	}
	if _, err := os.Stat(target); err == nil {
		return "", false, "the saved worktree location already exists"
	} else if !os.IsNotExist(err) {
		return "", false, "the saved worktree location could not be verified"
	}
	if _, err := os.Stat(repo); err != nil {
		return "", false, "the saved repository is unavailable"
	}
	var branchExists bool
	var worktrees []attngit.WorktreeEntry
	err := d.gitExecution().Run(context.Background(), gitTask{Kind: gitTaskGarden, Lane: gitInteractive}, func(ctx context.Context, client *attngit.Client) error {
		exists, refErr := client.RefExists(ctx, repo, branch)
		branchExists = exists
		if refErr != nil || !exists {
			return refErr
		}
		live, listErr := client.ObserveLiveWorktrees(ctx, repo)
		worktrees = live
		return listErr
	})
	if err != nil {
		return "", false, "the saved repository could not be inspected"
	}
	if !branchExists {
		return "", false, "the saved branch no longer exists"
	}
	for _, worktree := range worktrees {
		if strings.TrimSpace(worktree.Branch) == branch {
			return "", false, "the saved branch is already checked out"
		}
	}
	return target, true, ""
}

func (d *Daemon) continuationForSeed(seed garden.Seed) *seedContinuation {
	return d.continuationForSeedForeground(seed)
}

func (d *Daemon) continuationForSeedForeground(seed garden.Seed) *seedContinuation {
	return d.continuationForSeedWithResumeCheck(seed, d.conversationResumable)
}

func (d *Daemon) continuationForSeedWithResumeCheck(seed garden.Seed, checkResume func(string, string, string) (bool, string)) *seedContinuation {
	execution, source, ok := d.normalizedSeedContinuation(seed)
	if !ok {
		return nil
	}
	entry := d.store.SessionLedgerEntry(execution.SessionID)
	continuation := &seedContinuation{
		Execution:         execution,
		KeptConversation:  d.keptConversationForSession(execution.SessionID),
		Source:            source,
		LedgerAvailable:   entry != nil,
		DirectoryState:    inspectContinuationDirectory(execution),
		HandoverPlacement: handoverNeedsPlacement,
	}
	if entry != nil && entry.ProfileID == seed.ProfileID {
		continuation.Resume = strings.TrimSpace(d.store.GetResumeSessionID(entry.ID))
	}
	if live := d.store.Get(protocol.TrimID(execution.SessionID)); live != nil && d.sessionHasLiveWorker(live.ID) {
		continuation.SessionLive = true
		continuation.ResumeAvailable = true
		if continuation.DirectoryState == directoryPresent {
			continuation.HandoverPlacement = handoverReuseCwd
		} else {
			continuation.PlacementReason = "choose a reachable place for the new agent"
		}
		return continuation
	}
	if !continuation.LedgerAvailable {
		continuation.ResumeReason = "the original session is no longer in the ledger"
		d.planSeedHandoverPlacement(continuation)
		return continuation
	}
	d.planSeedHandoverPlacement(continuation)
	if _, ok := d.store.LaunchIntent(execution.SessionID); !ok {
		continuation.ResumeReason = "the session ledger has no saved launch contract"
		return continuation
	}
	if continuation.DirectoryState != directoryPresent {
		switch continuation.DirectoryState {
		case directoryMissing:
			continuation.ResumeReason = fmt.Sprintf("the original directory no longer exists: %s", execution.Cwd)
		case directoryUnknown:
			continuation.ResumeReason = "the original directory was not saved"
		default:
			continuation.ResumeReason = fmt.Sprintf("the original directory cannot be opened: %s", execution.Cwd)
		}
		return continuation
	}
	resumeID := continuation.Resume
	agentName := strings.TrimSpace(execution.Agent)
	if resumeID == "" || agentName == "" {
		continuation.ResumeReason = "the original conversation identity is unavailable"
		continuation.HandoverPlacement = handoverReuseCwd
		return continuation
	}
	if ok, reason := checkResume(agentName, resumeID, execution.Cwd); !ok {
		continuation.ResumeReason = "the original conversation is unavailable: " + reason
		continuation.HandoverPlacement = handoverReuseCwd
		return continuation
	}
	continuation.ResumeAvailable = true
	continuation.HandoverPlacement = handoverReuseCwd
	return continuation
}

func (d *Daemon) planSeedHandoverPlacement(continuation *seedContinuation) {
	switch continuation.DirectoryState {
	case directoryPresent:
		continuation.HandoverPlacement = handoverReuseCwd
	case directoryMissing:
		if _, safe, reason := d.branchCanBeRecreated(continuation.Execution); safe {
			continuation.HandoverPlacement = handoverRecreateBranch
		} else {
			continuation.PlacementReason = reason
		}
	case directoryUnknown:
		continuation.PlacementReason = "choose a directory for the new agent"
	default:
		continuation.PlacementReason = "the original directory could not be verified"
	}
}

func continuationToProtocol(continuation *seedContinuation) *protocol.SeedContinuation {
	if continuation == nil {
		return nil
	}
	execution := continuation.Execution
	out := &protocol.SeedContinuation{
		ExecutionID:       string(execution.SessionID),
		KeptConversation:  continuation.KeptConversation,
		Source:            continuation.Source,
		SessionLive:       continuation.SessionLive,
		Agent:             execution.Agent,
		Cwd:               execution.Cwd,
		DirectoryState:    continuation.DirectoryState,
		ResumeAvailable:   continuation.ResumeAvailable,
		HandoverPlacement: continuation.HandoverPlacement,
	}
	if value := continuation.Resume; value != "" {
		out.NativeConversationID = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(execution.RepositoryRoot); value != "" {
		out.RepositoryRoot = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(execution.RepositorySubdir); value != "" {
		out.RepositorySubdir = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(execution.Branch); value != "" {
		out.Branch = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(continuation.ResumeReason); value != "" {
		out.ResumeReason = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(continuation.PlacementReason); value != "" {
		out.PlacementReason = protocol.Ptr(value)
	}
	return out
}

func (d *Daemon) decorateSeedContinuation(wire *protocol.Seed, seed garden.Seed) {
	if wire == nil {
		return
	}
	wire.Continuation = continuationToProtocol(d.continuationForSeed(seed))
}
