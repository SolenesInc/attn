package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	branchStateLocal      = "local"
	branchStateRemoteOnly = "remote_only"
	branchStateGone       = "gone"
	branchStateMerged     = "merged"
)

type branchInspection struct {
	State             string
	Remote            string
	AlreadyCheckedOut bool
	RepoMissing       bool
	StaleRegistration bool
}

type sessionReopenVerdict struct {
	SessionID      protocol.SessionID
	Entry          *protocol.SessionLedgerEntry
	Execution      garden.Dispatch
	Resume         string
	Live           bool
	Reopenable     bool
	Reason         string
	Warning        string
	Actions        []protocol.SessionReopenAction
	DirectoryState string
	BranchState    string
	ProfileID      string
	ProfileDeleted bool
	RecreatePath   string
	Inspection     branchInspection
	Link           string
	BranchMerged   bool
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
	return execution
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

func (d *Daemon) decideReopenPlace(
	ctx context.Context,
	verdict *sessionReopenVerdict,
	hasLaunchIntent bool,
	gitView reopenGit,
) error {
	conversation, conversationReason := d.reopenConversation(verdict.Execution, verdict.Resume, verdict.DirectoryState == directoryMissing)
	if l := d.linkNamed(verdict.Link); !conversation && l != nil && l.setAsideAtClose(verdict.Resume) {
		conversation, conversationReason = true, ""
	}
	if !hasLaunchIntent {
		conversation = false
		conversationReason = fmt.Sprintf("session %s has no saved launch contract, so its exact agent configuration cannot be restored", verdict.SessionID)
	}

	switch verdict.DirectoryState {
	case directoryPresent:
		if !conversation {
			verdict.Reason = conversationReason
			return nil
		}
		verdict.Reopenable = true
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionReopen}
		return nil
	case directoryMissing:
		return d.decideMissingDirectory(ctx, verdict, conversation, conversationReason, gitView)
	case directoryUnknown:
		verdict.Reason = fmt.Sprintf("session %s saved no directory to reopen in", verdict.SessionID)
		return nil
	default:
		verdict.Reason = "its directory cannot be opened"
		return nil
	}
}

func (d *Daemon) decideMissingDirectory(
	ctx context.Context,
	verdict *sessionReopenVerdict,
	conversation bool,
	conversationReason string,
	gitView reopenGit,
) error {
	gone := "its directory no longer exists"
	repo := strings.TrimSpace(verdict.Execution.RepositoryRoot)
	branch := strings.TrimSpace(verdict.Execution.Branch)
	target, targetOK := savedWorktreeRoot(verdict.Execution)

	if repo == "" || branch == "" || !targetOK {
		verdict.Reason = gone + ", and it was not a worktree attn can put back"
		return nil
	}
	verdict.RecreatePath = target

	inspection, err := gitView.BranchAvailability(ctx, repo, branch)
	if err != nil {
		return fmt.Errorf("inspect branch %s in %s: %w", branch, repo, err)
	}
	verdict.Inspection = inspection
	if inspection.RepoMissing {
		verdict.Reason = gone + ", and its repository is gone too"
		return nil
	}
	verdict.BranchState = inspection.State

	if !conversation {
		verdict.Reason = conversationReason + ", and " + gone
		return nil
	}
	if inspection.AlreadyCheckedOut {
		verdict.Reason = fmt.Sprintf("%s, and branch %s is checked out somewhere else already", gone, branch)
		return nil
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
	}
	return nil
}

func (d *Daemon) goneBranchVerdict(verdict *sessionReopenVerdict, gone, branch string) (string, string) {
	merged := verdict.BranchMerged
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

func (d *Daemon) branchMerged(sessionID protocol.SessionID, branch string) bool {
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

func (d *Daemon) reopenConversation(execution garden.Dispatch, resumeID string, directoryMissing bool) (bool, string) {
	if resumeID == "" {
		return false, "no conversation id was saved for this session, so there is nothing to resume"
	}
	// A missing worktree can hide project storage settings; inspect after recreation.
	if directoryMissing {
		if driver, ok := d.ensurePluginRegistry().driver(strings.TrimSpace(execution.Agent)); ok && driver.Capabilities["resume"] && driver.Capabilities["resume_availability"] {
			return true, ""
		}
	}
	return d.conversationResumable(strings.TrimSpace(execution.Agent), resumeID, execution.Cwd)
}

func (d *Daemon) conversationResumable(agentName, resumeID, cwd string) (bool, string) {
	if plugin, ok := d.ensurePluginRegistry().driver(agentName); ok {
		if !plugin.Capabilities["resume"] {
			return false, fmt.Sprintf("agent %q does not resume conversations, so conversation %s cannot be picked up", agentName, resumeID)
		}
		return d.pluginConversationResumable(plugin, resumeID, cwd)
	}
	driver := agentdriver.Get(agentName)
	if driver == nil {
		return false, fmt.Sprintf("agent %q is not installed here, so its conversation cannot be resumed", agentName)
	}
	readiness, err := d.readConversationReadiness(context.Background(), protocol.ConversationReadinessMessage{Agent: agentName, Conversation: resumeID})
	if err != nil {
		return false, err.Error()
	}
	if !readiness.Known {
		reason := fmt.Sprintf("conversation %s is no longer in %s's storage", resumeID, agentName)
		if kept, ok := d.store.KeptConversation(agentName, resumeID); ok && !kept.DeletedAt.IsZero() {
			if kept.DeletedBy == "user" {
				reason += fmt.Sprintf("; you deleted attn's copy of conversation %s on %s", resumeID, kept.DeletedAt.UTC().Format("2006-01-02"))
			} else {
				reason += fmt.Sprintf("; attn deleted its copy of conversation %s on %s, %d days after no open seed pointed at it", resumeID, kept.DeletedAt.UTC().Format("2006-01-02"), int(conversationKeepGrace().Hours()/24))
			}
		}
		return false, reason
	}
	return true, ""
}

type sessionReopenOutcome struct {
	SessionID       protocol.SessionID
	ProfileID       string
	Directory       string
	Action          protocol.SessionReopenAction
	AlreadyRunning  bool
	WorktreeCreated string
	launch          launchCompletion
}

func (d *Daemon) reopenSession(
	sessionID protocol.SessionID, action protocol.SessionReopenAction,
) (*sessionReopenOutcome, error) {
	var outcome *sessionReopenOutcome
	err := d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		var reopenErr error
		outcome, reopenErr = d.reopenSessionProtected(protection, sessionID, action)
		return reopenErr
	})
	if outcome != nil {
		outcome.launch.finish()
	}
	return outcome, err
}

func (d *Daemon) reopenSessionProtected(
	protection foregroundCleanupProtection, sessionID protocol.SessionID, action protocol.SessionReopenAction,
) (*sessionReopenOutcome, error) {
	sessionID = protocol.TrimID(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	lifecycleLock := d.sessionLifecycleLockFor(sessionID)
	lifecycleLock.Lock()
	defer lifecycleLock.Unlock()

	entry := d.store.SessionLedgerEntry(sessionID)
	if entry == nil {
		return nil, fmt.Errorf(
			"no ledger row for session %s on this daemon; a session that ran before the ledger, or on another "+
				"daemon, is not here. Its seed may still hand the work over from its saved dispatch", sessionID)
	}
	verdict, err := d.resolveReopen(protection.Context(), *entry)
	if err != nil {
		return nil, fmt.Errorf("resolve reopen eligibility for session %s: %w", sessionID, err)
	}
	if verdict.Live {
		return &sessionReopenOutcome{
			SessionID:      sessionID,
			ProfileID:      verdict.ProfileID,
			Directory:      entry.Directory,
			Action:         protocol.SessionReopenActionReopen,
			AlreadyRunning: true,
		}, nil
	}
	if action == "" {
		action = protocol.SessionReopenActionReopen
	}
	if !verdict.offers(action) {
		return nil, &reopenRefusedError{verdict: &verdict, action: action}
	}
	return d.performReopenLocked(protection, &verdict, action)
}

type reopenRefusedError struct {
	verdict *sessionReopenVerdict
	action  protocol.SessionReopenAction
}

func (e *reopenRefusedError) Error() string {
	reason := strings.TrimSpace(e.verdict.Reason)
	if reason == "" {
		reason = "the saved execution does not allow it"
	}
	if len(e.verdict.Actions) == 0 {
		return fmt.Sprintf("%s cannot be reopened: %s", e.verdict.SessionID, reason)
	}
	offered := make([]string, 0, len(e.verdict.Actions))
	for _, offer := range e.verdict.Actions {
		offered = append(offered, string(offer))
	}
	return fmt.Sprintf("%s cannot be reopened with %s: %s. Offered instead: %s",
		e.verdict.SessionID, e.action, reason, strings.Join(offered, ", "))
}

func (d *Daemon) performReopenLocked(
	protection foregroundCleanupProtection,
	verdict *sessionReopenVerdict,
	action protocol.SessionReopenAction,
) (*sessionReopenOutcome, error) {
	profileID := verdict.ProfileID
	if _, err := d.liveLaunchProfile(profileID); err != nil {
		return nil, fmt.Errorf("reopen %s: %w", verdict.SessionID, err)
	}
	plan := sessionReopenPlan{
		SessionID: verdict.SessionID,
		Directory: verdict.Execution.Cwd,
		Title:     verdict.Entry.Label,
		ProfileID: profileID,
	}
	created := ""

	switch action {
	case protocol.SessionReopenActionReopen:
	case protocol.SessionReopenActionRecreateWorktreeAndReopen,
		protocol.SessionReopenActionFetchRecreateAndReopen:
		recreated, err := Call[protocol.ReopenWorktreeResult](protection.Context(), d.router(), protocol.ReopenWorktreeMessage{
			Cmd: protocol.CmdReopenWorktree, Host: protocol.Deref(verdict.Entry.Host), SessionID: verdict.SessionID,
			Cwd: verdict.Execution.Cwd, Agent: verdict.Execution.Agent, Conversation: verdict.Resume,
			Branch: verdict.Execution.Branch, Repository: verdict.Execution.RepositoryRoot, RepositorySubdir: verdict.Execution.RepositorySubdir,
			Action: action, RecreatePath: verdict.RecreatePath, Remote: verdict.Inspection.Remote, StaleRegistration: verdict.Inspection.StaleRegistration,
		})
		if err != nil {
			return nil, err
		}
		created, plan.Directory = recreated.Path, recreated.Cwd
	default:
		return nil, fmt.Errorf("%q is not a reopen action", action)
	}

	outcome, err := d.reopenSessionRuntimeProtected(protection, plan)
	if err != nil {
		return nil, err
	}
	return &sessionReopenOutcome{
		SessionID:       outcome.SessionID,
		ProfileID:       outcome.ProfileID,
		Directory:       plan.Directory,
		Action:          action,
		AlreadyRunning:  outcome.AlreadyRunning,
		WorktreeCreated: created,
		launch:          outcome.launch,
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

func (d *Daemon) recreateReopenWorktreeProtected(
	protection foregroundCleanupProtection,
	verdict *sessionReopenVerdict,
	action protocol.SessionReopenAction,
) (string, error) {
	plan, err := d.reopenWorktreeProviderPlan(verdict, action)
	if err != nil {
		return "", err
	}
	d.store.MonitorWorktreeRepository(plan.repository)
	if err := d.dispatchWorktreeBeforeCreateHooks(plan.repository, plan.branch, plan.startingFrom, plan.path); err != nil {
		return "", err
	}
	createdPath, createdBranch, handled, err := d.dispatchWorktreeCreateProvider(
		plan.repository, plan.branch, plan.startingFrom, plan.path,
	)
	if err != nil {
		return "", err
	}
	if !handled {
		createdPath = plan.path
		mutationErr := d.gitExecution().Run(protection.Context(), gitTask{
			Kind: gitTaskWorktreeMutation, Lane: gitInteractive,
		}, func(ctx context.Context, client *attngit.Client) error {
			var createErr error
			createdBranch, createErr = mutateReopenWorktreeAdmitted(
				ctx, client, plan, action, verdict.Inspection.StaleRegistration,
			)
			return createErr
		})
		if mutationErr != nil {
			return createdPath, mutationErr
		}
	}
	d.registerCreatedWorktree(protection, plan.repository, createdPath, createdBranch)
	return createdPath, d.dispatchWorktreeAfterCreateHooks(plan.repository, createdPath, createdBranch)
}

type reopenWorktreePlan struct {
	repository   string
	branch       string
	startingFrom string
	path         string
}

func (d *Daemon) reopenWorktreeProviderPlan(
	verdict *sessionReopenVerdict,
	action protocol.SessionReopenAction,
) (reopenWorktreePlan, error) {
	plan := reopenWorktreePlan{
		repository: strings.TrimSpace(verdict.Execution.RepositoryRoot),
		branch:     strings.TrimSpace(verdict.Execution.Branch),
		path:       verdict.RecreatePath,
	}
	switch action {
	case protocol.SessionReopenActionRecreateWorktreeAndReopen:
		plan.startingFrom = plan.branch
	case protocol.SessionReopenActionFetchRecreateAndReopen:
		if verdict.Inspection.Remote == "" {
			return reopenWorktreePlan{}, fmt.Errorf("no remote carries branch %s any more", plan.branch)
		}
		plan.startingFrom = verdict.Inspection.Remote + "/" + plan.branch
	default:
		return reopenWorktreePlan{}, fmt.Errorf("%q does not recreate a worktree", action)
	}
	return plan, nil
}

func mutateReopenWorktreeAdmitted(
	ctx context.Context,
	client *attngit.Client,
	plan reopenWorktreePlan,
	action protocol.SessionReopenAction,
	staleRegistration bool,
) (string, error) {
	if staleRegistration {
		if err := client.PruneWorktrees(ctx, plan.repository); err != nil {
			return "", fmt.Errorf("clear the stale worktree registration in %s: %w", plan.repository, err)
		}
	}
	switch action {
	case protocol.SessionReopenActionRecreateWorktreeAndReopen:
		return plan.branch, client.CreateWorktreeFromBranch(ctx, plan.repository, plan.startingFrom, plan.path)
	case protocol.SessionReopenActionFetchRecreateAndReopen:
		return client.CreateWorktreeFromRemoteBranch(ctx, plan.repository, plan.startingFrom, plan.path)
	default:
		return "", fmt.Errorf("%q does not recreate a worktree", action)
	}
}

type sessionReopenPlan struct {
	SessionID protocol.SessionID
	Directory string
	Title     string
	ProfileID string
}

type sessionRuntimeReopened struct {
	SessionID      protocol.SessionID
	ProfileID      string
	AlreadyRunning bool
	launch         launchCompletion
}

func (d *Daemon) reopenSessionRuntime(
	plan sessionReopenPlan,
) (*sessionRuntimeReopened, error) {
	var outcome *sessionRuntimeReopened
	err := d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		var reopenErr error
		outcome, reopenErr = d.reopenSessionRuntimeWithProtection(protection, plan)
		return reopenErr
	})
	return outcome, err
}

func (d *Daemon) reopenSessionRuntimeWithProtection(
	protection foregroundCleanupProtection,
	plan sessionReopenPlan,
) (*sessionRuntimeReopened, error) {
	lifecycleLock := d.sessionLifecycleLockFor(plan.SessionID)
	lifecycleLock.Lock()
	outcome, err := d.reopenSessionRuntimeProtected(protection, plan)
	lifecycleLock.Unlock()
	if outcome != nil {
		outcome.launch.finish()
	}
	return outcome, err
}

func (d *Daemon) reopenSessionRuntimeProtected(
	protection foregroundCleanupProtection,
	plan sessionReopenPlan,
) (*sessionRuntimeReopened, error) {
	entry := d.store.SessionLedgerEntry(plan.SessionID)
	if entry == nil {
		return nil, fmt.Errorf("session %s is not in the ledger", plan.SessionID)
	}
	priorSession := d.store.Get(plan.SessionID)
	if priorSession != nil && d.sessionHasLiveWorker(plan.SessionID) {
		return &sessionRuntimeReopened{
			SessionID: plan.SessionID, ProfileID: priorSession.ProfileID, AlreadyRunning: true,
		}, nil
	}
	intent, ok := d.store.LaunchIntent(plan.SessionID)
	if !ok {
		return nil, fmt.Errorf("session %s has no stored launch intent", plan.SessionID)
	}

	if strings.TrimSpace(plan.Directory) == "" {
		plan.Directory = entry.Directory
	}
	checkout, err := d.delegationCheckout(protocol.Deref(entry.Host), plan.Directory, nil)
	directory := checkout.Cwd
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(entry.Agent) == "" {
		return nil, fmt.Errorf("session %s saved no agent to start", plan.SessionID)
	}
	if strings.TrimSpace(plan.Title) == "" {
		plan.Title = entry.Label
	}
	profileID := strings.TrimSpace(plan.ProfileID)
	if profileID == "" {
		if profileID, err = d.store.SessionProfileID(plan.SessionID); err != nil {
			return nil, err
		}
	}
	if _, err := d.liveLaunchProfile(profileID); err != nil {
		return nil, fmt.Errorf("reopen %s: %w", plan.SessionID, err)
	}

	d.waitForSessionTeardown(plan.SessionID)
	d.store.ClearSessionIntentionalClose(plan.SessionID)

	if err := d.store.ReopenSession(plan.SessionID); err != nil {
		return nil, err
	}

	session := d.store.Get(plan.SessionID)
	session.Directory = directory
	session.Label = plan.Title
	if err := d.store.AddCheckedUnlessTeardown(session); err != nil {
		return nil, err
	}
	if _, placed, err := d.store.SessionPlacement(session.ID); err != nil {
		return nil, err
	} else if !placed {
		if placed := d.placeLaunchedSession(session, &launchPlacement{reopen: true}); placed.err != nil {
			return nil, placed.err
		}
	}
	spawn, policy := buildStoredIntentSpawn(session, intent, 80, 24)
	policy.fromLedger = true
	if resumeID := strings.TrimSpace(d.store.GetResumeSessionID(plan.SessionID)); resumeID != "" {
		spawn.ResumeSessionID = protocol.Ptr(resumeID)
	}
	placed, rejection := d.runSpawnPipelineProtected(protection, spawn, policy)
	if rejection != nil {
		return nil, fmt.Errorf("spawn reopened session: %w", rejection.reason())
	}
	if session := d.store.Get(plan.SessionID); session == nil {
		return nil, fmt.Errorf("reopened session was not persisted")
	}
	d.logf("reopen: session %s is back in %s in profile %s", plan.SessionID, directory, profileID)
	return &sessionRuntimeReopened{SessionID: plan.SessionID, ProfileID: profileID, launch: placed.launch}, nil
}

func (d *Daemon) handleSessionReopen(conn net.Conn, msg *protocol.SessionReopenMessage) {
	action := protocol.SessionReopenAction("")
	if msg.Action != nil {
		action = *msg.Action
	}
	outcome, err := d.reopenSession(msg.SessionID, action)
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
