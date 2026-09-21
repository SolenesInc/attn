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

	reopenPlaceReuse  = "reuse"
	reopenPlaceCreate = "create"
	reopenPlaceAdd    = "add"
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
	DirectoryState string
	BranchState    string
	WorkspaceID    string
	WorkspacePlan  string
	PanePlan       string
	RecreatePath   string
	Inspection     branchInspection
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
		WorkspaceID:    v.WorkspaceID,
		WorkspacePlan:  v.WorkspacePlan,
		PanePlan:       v.PanePlan,
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

func (d *Daemon) planReopenPlacement(verdict *sessionReopenVerdict) {
	workspaceID := strings.TrimSpace(verdict.Entry.WorkspaceID)
	if workspaceID != "" && d.store.GetWorkspace(workspaceID) != nil {
		verdict.WorkspaceID = workspaceID
		verdict.WorkspacePlan = reopenPlaceReuse
	} else {
		verdict.WorkspaceID = reopenWorkspaceID(verdict.SessionID)
		verdict.WorkspacePlan = reopenPlaceCreate
		if d.store.GetWorkspace(verdict.WorkspaceID) != nil {
			verdict.WorkspacePlan = reopenPlaceReuse
		}
	}
	verdict.PanePlan = reopenPlaceAdd
	if d.workspaceLayoutHasSessionPane(verdict.WorkspaceID, verdict.SessionID) {
		verdict.PanePlan = reopenPlaceReuse
	}
}

func reopenWorkspaceID(sessionID string) string {
	return "workspace-" + sessionID
}

func (d *Daemon) workspaceLayoutHasSessionPane(workspaceID, sessionID string) bool {
	holder, paneID, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID)
	return ok && paneID != "" && holder == workspaceID
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

func (d *Daemon) decideReopenPlace(
	ctx context.Context,
	verdict *sessionReopenVerdict,
	hasLaunchIntent bool,
	gitView reopenGit,
) error {
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
			return nil
		}
		verdict.Reopenable = true
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionReopen}
		warning, err := reopenBranchWarning(ctx, gitView, verdict.Execution)
		if err != nil {
			return err
		}
		verdict.Warning = warning
		return nil
	case directoryMissing:
		return d.decideMissingDirectory(ctx, verdict, conversation, conversationReason, gitView)
	case directoryUnknown:
		verdict.Reason = fmt.Sprintf("session %s saved no directory to reopen in", verdict.SessionID)
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}
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
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}
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
		verdict.Actions = d.freshActionsForMissingDirectory(verdict, inspection)
		return nil
	}
	if inspection.AlreadyCheckedOut {
		verdict.Reason = fmt.Sprintf("%s, and branch %s is checked out somewhere else already", gone, branch)
		verdict.Actions = []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshElsewhere}
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
		verdict.Actions = []protocol.SessionReopenAction{
			protocol.SessionReopenActionStartFreshDefaultBranch,
			protocol.SessionReopenActionStartFreshElsewhere,
		}
	}
	return nil
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

func reopenBranchWarning(ctx context.Context, gitView reopenGit, execution garden.Dispatch) (string, error) {
	saved := strings.TrimSpace(execution.Branch)
	if saved == "" {
		return "", nil
	}
	info, err := gitView.BranchInfo(ctx, execution.Cwd)
	if err != nil || info == nil {
		return "", nil
	}
	if current := strings.TrimSpace(info.Branch); current != "" && current != saved {
		return fmt.Sprintf("it is on branch %s now; the session ran on %s", current, saved), nil
	}
	return "", nil
}

type sessionReopenOutcome struct {
	SessionID       string
	WorkspaceID     string
	Directory       string
	Action          protocol.SessionReopenAction
	AlreadyRunning  bool
	WorktreeCreated string
}

func (d *Daemon) reopenSession(
	sessionID string, action protocol.SessionReopenAction, directory string,
) (*sessionReopenOutcome, error) {
	return d.reopenSessionForeground(sessionID, action, directory)
}

func (d *Daemon) reopenSessionForeground(
	sessionID string, action protocol.SessionReopenAction, directory string,
) (*sessionReopenOutcome, error) {
	sessionID = strings.TrimSpace(sessionID)
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
	resolver := sessionReopenResolver{daemon: d}
	key := reopenKey{SessionID: sessionID, ClosedAt: protocol.Deref(entry.ClosedAt)}
	var verdict sessionReopenVerdict
	var err error
	if key.ClosedAt == "" {
		verdict, err = resolver.ResolveEntry(
			context.Background(), *entry, d.scheduledReopenGit(gitInteractive),
		)
	} else {
		verdict, err = resolver.ResolveClosed(
			context.Background(), key, d.scheduledReopenGit(gitInteractive),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve reopen eligibility for session %s: %w", sessionID, err)
	}
	if verdict.Live {
		return &sessionReopenOutcome{
			SessionID:      sessionID,
			WorkspaceID:    entry.WorkspaceID,
			Directory:      entry.Directory,
			Action:         protocol.SessionReopenActionReopen,
			AlreadyRunning: true,
		}, nil
	}
	if action == "" {
		action = protocol.SessionReopenActionReopen
	}
	if !verdict.offers(action) {
		return nil, reopenRefusal(&verdict, action)
	}
	return d.performReopenLocked(key, &verdict, action, directory)
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

func (d *Daemon) performReopenLocked(
	key reopenKey,
	verdict *sessionReopenVerdict,
	action protocol.SessionReopenAction,
	directory string,
) (*sessionReopenOutcome, error) {
	plan := sessionReopenPlan{
		SessionID:   verdict.SessionID,
		Directory:   verdict.Execution.Cwd,
		Title:       verdict.Entry.Label,
		WorkspaceID: verdict.WorkspaceID,
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
		path, resolved, err := d.recreateReopenWorktree(key, action)
		if err != nil {
			return nil, err
		}
		verdict = resolved
		plan.Directory = verdict.Execution.Cwd
		plan.Title = verdict.Entry.Label
		plan.WorkspaceID = verdict.WorkspaceID
		created = path
		rollback.onWorktreeCreated(path)
		plan.Directory = reopenDirectoryInsideWorktree(path, verdict.Execution)
	default:
		return nil, fmt.Errorf("%q is not a reopen action", action)
	}

	outcome, err := d.reopenSessionRuntimeLocked(plan, rollback, nil)
	if err != nil {
		return nil, err
	}
	return &sessionReopenOutcome{
		SessionID:       outcome.SessionID,
		WorkspaceID:     outcome.WorkspaceID,
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
	key reopenKey,
	action protocol.SessionReopenAction,
) (string, *sessionReopenVerdict, error) {
	resolver := sessionReopenResolver{daemon: d}
	var authoritative *sessionReopenVerdict
	var createdPath, createdBranch string
	err := d.worktreeMaintenance.RunForeground(context.Background(), "recreate reopen worktree", func(protectedCtx context.Context) error {
		resolved, resolveErr := resolver.ResolveClosed(
			protectedCtx, key, d.scheduledReopenGit(gitInteractive),
		)
		if resolveErr != nil {
			return resolveErr
		}
		if !resolved.offers(action) {
			return reopenRefusal(&resolved, action)
		}
		authoritative = &resolved
		plan, planErr := d.reopenWorktreeProviderPlan(protectedCtx, &resolved, action)
		if planErr != nil {
			return planErr
		}
		if hookErr := d.dispatchWorktreeBeforeCreateHooks(plan.repository, plan.branch, plan.startingFrom, plan.path); hookErr != nil {
			return hookErr
		}
		providerPath, providerBranch, handled, providerErr := d.dispatchWorktreeCreateProvider(
			plan.repository, plan.branch, plan.startingFrom, plan.path,
		)
		if providerErr != nil {
			return providerErr
		}
		if handled {
			createdPath, createdBranch = providerPath, providerBranch
		} else {
			createdPath, createdBranch = plan.path, plan.branch
			mutationErr := d.gitExecution().Run(protectedCtx, gitTask{
				Kind: gitTaskWorktreeMutation, Lane: gitInteractive, Effect: gitWrite, Scope: plan.repository,
			}, func(ctx context.Context, client *attngit.Client) error {
				branch, err := mutateReopenWorktreeAdmitted(ctx, client, &resolved, action, plan.startingFrom)
				if err == nil {
					createdBranch = branch
				}
				return err
			})
			if mutationErr != nil {
				return mutationErr
			}
		}
		d.registerCreatedWorktree(plan.repository, createdPath, createdBranch)
		return d.dispatchWorktreeAfterCreateHooks(plan.repository, createdPath, createdBranch)
	})
	if err != nil {
		return createdPath, authoritative, err
	}
	return createdPath, authoritative, nil
}

type reopenWorktreePlan struct {
	repository   string
	branch       string
	startingFrom string
	path         string
}

func (d *Daemon) reopenWorktreeProviderPlan(
	ctx context.Context,
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
	case protocol.SessionReopenActionStartFreshDefaultBranch:
		base, err := gitValue(ctx, d.gitExecution(), gitTask{
			Kind: gitTaskReopen, Lane: gitInteractive, Effect: gitRead, Scope: plan.repository,
		}, func(runCtx context.Context, client *attngit.Client) (string, error) {
			return client.GetDefaultBranch(runCtx, plan.repository)
		})
		if err != nil || strings.TrimSpace(base) == "" {
			return reopenWorktreePlan{}, fmt.Errorf("%s has no default branch to start from: %w", plan.repository, err)
		}
		plan.startingFrom = base
	default:
		return reopenWorktreePlan{}, fmt.Errorf("%q does not recreate a worktree", action)
	}
	return plan, nil
}

func mutateReopenWorktreeAdmitted(
	ctx context.Context,
	client *attngit.Client,
	verdict *sessionReopenVerdict,
	action protocol.SessionReopenAction,
	startingFrom string,
) (string, error) {
	repository := strings.TrimSpace(verdict.Execution.RepositoryRoot)
	branch := strings.TrimSpace(verdict.Execution.Branch)
	path := verdict.RecreatePath
	if verdict.Inspection.StaleRegistration {
		if err := client.PruneWorktrees(ctx, repository); err != nil {
			return "", fmt.Errorf("clear the stale worktree registration in %s: %w", repository, err)
		}
	}
	switch action {
	case protocol.SessionReopenActionRecreateWorktreeAndReopen:
		return branch, client.CreateWorktreeFromBranch(ctx, repository, startingFrom, path)
	case protocol.SessionReopenActionFetchRecreateAndReopen:
		if startingFrom == "" {
			return "", fmt.Errorf("no remote carries branch %s any more", branch)
		}
		return client.CreateWorktreeFromRemoteBranch(ctx, repository, startingFrom, path)
	case protocol.SessionReopenActionStartFreshDefaultBranch:
		return branch, client.CreateWorktreeFromPoint(ctx, repository, branch, path, startingFrom)
	default:
		return "", fmt.Errorf("%q does not recreate a worktree", action)
	}
}

type sessionReopenPlan struct {
	SessionID         string
	Directory         string
	Title             string
	WorkspaceID       string
	InitialPrompt     string
	FreshConversation bool
}

type sessionRuntimeReopened struct {
	SessionID      string
	WorkspaceID    string
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
	return d.reopenSessionRuntimeLocked(plan, rollback, afterSpawn)
}

func (d *Daemon) reopenSessionRuntimeLocked(
	plan sessionReopenPlan,
	rollback *delegationRollback,
	afterSpawn func() error,
) (*sessionRuntimeReopened, error) {
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
			SessionID: plan.SessionID, WorkspaceID: entry.WorkspaceID, AlreadyRunning: true,
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
	workspaceID := strings.TrimSpace(plan.WorkspaceID)
	if workspaceID == "" {
		workspaceID = reopenWorkspaceID(plan.SessionID)
	}

	d.waitForSessionTeardown(plan.SessionID)
	d.store.ClearSessionIntentionalClose(plan.SessionID)

	lifted, reopened, err := d.store.ReopenSession(plan.SessionID)
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

	if d.store.GetWorkspace(workspaceID) == nil {
		d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
			Cmd:       protocol.CmdRegisterWorkspace,
			ID:        workspaceID,
			Title:     plan.Title,
			Directory: directory,
		})
		if d.store.GetWorkspace(workspaceID) == nil {
			return fail(fmt.Errorf("create reopen workspace"))
		}
		rollback.onWorkspaceCreated(workspaceID)
	}

	_, paneCreated, err := d.addWorkspaceSessionPane(&protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-" + plan.SessionID),
		SessionID:   plan.SessionID,
		Title:       protocol.Ptr(plan.Title),
	})
	if err != nil {
		return fail(fmt.Errorf("create reopen pane: %w", err))
	}
	if paneCreated {
		rollback.onPaneCreated(plan.SessionID)
	}

	session := &protocol.Session{
		ID: plan.SessionID, Label: plan.Title, Agent: protocol.SessionAgent(entry.Agent),
		Directory: directory, WorkspaceID: workspaceID,
	}
	spawn := &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: session.ID, Cwd: session.Directory,
		WorkspaceID: session.WorkspaceID, Agent: string(session.Agent), Cols: 80, Rows: 24,
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
	d.logf("reopen: session %s is back in %s (workspace %s)", plan.SessionID, directory, workspaceID)
	return &sessionRuntimeReopened{SessionID: plan.SessionID, WorkspaceID: workspaceID}, nil
}

func (r *delegationRollback) onSessionReopened(sessionID string, closed store.SessionCloseRecord) {
	r.undo = append(r.undo, func() error {
		r.d.terminateSession(sessionID, syscall.SIGTERM)
		r.d.restoreSessionClose(sessionID, closed)
		r.d.dissociateSessionFromWorkspace(sessionID)
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
		if r.d.workspaces != nil {
			if prior.WorkspaceID == "" {
				r.d.workspaces.dissociateSession(prior.ID)
			} else {
				r.d.workspaces.associateSession(prior.ID, prior.WorkspaceID, prior.Label)
			}
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
	outcome, err := d.reopenSession(msg.SessionID, action, protocol.Deref(msg.Directory))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, SessionReopenResult: sessionReopenResult(outcome)})
}

func sessionReopenResult(outcome *sessionReopenOutcome) *protocol.SessionReopenResult {
	result := &protocol.SessionReopenResult{
		SessionID:   outcome.SessionID,
		WorkspaceID: outcome.WorkspaceID,
		Directory:   outcome.Directory,
		Action:      outcome.Action,
	}
	if outcome.AlreadyRunning {
		result.AlreadyRunning = protocol.Ptr(true)
	}
	if outcome.WorktreeCreated != "" {
		result.WorktreeCreated = protocol.Ptr(outcome.WorktreeCreated)
	}
	return result
}
