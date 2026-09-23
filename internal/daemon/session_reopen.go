package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const (
	branchStateLocal      = "local"
	branchStateRemoteOnly = "remote_only"
	branchStateGone       = "gone"
	branchStateMerged     = "merged"
	branchStateUnknown    = "unknown"
)

type branchInspection struct {
	State             string
	Remote            string
	AlreadyCheckedOut bool
	RepoMissing       bool
	StaleRegistration bool
}

type sessionReopenVerdict struct {
	SessionID      string
	Entry          *protocol.SessionLedgerEntry
	Execution      garden.Dispatch
	Live           bool
	Reopenable     bool
	Reason         string
	Warning        string
	Actions        []protocol.SessionReopenAction
	Checking       bool
	DirectoryState string
	BranchState    string
	ProfileID      string
	ProfileDeleted bool
	RecreatePath   string
}

func (v *sessionReopenVerdict) offers(action protocol.SessionReopenAction) bool {
	for _, offered := range v.Actions {
		if offered == action {
			return true
		}
	}
	return false
}

func (v *sessionReopenVerdict) toProtocol() *protocol.SessionReopen {
	out := &protocol.SessionReopen{
		Reopenable:     v.Reopenable,
		Actions:        v.Actions,
		Checking:       v.Checking,
		DirectoryState: v.DirectoryState,
		ProfileID:      v.ProfileID,
		ProfileDeleted: v.ProfileDeleted,
	}
	if out.Actions == nil {
		out.Actions = []protocol.SessionReopenAction{}
	}
	if v.Reason != "" {
		out.Reason = protocol.Ptr(v.Reason)
	}
	if v.Warning != "" {
		out.Warning = protocol.Ptr(v.Warning)
	}
	if v.BranchState != "" {
		out.BranchState = protocol.Ptr(v.BranchState)
	}
	return out
}

func (d *Daemon) reopenExecutionFromLedger(entry *protocol.SessionLedgerEntry) garden.Dispatch {
	execution, _ := d.gardenDispatch(entry.ID)
	execution.SessionID = entry.ID
	if execution.Cwd == "" {
		execution.Cwd = entry.Directory
	}
	if execution.Agent == "" {
		execution.Agent = entry.Agent
	}
	if execution.Branch == "" {
		execution.Branch = protocol.Deref(entry.Branch)
	}
	if execution.RepositoryRoot == "" {
		execution.RepositoryRoot = protocol.Deref(entry.MainRepo)
	}
	if execution.HostKind == "" {
		execution.HostKind = garden.HostLocal
	}
	execution.Resume = d.store.GetResumeSessionID(entry.ID)
	return execution
}

func (d *Daemon) reopenVerdict(sessionID string) (*sessionReopenVerdict, bool) {
	entry := d.store.SessionLedgerEntry(strings.TrimSpace(sessionID))
	if entry == nil {
		return nil, false
	}
	return d.reopenVerdictForEntry(entry), true
}

func (d *Daemon) reopenVerdictForEntry(entry *protocol.SessionLedgerEntry) *sessionReopenVerdict {
	verdict := &sessionReopenVerdict{
		SessionID: entry.ID,
		Entry:     entry,
		Execution: d.reopenExecutionFromLedger(entry),
		Live:      protocol.Deref(entry.ClosedAt) == "",
	}
	verdict.DirectoryState = inspectContinuationDirectory(verdict.Execution)
	d.planReopenProfile(verdict)

	if verdict.Live {
		verdict.Reason = fmt.Sprintf("session %s is running; focus it instead of reopening it", entry.ID)
		return verdict
	}
	if !decideReopenHost(verdict, d.endpointInfos()) {
		return verdict
	}
	_, hasLaunchIntent := d.store.LaunchIntent(entry.ID)
	d.decideReopenPlace(verdict, hasLaunchIntent)
	return verdict
}

func (d *Daemon) planReopenProfile(verdict *sessionReopenVerdict) {
	profileID, err := d.store.SessionProfileID(verdict.SessionID)
	if err != nil {
		d.logf("reopen: reading the profile of session %s: %v", verdict.SessionID, err)
	}
	verdict.ProfileID = profileID
	if profileID == "" {
		verdict.ProfileDeleted = true
		return
	}
	profile, err := d.store.GetProfile(profileID)
	verdict.ProfileDeleted = err != nil || profile.Deleted()
}

func (v *sessionReopenVerdict) destinationProfile(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	switch {
	case v.ProfileDeleted && requested == "":
		return "", fmt.Errorf("session %s belonged to profile %q, which is gone; name the profile to reopen it into (attn session reopen --profile <id>)", v.SessionID, v.ProfileID)
	case v.ProfileDeleted:
		return requested, nil
	case requested != "" && requested != v.ProfileID:
		return "", fmt.Errorf("session %s belongs to profile %s; it reopens there, and moves to %s only through a move", v.SessionID, v.ProfileID, requested)
	default:
		return v.ProfileID, nil
	}
}

func decideReopenHost(verdict *sessionReopenVerdict, endpoints []protocol.EndpointInfo) bool {
	if strings.TrimSpace(verdict.Execution.HostKind) != garden.HostRemote {
		return true
	}
	name, reachable := endpointReachable(endpoints, strings.TrimSpace(verdict.Execution.EndpointID))
	if reachable {
		verdict.Reason = fmt.Sprintf(
			"session %s ran on %s; its ledger row lives on that daemon, so reopen it there",
			verdict.SessionID, name)
		return false
	}
	verdict.Reason = fmt.Sprintf(
		"session %s ran on %s, which is not reachable now; retry when it is",
		verdict.SessionID, name)
	return false
}

func endpointReachable(endpoints []protocol.EndpointInfo, endpointID string) (string, bool) {
	name := endpointID
	if name == "" {
		name = "another host"
	}
	for _, endpoint := range endpoints {
		if endpoint.ID != endpointID {
			continue
		}
		if named := strings.TrimSpace(endpoint.Name); named != "" {
			name = named
		}
		return name, endpoint.Status == "connected"
	}
	return name, false
}

func (d *Daemon) endpointInfos() []protocol.EndpointInfo {
	if d.hubManager == nil {
		return nil
	}
	return d.hubManager.List()
}

func (d *Daemon) decideReopenPlace(verdict *sessionReopenVerdict, hasLaunchIntent bool) {
	conversation, conversationReason := d.reopenConversation(verdict.Execution)
	if !hasLaunchIntent {
		conversation = false
		conversationReason = fmt.Sprintf("session %s has no saved launch contract, so its exact agent configuration cannot be restored", verdict.SessionID)
	}

	switch verdict.DirectoryState {
	case directoryPresent:
		if !conversation {
			verdict.Reason = conversationReason
			verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshSamePlace}
			return
		}
		verdict.Reopenable = true
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionReopen}
		verdict.Warning = d.reopenBranchWarning(verdict.Execution)
		return
	case directoryMissing:
		d.decideMissingDirectory(verdict, conversation, conversationReason)
		return
	case directoryUnknown:
		verdict.Reason = fmt.Sprintf("session %s saved no directory to reopen in", verdict.SessionID)
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}
		return
	default:
		verdict.Reason = "its directory cannot be opened"
		return
	}
}

func (d *Daemon) decideMissingDirectory(verdict *sessionReopenVerdict, conversation bool, conversationReason string) {
	gone := "its directory no longer exists"
	repo := strings.TrimSpace(verdict.Execution.RepositoryRoot)
	branch := strings.TrimSpace(verdict.Execution.Branch)
	target, targetOK := savedWorktreeRoot(verdict.Execution)

	if repo == "" || branch == "" || !targetOK {
		verdict.Reason = gone + ", and it was not a worktree attn can put back"
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}
		return
	}
	verdict.RecreatePath = target

	inspection, known := d.branchInspection(verdict.SessionID, repo, branch)
	if !known {
		verdict.Checking = true
		verdict.BranchState = branchStateUnknown
		verdict.Reason = gone
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}
		return
	}
	if inspection.RepoMissing {
		verdict.Reason = gone + ", and its repository is gone too"
		return
	}
	verdict.BranchState = inspection.State

	if !conversation {
		verdict.Reason = conversationReason + ", and " + gone
		verdict.Actions = d.freshActionsForMissingDirectory(verdict, inspection)
		return
	}
	if inspection.AlreadyCheckedOut {
		verdict.Reason = fmt.Sprintf("%s, and branch %s is checked out somewhere else already", gone, branch)
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}
		return
	}

	switch inspection.State {
	case branchStateLocal:
		verdict.Reason = fmt.Sprintf("%s; branch %s is still here, so the worktree can be put back", gone, branch)
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionRecreateWorktreeAndReopen}
	case branchStateRemoteOnly:
		verdict.Reason = fmt.Sprintf("%s; branch %s is only on %s, so it has to be fetched first",
			gone, branch, inspection.Remote)
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionFetchRecreateAndReopen}
	default:
		verdict.BranchState, verdict.Reason = d.goneBranchVerdict(verdict, gone, branch)
		verdict.Actions = []protocol.SessionReopenAction{
			protocol.SessionReopenActionStartFreshDefaultBranch,
			protocol.SessionReopenActionStartFreshElsewhere,
		}
	}
}

func (d *Daemon) freshActionsForMissingDirectory(
	verdict *sessionReopenVerdict, inspection branchInspection,
) []protocol.SessionReopenAction {
	if inspection.State == branchStateGone && !inspection.AlreadyCheckedOut {
		verdict.BranchState, _ = d.goneBranchVerdict(verdict, "", strings.TrimSpace(verdict.Execution.Branch))
		return []protocol.SessionReopenAction{
			protocol.SessionReopenActionStartFreshDefaultBranch,
			protocol.SessionReopenActionStartFreshElsewhere,
		}
	}
	return []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}
}

func (d *Daemon) goneBranchVerdict(verdict *sessionReopenVerdict, gone, branch string) (string, string) {
	merged := d.branchMerged(verdict.SessionID, branch)
	state := branchStateGone
	tail := fmt.Sprintf("branch %s is gone from this repository and its remotes", branch)
	if merged {
		state = branchStateMerged
		tail = fmt.Sprintf("branch %s was merged and is gone from this repository and its remotes", branch)
	}
	if gone == "" {
		return state, tail
	}
	return state, gone + "; " + tail
}

func (d *Daemon) branchMerged(sessionID, branch string) bool {
	if branch == "" {
		return false
	}
	for _, record := range d.store.ListSessionPullRequests(sessionID) {
		if strings.TrimSpace(record.HeadBranch) == branch && strings.EqualFold(record.State, "merged") {
			return true
		}
	}
	return false
}

func (d *Daemon) reopenConversation(execution garden.Dispatch) (bool, string) {
	resumeID := strings.TrimSpace(execution.Resume)
	if resumeID == "" {
		return false, "no conversation id was saved for this session, so there is nothing to resume"
	}
	return d.conversationResumable(strings.TrimSpace(execution.Agent), resumeID)
}

func (d *Daemon) conversationResumable(agentName, resumeID string) (bool, string) {
	if plugin, ok := d.ensurePluginRegistry().driver(agentName); ok {
		if !plugin.Capabilities["resume"] {
			return false, fmt.Sprintf("agent %q does not resume conversations, so conversation %s cannot be picked up", agentName, resumeID)
		}
		return true, ""
	}
	driver := agentdriver.Get(agentName)
	if driver == nil {
		return false, fmt.Sprintf("agent %q is not installed here, so its conversation cannot be resumed", agentName)
	}
	if !agentdriver.ResumeAvailable(driver, resumeID) {
		return false, fmt.Sprintf("conversation %s is no longer in %s's storage", resumeID, agentName)
	}
	return true, ""
}

func (d *Daemon) reopenBranchWarning(execution garden.Dispatch) string {
	saved := strings.TrimSpace(execution.Branch)
	if saved == "" {
		return ""
	}
	info, err := attngit.GetBranchInfo(execution.Cwd)
	if err != nil || info == nil {
		return ""
	}
	if current := strings.TrimSpace(info.Branch); current != "" && current != saved {
		return fmt.Sprintf("it is on branch %s now; the session ran on %s", current, saved)
	}
	return ""
}

func (d *Daemon) branchInspection(sessionID, repo, branch string) (branchInspection, bool) {
	key := branchInspectionKey(repo, branch)
	d.branchInspectionsMu.Lock()
	inspection, known := d.branchInspections[key]
	d.branchInspectionsMu.Unlock()
	if !known {
		d.inspectBranchInBackground(sessionID, repo, branch)
	}
	return inspection, known
}

func branchInspectionKey(repo, branch string) string {
	return attngit.CanonicalizePath(repo) + "\x00" + strings.TrimSpace(branch)
}

func (d *Daemon) inspectBranchInBackground(sessionID, repo, branch string) <-chan struct{} {
	key := branchInspectionKey(repo, branch)
	d.branchInspectionsMu.Lock()
	if d.branchInspections == nil {
		d.branchInspections = make(map[string]branchInspection)
	}
	if d.branchInspectionsRunning == nil {
		d.branchInspectionsRunning = make(map[string]chan struct{})
	}
	if running := d.branchInspectionsRunning[key]; running != nil {
		d.branchInspectionsMu.Unlock()
		return running
	}
	done := make(chan struct{})
	d.branchInspectionsRunning[key] = done
	d.branchInspectionsMu.Unlock()

	go func() {
		defer func() {
			d.branchInspectionsMu.Lock()
			delete(d.branchInspectionsRunning, key)
			d.branchInspectionsMu.Unlock()
			close(done)
		}()
		finishOperation := d.beginGitOperation(protocol.GitOperationKindInspectBranch, repo, nil)
		var inspection branchInspection
		err := d.worktreeMaintenance.RunForeground(context.Background(), "inspect reopen branch", func(context.Context) error {
			var err error
			inspection, err = inspectBranch(repo, branch)
			return err
		})
		finishOperation(err)
		if err != nil {
			d.logf("reopen: inspecting branch %s in %s: %v", branch, repo, err)
			return
		}

		d.branchInspectionsMu.Lock()
		d.branchInspections[key] = inspection
		d.branchInspectionsMu.Unlock()

		if verdict, found := d.reopenVerdict(sessionID); found {
			d.publishFact(FactSessionReopenRefreshed, sessionID, verdict.toProtocol())
		}
	}()
	return done
}

func (d *Daemon) refreshReopenBranch(verdict *sessionReopenVerdict) {
	if verdict.BranchState == "" || verdict.BranchState == branchStateUnknown {
		return
	}
	d.inspectBranchInBackground(verdict.SessionID, verdict.Execution.RepositoryRoot, verdict.Execution.Branch)
}

func (d *Daemon) forgetBranchInspections(repo string) {
	prefix := attngit.CanonicalizePath(repo) + "\x00"
	d.branchInspectionsMu.Lock()
	defer d.branchInspectionsMu.Unlock()
	for key := range d.branchInspections {
		if strings.HasPrefix(key, prefix) {
			delete(d.branchInspections, key)
		}
	}
}

func inspectBranch(repo, branch string) (branchInspection, error) {
	if _, err := os.Stat(repo); err != nil {
		return branchInspection{State: branchStateGone, RepoMissing: true}, nil
	}
	inspection := branchInspection{State: branchStateGone}
	if attngit.RefExists(repo, branch) {
		inspection.State = branchStateLocal
	} else {
		remotes, err := attngit.ListRemotes(repo)
		if err != nil {
			return branchInspection{}, fmt.Errorf("read remotes of %s: %w", repo, err)
		}
		for _, remote := range remotes {
			if attngit.RefExists(repo, remote+"/"+branch) {
				inspection.State = branchStateRemoteOnly
				inspection.Remote = remote
				break
			}
		}
	}
	worktrees, err := attngit.ObserveWorktrees(repo)
	if err != nil {
		return branchInspection{}, fmt.Errorf("read worktrees of %s: %w", repo, err)
	}
	for _, worktree := range worktrees {
		if strings.TrimSpace(worktree.Branch) != branch {
			continue
		}
		if worktree.Prunable {
			inspection.StaleRegistration = true
			continue
		}
		inspection.AlreadyCheckedOut = true
	}
	return inspection, nil
}

type sessionReopenOutcome struct {
	SessionID       string
	ProfileID       string
	Directory       string
	Action          protocol.SessionReopenAction
	AlreadyRunning  bool
	WorktreeCreated string
}

func (d *Daemon) reopenSession(
	sessionID string, action protocol.SessionReopenAction, directory, profileID string,
) (*sessionReopenOutcome, error) {
	var outcome *sessionReopenOutcome
	err := d.worktreeMaintenance.RunForeground(context.Background(), "reopen session", func(context.Context) error {
		var err error
		outcome, err = d.reopenSessionForeground(sessionID, action, directory, profileID)
		return err
	})
	return outcome, err
}

func (d *Daemon) reopenSessionForeground(
	sessionID string, action protocol.SessionReopenAction, directory, profileID string,
) (*sessionReopenOutcome, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	verdict, found := d.reopenVerdict(sessionID)
	if !found {
		return nil, fmt.Errorf(
			"no ledger row for session %s on this daemon; a session that ran before the ledger, or on another "+
				"daemon, is not here. Its seed may still hand the work over from its saved dispatch", sessionID)
	}
	if verdict.Live {
		return &sessionReopenOutcome{
			SessionID:      sessionID,
			ProfileID:      verdict.ProfileID,
			Directory:      verdict.Entry.Directory,
			Action:         protocol.SessionReopenActionReopen,
			AlreadyRunning: true,
		}, nil
	}
	if action == "" {
		action = protocol.SessionReopenActionReopen
	}
	if verdict.Checking && !verdict.offers(action) {
		<-d.inspectBranchInBackground(sessionID, verdict.Execution.RepositoryRoot, verdict.Execution.Branch)
		verdict, found = d.reopenVerdict(sessionID)
		if !found {
			return nil, fmt.Errorf("session %s left the ledger while its branch was being checked", sessionID)
		}
	}
	if !verdict.offers(action) {
		return nil, reopenRefusal(verdict, action)
	}
	return d.performReopen(verdict, action, directory, profileID)
}

func reopenRefusal(verdict *sessionReopenVerdict, action protocol.SessionReopenAction) error {
	reason := strings.TrimSpace(verdict.Reason)
	if reason == "" {
		reason = "the saved execution does not allow it"
	}
	if len(verdict.Actions) == 0 {
		return fmt.Errorf("%s cannot be reopened: %s", verdict.SessionID, reason)
	}
	offered := make([]string, 0, len(verdict.Actions))
	for _, offer := range verdict.Actions {
		offered = append(offered, string(offer))
	}
	return fmt.Errorf("%s cannot be reopened with %s: %s. Offered instead: %s",
		verdict.SessionID, action, reason, strings.Join(offered, ", "))
}

func (d *Daemon) performReopen(
	verdict *sessionReopenVerdict, action protocol.SessionReopenAction, directory, requestedProfileID string,
) (*sessionReopenOutcome, error) {
	profileID, err := verdict.destinationProfile(requestedProfileID)
	if err != nil {
		return nil, err
	}
	if _, err := d.liveLaunchProfile(profileID); err != nil {
		return nil, fmt.Errorf("reopen %s: %w", verdict.SessionID, err)
	}
	plan := sessionReopenPlan{
		SessionID: verdict.SessionID,
		Directory: verdict.Execution.Cwd,
		Title:     verdict.Entry.Label,
		ProfileID: profileID,
	}
	rollback := d.newDelegationRollback()
	created := ""

	switch action {
	case protocol.SessionReopenActionReopen:
	case protocol.SessionReopenActionStartFreshSamePlace:
		plan.FreshConversation = true
	case protocol.SessionReopenActionStartFreshElsewhere:
		plan.FreshConversation = true
		if strings.TrimSpace(directory) == "" {
			return nil, fmt.Errorf("start_fresh_elsewhere needs a directory to start in; pass --cwd <path>")
		}
		chosen, err := validateDelegationDirectory(strings.TrimSpace(directory))
		if err != nil {
			return nil, fmt.Errorf("start_fresh_elsewhere needs a directory to start in: %w", err)
		}
		plan.Directory = chosen
	case protocol.SessionReopenActionRecreateWorktreeAndReopen,
		protocol.SessionReopenActionFetchRecreateAndReopen,
		protocol.SessionReopenActionStartFreshDefaultBranch:
		if action == protocol.SessionReopenActionStartFreshDefaultBranch {
			plan.FreshConversation = true
		}
		path, err := d.recreateReopenWorktree(verdict, action)
		if err != nil {
			return nil, err
		}
		created = path
		rollback.onWorktreeCreated(path)
		plan.Directory = reopenDirectoryInsideWorktree(path, verdict.Execution)
	default:
		return nil, fmt.Errorf("%q is not a reopen action", action)
	}

	outcome, err := d.reopenSessionRuntime(plan, rollback, nil)
	if err != nil {
		return nil, err
	}
	return &sessionReopenOutcome{
		SessionID:       outcome.SessionID,
		ProfileID:       outcome.ProfileID,
		Directory:       plan.Directory,
		Action:          action,
		WorktreeCreated: created,
	}, nil
}

func reopenDirectoryInsideWorktree(worktree string, execution garden.Dispatch) string {
	subdir := strings.TrimSpace(execution.RepositorySubdir)
	if subdir == "" || subdir == "." {
		return worktree
	}
	candidate := attngit.CanonicalizePath(worktree + string(os.PathSeparator) + subdir)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return worktree
}

func (d *Daemon) recreateReopenWorktree(
	verdict *sessionReopenVerdict, action protocol.SessionReopenAction,
) (string, error) {
	repo := strings.TrimSpace(verdict.Execution.RepositoryRoot)
	branch := strings.TrimSpace(verdict.Execution.Branch)
	path := verdict.RecreatePath
	defer d.forgetBranchInspections(repo)

	if inspection, known := d.branchInspection(verdict.SessionID, repo, branch); known && inspection.StaleRegistration {
		if err := attngit.PruneWorktrees(repo); err != nil {
			return "", fmt.Errorf("clear the stale worktree registration in %s: %w", repo, err)
		}
	}

	switch action {
	case protocol.SessionReopenActionRecreateWorktreeAndReopen:
		return d.doCreateWorktreeFromBranchForeground(&protocol.CreateWorktreeFromBranchMessage{
			Cmd: protocol.CmdCreateWorktreeFromBranch, MainRepo: repo, Branch: branch, Path: protocol.Ptr(path),
		})
	case protocol.SessionReopenActionFetchRecreateAndReopen:
		inspection, _ := d.branchInspection(verdict.SessionID, repo, branch)
		if inspection.Remote == "" {
			return "", fmt.Errorf("no remote carries branch %s any more", branch)
		}
		return d.doCreateWorktreeFromBranchForeground(&protocol.CreateWorktreeFromBranchMessage{
			Cmd:      protocol.CmdCreateWorktreeFromBranch,
			MainRepo: repo,
			Branch:   inspection.Remote + "/" + branch,
			Path:     protocol.Ptr(path),
		})
	default:
		base, err := attngit.GetDefaultBranch(repo)
		if err != nil || strings.TrimSpace(base) == "" {
			return "", fmt.Errorf("%s has no default branch to start from: %w", repo, err)
		}
		return d.doCreateWorktreeForeground(&protocol.CreateWorktreeMessage{
			Cmd:          protocol.CmdCreateWorktree,
			MainRepo:     repo,
			Branch:       branch,
			Path:         protocol.Ptr(path),
			StartingFrom: protocol.Ptr(base),
		})
	}
}

type sessionReopenPlan struct {
	SessionID         string
	Directory         string
	Title             string
	ProfileID         string
	InitialPrompt     string
	FreshConversation bool
}

type sessionRuntimeReopened struct {
	SessionID      string
	ProfileID      string
	AlreadyRunning bool
}

func (d *Daemon) reopenSessionRuntime(
	plan sessionReopenPlan,
	rollback *delegationRollback,
	afterSpawn func() error,
) (*sessionRuntimeReopened, error) {
	lifecycleLock := d.sessionLifecycleLockFor(plan.SessionID)
	lifecycleLock.Lock()
	defer lifecycleLock.Unlock()
	fail := func(cause error) (*sessionRuntimeReopened, error) {
		return nil, rollback.fail(cause)
	}

	entry := d.store.SessionLedgerEntry(plan.SessionID)
	if entry == nil {
		return fail(fmt.Errorf("session %s is not in the ledger", plan.SessionID))
	}
	priorSession := d.store.Get(plan.SessionID)
	if priorSession != nil && d.sessionHasLiveWorker(plan.SessionID) {
		if afterSpawn != nil {
			if err := afterSpawn(); err != nil {
				return fail(err)
			}
		}
		rollback.abandon()
		return &sessionRuntimeReopened{
			SessionID: plan.SessionID, ProfileID: priorSession.ProfileID, AlreadyRunning: true,
		}, nil
	}
	intent, ok := d.store.LaunchIntent(plan.SessionID)
	priorIntent, hadPriorIntent := intent, ok
	if !ok && !plan.FreshConversation {
		return fail(fmt.Errorf("session %s has no stored launch intent", plan.SessionID))
	}

	if strings.TrimSpace(plan.Directory) == "" {
		plan.Directory = entry.Directory
	}
	directory, err := validateDelegationDirectory(plan.Directory)
	if err != nil {
		return fail(err)
	}
	if strings.TrimSpace(entry.Agent) == "" {
		return fail(fmt.Errorf("session %s saved no agent to start", plan.SessionID))
	}
	if strings.TrimSpace(plan.Title) == "" {
		plan.Title = entry.Label
	}
	profileID := strings.TrimSpace(plan.ProfileID)
	if profileID == "" {
		if profileID, err = d.store.SessionProfileID(plan.SessionID); err != nil {
			return fail(err)
		}
	}
	if _, err := d.liveLaunchProfile(profileID); err != nil {
		return fail(fmt.Errorf("reopen %s: %w", plan.SessionID, err))
	}

	d.waitForSessionTeardown(plan.SessionID)
	d.store.ClearSessionIntentionalClose(plan.SessionID)

	lifted, reopened, err := d.store.ReopenSession(plan.SessionID, profileID)
	if err != nil {
		return fail(err)
	}
	if reopened {
		rollback.onSessionReopened(plan.SessionID, lifted)
	}

	if plan.FreshConversation {
		prior := d.store.GetSessionConversation(plan.SessionID)
		d.store.SetResumeSessionID(plan.SessionID, "")
		d.forgetDispatchResume(plan.SessionID)
		rollback.onConversationForgotten(plan.SessionID, prior)
	}

	session := &protocol.Session{
		ID: plan.SessionID, Label: plan.Title, Agent: protocol.SessionAgent(entry.Agent),
		Directory: directory, ProfileID: profileID,
	}
	spawn := &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: session.ID, Cwd: session.Directory,
		ProfileID: session.ProfileID, Agent: string(session.Agent), Cols: 80, Rows: 24,
		Label: protocol.Ptr(session.Label),
	}
	policy := internalSpawnPolicy{}
	if ok {
		spawn, policy = buildStoredIntentSpawn(session, intent, 80, 24)
	}
	if prompt := strings.TrimSpace(plan.InitialPrompt); prompt != "" {
		spawn.InitialPrompt = protocol.Ptr(prompt)
	}
	if resumeID := strings.TrimSpace(d.store.GetResumeSessionID(plan.SessionID)); !plan.FreshConversation && resumeID != "" {
		spawn.ResumeSessionID = protocol.Ptr(resumeID)
	}
	spawnClient := newInternalWSClient()
	d.handleSpawnSessionWithPolicyForeground(spawnClient, spawn, policy)
	if _, err := readInternalActionResult(spawnClient); err != nil {
		return fail(fmt.Errorf("spawn reopened session: %w", err))
	}
	if session := d.store.Get(plan.SessionID); session == nil {
		return fail(fmt.Errorf("reopened session was not persisted"))
	}
	if priorSession != nil {
		rollback.onSessionRespawned(priorSession, priorIntent, hadPriorIntent)
	} else if !reopened {
		rollback.onSessionSpawned(plan.SessionID)
	}

	if afterSpawn != nil {
		if err := afterSpawn(); err != nil {
			return fail(err)
		}
	}
	rollback.abandon()
	d.logf("reopen: session %s is back in %s, unplaced in profile %s", plan.SessionID, directory, profileID)
	return &sessionRuntimeReopened{SessionID: plan.SessionID, ProfileID: profileID}, nil
}

func (r *delegationRollback) onSessionReopened(sessionID string, closed store.SessionCloseRecord) {
	r.undo = append(r.undo, func() error {
		r.d.terminateSession(sessionID, syscall.SIGTERM)
		r.d.restoreSessionClose(sessionID, closed)
		return nil
	})
}

func (r *delegationRollback) onSessionRespawned(
	prior *protocol.Session,
	priorIntent store.LaunchIntent,
	hadPriorIntent bool,
) {
	r.undo = append(r.undo, func() error {
		if err := r.d.terminateSessionRuntimeChecked(prior.ID, syscall.SIGTERM); err != nil {
			return err
		}
		r.d.closePluginDriverSession(prior.ID, "launch_failed", nil, "")
		if err := r.d.store.AddCheckedUnlessTeardown(prior); err != nil {
			return err
		}
		if hadPriorIntent {
			r.d.store.SetLaunchIntent(prior.ID, priorIntent)
		} else {
			r.d.store.ClearLaunchIntent(prior.ID)
		}
		r.d.publishFact(FactSessionReregistered, prior.ID, nil)
		return nil
	})
}

func (r *delegationRollback) onConversationForgotten(sessionID string, prior store.SessionConversation) {
	r.undo = append(r.undo, func() error {
		if strings.TrimSpace(prior.NativeID) == "" {
			return nil
		}
		if strings.TrimSpace(prior.TranscriptPath) != "" {
			_, err := r.d.store.TransitionSessionConversation(sessionID, prior.NativeID, prior.TranscriptPath)
			return err
		}
		_, err := r.d.store.TransitionSessionResumeID(sessionID, prior.NativeID)
		return err
	})
}

func (d *Daemon) forgetDispatchResume(sessionID string) {
	if _, err := d.updateGardenDispatch(sessionID, func(current garden.Dispatch) (garden.Dispatch, bool, error) {
		if strings.TrimSpace(current.Resume) == "" {
			return current, false, nil
		}
		current.Resume = ""
		return current, true, nil
	}); err != nil {
		d.logf("reopen: forgetting the resume id of session %s: %v", sessionID, err)
	}
}

func (d *Daemon) handleSessionReopen(conn net.Conn, msg *protocol.SessionReopenMessage) {
	action := protocol.SessionReopenAction("")
	if msg.Action != nil {
		action = *msg.Action
	}
	outcome, err := d.reopenSession(msg.SessionID, action, protocol.Deref(msg.Directory), protocol.Deref(msg.ProfileID))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, SessionReopenResult: sessionReopenResult(outcome)})
}

func sessionReopenResult(outcome *sessionReopenOutcome) *protocol.SessionReopenResult {
	result := &protocol.SessionReopenResult{
		SessionID: outcome.SessionID,
		ProfileID: outcome.ProfileID,
		Directory: outcome.Directory,
		Action:    outcome.Action,
	}
	if outcome.AlreadyRunning {
		result.AlreadyRunning = protocol.Ptr(true)
	}
	if outcome.WorktreeCreated != "" {
		result.WorktreeCreated = protocol.Ptr(outcome.WorktreeCreated)
	}
	return result
}
