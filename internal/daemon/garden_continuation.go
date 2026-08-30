package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const (
	continuationSourceExecution = "execution"
	continuationSourceLegacy    = "legacy"

	directoryPresent     = "present"
	directoryMissing     = "missing"
	directoryRemote      = "remote"
	directoryUnavailable = "unavailable"
	directoryUnknown     = "unknown"

	handoverReuseCwd       = "reuse_cwd"
	handoverRecreateBranch = "recreate_branch"
	handoverNeedsPlacement = "placement_required"
)

type seedContinuation struct {
	Execution         garden.Dispatch
	Source            string
	SessionLive       bool
	DirectoryState    string
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

func (d *Daemon) gardenSession(sessionID string) *protocol.Session {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	if session := d.store.Get(sessionID); session != nil {
		return session
	}
	if d.hubManager != nil {
		return d.hubManager.RemoteSession(sessionID)
	}
	return nil
}

func observedGardenExecution(session *protocol.Session, resumeID string, now time.Time) garden.Dispatch {
	if session == nil {
		return garden.Dispatch{}
	}
	execution := garden.Dispatch{
		SessionID:  strings.TrimSpace(session.ID),
		Cwd:        strings.TrimSpace(session.Directory),
		Agent:      strings.TrimSpace(string(session.Agent)),
		Resume:     strings.TrimSpace(resumeID),
		CapturedAt: formatGardenTime(now),
	}
	if endpointID := strings.TrimSpace(protocol.Deref(session.EndpointID)); endpointID != "" {
		execution.HostKind = garden.HostRemote
		execution.EndpointID = endpointID
		execution.RepositoryRoot = strings.TrimSpace(protocol.Deref(session.MainRepo))
		execution.Branch = strings.TrimSpace(protocol.Deref(session.Branch))
		return execution
	}

	execution.HostKind = garden.HostLocal
	if execution.Cwd == "" {
		return execution
	}
	checkoutRoot, err := attngit.GetRepoRoot(execution.Cwd)
	if err != nil || checkoutRoot == "" {
		execution.Branch = strings.TrimSpace(protocol.Deref(session.Branch))
		return execution
	}
	canonicalCwd := attngit.CanonicalizePath(execution.Cwd)
	canonicalRoot := attngit.CanonicalizePath(checkoutRoot)
	if rel, relErr := filepath.Rel(canonicalRoot, canonicalCwd); relErr == nil && rel != "." && rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		execution.RepositorySubdir = rel
	}
	execution.RepositoryRoot = attngit.ResolveMainRepoPath(checkoutRoot)
	if info, infoErr := attngit.GetBranchInfo(execution.Cwd); infoErr == nil && info != nil {
		execution.Branch = strings.TrimSpace(info.Branch)
	}
	if execution.Branch == "" {
		execution.Branch = strings.TrimSpace(protocol.Deref(session.Branch))
	}
	return execution
}

func mergeGardenExecution(current, observed garden.Dispatch) garden.Dispatch {
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
	if observed.Resume != "" {
		next.Resume = observed.Resume
	}
	if observed.HostKind != "" {
		next.HostKind = observed.HostKind
	}
	if observed.EndpointID != "" {
		next.EndpointID = observed.EndpointID
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
	sessionID string,
	update func(garden.Dispatch) (garden.Dispatch, bool, error),
) (garden.Dispatch, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return garden.Dispatch{}, errors.New("garden execution needs a session id")
	}
	schema, err := d.dispatchesCollection()
	if err != nil {
		return garden.Dispatch{}, err
	}
	const attempts = 3
	var lastErr error
	for range attempts {
		current, doc, found, readErr := d.gardenDispatchDocument(sessionID)
		if readErr != nil {
			return garden.Dispatch{}, readErr
		}
		if !found {
			current.SessionID = sessionID
		}
		next, changed, updateErr := update(current)
		if updateErr != nil {
			return garden.Dispatch{}, updateErr
		}
		next.SessionID = sessionID
		if !changed {
			return current, nil
		}
		body, encodeErr := next.Encode()
		if encodeErr != nil {
			return garden.Dispatch{}, encodeErr
		}
		expected := docstore.ExpectAbsent
		if found {
			expected = doc.Rev
		}
		if d.gardenDispatchBeforeWrite != nil {
			d.gardenDispatchBeforeWrite(sessionID)
		}
		fact := documentChangedFact(garden.Namespace, garden.CollectionDispatches, sessionID, false)
		written, writeErr := d.store.CommitDocumentWrite(store.DocumentWrite{
			Schema: *schema, ID: sessionID, Body: body, Expected: &expected,
		}, fact, d.gardenTime())
		if writeErr != nil {
			if docstore.IsConflict(writeErr) {
				lastErr = writeErr
				continue
			}
			return garden.Dispatch{}, writeErr
		}
		d.announceCommittedWrite(fact, written.Seq)
		if d.gardenDispatchAfterWrite != nil {
			d.gardenDispatchAfterWrite(sessionID)
		}
		d.rememberDispatchProjection(sessionID, next, written.Rev)
		return next, nil
	}
	return garden.Dispatch{}, fmt.Errorf(
		"dispatch %s changed under all %d metadata refresh attempts: %w", sessionID, attempts, lastErr)
}

func activeDispatchCrown(dispatch garden.Dispatch) string {
	if strings.TrimSpace(dispatch.SupersededBy) != "" {
		return ""
	}
	return strings.TrimSpace(dispatch.Crown)
}

func (d *Daemon) captureGardenSessionExecution(session *protocol.Session) (garden.Dispatch, error) {
	if session == nil || strings.TrimSpace(session.ID) == "" {
		return garden.Dispatch{}, errors.New("garden execution needs a tracked session")
	}
	resumeID := ""
	if d.store.Get(session.ID) != nil {
		resumeID = d.store.GetResumeSessionID(session.ID)
	}
	observed := observedGardenExecution(session, resumeID, d.gardenTime())
	return d.updateGardenDispatch(session.ID, func(current garden.Dispatch) (garden.Dispatch, bool, error) {
		return mergeGardenExecution(current, observed), true, nil
	})
}

func (d *Daemon) ensureGardenExecution(sessionID string) (garden.Dispatch, error) {
	session := d.gardenSession(sessionID)
	if session == nil {
		if execution, ok := d.gardenDispatch(sessionID); ok && strings.TrimSpace(execution.Cwd) != "" && strings.TrimSpace(execution.Agent) != "" {
			return execution, nil
		}
		return garden.Dispatch{}, fmt.Errorf("session %s is not tracked, so its execution context cannot be saved", strings.TrimSpace(sessionID))
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
			if err != nil || garden.Closed(seed.Status) || strings.TrimSpace(seed.LastExecutionID) == "" {
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

func (d *Daemon) normalizedSeedContinuation(seed garden.Seed) (garden.Dispatch, string, bool) {
	if executionID := strings.TrimSpace(seed.LastExecutionID); executionID != "" {
		if execution, ok := d.gardenDispatch(executionID); ok {
			return execution, continuationSourceExecution, true
		}
	}
	resumeID := strings.TrimSpace(seed.ResumeSessionID)
	cwd := strings.TrimSpace(seed.ResumeCwd)
	agent := strings.TrimSpace(seed.ResumeAgent)
	if resumeID == "" || cwd == "" || agent == "" {
		return garden.Dispatch{}, "", false
	}
	return garden.Dispatch{
		SessionID: resumeID,
		Cwd:       cwd,
		Agent:     agent,
		Resume:    resumeID,
		HostKind:  garden.HostLocal,
	}, continuationSourceLegacy, true
}

func inspectContinuationDirectory(execution garden.Dispatch) string {
	switch strings.TrimSpace(execution.HostKind) {
	case garden.HostRemote:
		return directoryRemote
	case garden.HostLocal:
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
	default:
		return directoryUnknown
	}
}

func savedWorktreeRoot(execution garden.Dispatch) (string, bool) {
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

func branchCanBeRecreated(execution garden.Dispatch) (string, bool, string) {
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
	if !attngit.RefExists(repo, branch) {
		return "", false, "the saved branch no longer exists"
	}
	worktrees, err := attngit.ListWorktrees(repo)
	if err != nil {
		return "", false, "the saved repository could not be inspected"
	}
	for _, worktree := range worktrees {
		if strings.TrimSpace(worktree.Branch) == branch {
			return "", false, "the saved branch is already checked out"
		}
	}
	return target, true, ""
}

func (d *Daemon) continuationForSeed(seed garden.Seed) *seedContinuation {
	execution, source, ok := d.normalizedSeedContinuation(seed)
	if !ok {
		return nil
	}
	continuation := &seedContinuation{
		Execution:         execution,
		Source:            source,
		DirectoryState:    inspectContinuationDirectory(execution),
		HandoverPlacement: handoverNeedsPlacement,
	}
	if live := d.gardenSession(execution.SessionID); live != nil {
		continuation.SessionLive = true
		continuation.ResumeAvailable = true
		if execution.HostKind == garden.HostLocal && continuation.DirectoryState == directoryPresent {
			continuation.HandoverPlacement = handoverReuseCwd
		} else {
			continuation.PlacementReason = "choose a reachable place for the new agent"
		}
		return continuation
	}
	if execution.HostKind != garden.HostLocal {
		continuation.ResumeReason = "the original host is not available here"
		continuation.PlacementReason = "choose a reachable place for the new agent"
		return continuation
	}
	if continuation.DirectoryState != directoryPresent {
		switch continuation.DirectoryState {
		case directoryMissing:
			continuation.ResumeReason = fmt.Sprintf("the original directory no longer exists: %s", execution.Cwd)
			if _, safe, reason := branchCanBeRecreated(execution); safe {
				continuation.HandoverPlacement = handoverRecreateBranch
			} else {
				continuation.PlacementReason = reason
			}
		case directoryUnknown:
			continuation.ResumeReason = "the original directory was not saved"
			continuation.PlacementReason = "choose a directory for the new agent"
		default:
			continuation.ResumeReason = fmt.Sprintf("the original directory cannot be opened: %s", execution.Cwd)
			continuation.PlacementReason = "the original directory could not be verified"
		}
		return continuation
	}
	resumeID := strings.TrimSpace(execution.Resume)
	agentName := strings.TrimSpace(execution.Agent)
	if resumeID == "" || agentName == "" {
		continuation.ResumeReason = "the original conversation identity is unavailable"
		continuation.HandoverPlacement = handoverReuseCwd
		return continuation
	}
	driver := agentdriver.Get(agentName)
	if !agentdriver.ResumeAvailable(driver, resumeID) {
		continuation.ResumeReason = "the original conversation is unavailable"
		continuation.HandoverPlacement = handoverReuseCwd
		return continuation
	}
	continuation.ResumeAvailable = true
	continuation.HandoverPlacement = handoverReuseCwd
	return continuation
}

func continuationToProtocol(continuation *seedContinuation) *protocol.SeedContinuation {
	if continuation == nil {
		return nil
	}
	execution := continuation.Execution
	out := &protocol.SeedContinuation{
		ExecutionID:       execution.SessionID,
		Source:            continuation.Source,
		SessionLive:       continuation.SessionLive,
		Agent:             execution.Agent,
		Cwd:               execution.Cwd,
		HostKind:          execution.HostKind,
		DirectoryState:    continuation.DirectoryState,
		ResumeAvailable:   continuation.ResumeAvailable,
		HandoverPlacement: continuation.HandoverPlacement,
	}
	if out.HostKind == "" {
		out.HostKind = directoryUnknown
	}
	if value := strings.TrimSpace(execution.Resume); value != "" {
		out.NativeConversationID = protocol.Ptr(value)
	}
	if value := strings.TrimSpace(execution.EndpointID); value != "" {
		out.EndpointID = protocol.Ptr(value)
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
