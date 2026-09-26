package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	delegationPlacementCurrent  = "current_workspace"
	delegationPlacementNew      = "new_workspace"
	delegationWorktreeOwnerFile = "attn-delegation-owner"
)

var delegationGitTask = gitTask{Kind: gitTaskDelegation, Lane: gitInteractive}

type internalActionResult struct {
	Event   string  `json:"event"`
	Success bool    `json:"success"`
	Error   *string `json:"error,omitempty"`
	PaneID  *string `json:"pane_id,omitempty"`
}

func newInternalWSClient() *wsClient {
	return &wsClient{send: make(chan outboundMessage, 4)}
}

func readInternalActionResult(client *wsClient) (internalActionResult, error) {
	select {
	case message := <-client.send:
		var result internalActionResult
		if err := json.Unmarshal(message.payload, &result); err != nil {
			return internalActionResult{}, err
		}
		if !result.Success {
			return result, fmt.Errorf("%s", protocol.Deref(result.Error))
		}
		return result, nil
	default:
		return internalActionResult{}, fmt.Errorf("daemon operation returned no result")
	}
}

func (d *Daemon) validateDelegationName(name string, creatingWorkspace bool, targetWorkspaceID string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a name is required; pass --name")
	}
	if name == "." || name == string(filepath.Separator) {
		return fmt.Errorf("%q is not a usable name; pass --name", name)
	}
	if len([]rune(name)) > maxSessionNameRunes {
		return fmt.Errorf("name %q is too long (max %d characters); pass a shorter --name", name, maxSessionNameRunes)
	}
	if creatingWorkspace {
		for _, ws := range d.store.ListWorkspaces() {
			if strings.EqualFold(strings.TrimSpace(ws.Title), name) {
				return fmt.Errorf("workspace name %q is already in use; pass a unique --name", name)
			}
		}
	}
	if targetWorkspaceID != "" {
		for _, sessionID := range d.store.SessionsInWorkspace(targetWorkspaceID) {
			existing := d.store.Get(sessionID)
			if existing != nil && strings.EqualFold(strings.TrimSpace(existing.Label), name) {
				return fmt.Errorf("session name %q is already used in this workspace; pass a unique --name", name)
			}
		}
	}
	return nil
}

func truncateDelegationName(name string) string {
	runes := []rune(name)
	if len(runes) <= maxSessionNameRunes {
		return name
	}
	return strings.TrimRight(string(runes[:maxSessionNameRunes]), "-_. \t")
}

func (d *Daemon) resolveDelegationAgent(sourceAgent string, requested *string) (string, error) {
	agent := strings.TrimSpace(strings.ToLower(protocol.Deref(requested)))
	if agent == "" {
		agent = strings.TrimSpace(strings.ToLower(sourceAgent))
	}
	if agent == "" || agent == protocol.AgentShellValue {
		agent = string(protocol.SessionAgentCodex)
	}
	if pluginDriver, ok := d.ensurePluginRegistry().driver(agent); ok {
		if !pluginDriver.Capabilities["initial_prompt"] {
			return "", fmt.Errorf("agent %q does not support initial prompts", agent)
		}
		return pluginDriver.Agent, nil
	}
	driver := agentdriver.Get(agent)
	if driver == nil {
		return "", fmt.Errorf("agent %q is not available", agent)
	}
	if !agentdriver.EffectiveCapabilities(driver).HasInitialPrompt {
		return "", fmt.Errorf("agent %q does not support initial prompts", agent)
	}
	return driver.Name(), nil
}

func (d *Daemon) validateDelegationModelEffort(agent, model, effort string) error {
	if model == "" && effort == "" {
		return nil
	}
	if pluginDriver, ok := d.ensurePluginRegistry().driver(agent); ok {
		if model != "" && !pluginDriver.Capabilities["model_pin"] {
			return fmt.Errorf("agent %q does not support --model", agent)
		}
		if effort != "" && !pluginDriver.Capabilities["effort_pin"] {
			return fmt.Errorf("agent %q does not support --effort", agent)
		}
		return nil
	}
	caps := agentdriver.EffectiveCapabilities(agentdriver.Get(agent))
	if model != "" && !caps.HasModelPin {
		return fmt.Errorf("agent %q does not support --model", agent)
	}
	if effort != "" && !caps.HasEffortPin {
		return fmt.Errorf("agent %q does not support --effort", agent)
	}
	return nil
}

func (d *Daemon) defaultDelegationEffort(agent, effort string) string {
	if effort != "" {
		return effort
	}
	if pluginDriver, ok := d.ensurePluginRegistry().driver(agent); ok {
		if pluginDriver.Capabilities["effort_pin"] {
			return "medium"
		}
		return ""
	}
	if agentdriver.EffectiveCapabilities(agentdriver.Get(agent)).HasEffortPin {
		return "medium"
	}
	return ""
}

func (d *Daemon) resolveDelegationRepository(path, flagName string) (string, error) {
	root, err := d.resolveMainRepo(context.Background(), delegationGitTask, path)
	if err != nil {
		return "", fmt.Errorf("%s %s is not in a Git repository", flagName, git.CanonicalizePath(path))
	}
	return root, nil
}

func (d *Daemon) validateDelegationRepositoryInputs(cwd string, request *protocol.DelegateWorktreeRequest) error {
	if request == nil || strings.TrimSpace(cwd) == "" || strings.TrimSpace(protocol.Deref(request.Repo)) == "" {
		return nil
	}
	cwdRepo, err := d.resolveDelegationRepository(cwd, "--cwd")
	if err != nil {
		return err
	}
	explicitRepo, err := d.resolveDelegationRepository(protocol.Deref(request.Repo), "--repo")
	if err != nil {
		return err
	}
	if git.CanonicalizePath(cwdRepo) == git.CanonicalizePath(explicitRepo) {
		return nil
	}
	return fmt.Errorf("repository placement conflict: --cwd resolves to %s, but --repo resolves to %s; remove --repo to branch from --cwd, or make both flags point to the same repository", cwdRepo, explicitRepo)
}

func delegationPlacement(msg *resolvedDelegationLaunch) string {
	placement := strings.TrimSpace(strings.ToLower(protocol.Deref(msg.Placement)))
	if placement != "" {
		return placement
	}
	if strings.TrimSpace(msg.Cwd) != "" {
		return delegationPlacementNew
	}
	return delegationPlacementCurrent
}

func validateDelegationDirectory(path string) (string, error) {
	path = git.CanonicalizePath(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("delegation directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("delegation directory is not a directory: %s", path)
	}
	return path, nil
}

func (d *Daemon) activeSessionInCheckout(directory string, excluded ...string) (string, []string) {
	worktreeRoot, err := d.readRepoRoot(context.Background(), delegationGitTask, directory)
	if err != nil {
		return "", nil
	}
	worktreeRoot = git.CanonicalizePath(worktreeRoot)
	skip := map[string]bool{}
	for _, id := range excluded {
		skip[strings.TrimSpace(id)] = true
	}
	var occupants []string
	for _, session := range d.store.List("") {
		if skip[session.ID] || !d.sessionHasLiveWorker(session.ID) {
			continue
		}
		sessionRoot, err := d.readRepoRoot(context.Background(), delegationGitTask, session.Directory)
		if err == nil && git.CanonicalizePath(sessionRoot) == worktreeRoot {
			occupants = append(occupants, session.ID)
		}
	}
	sort.Strings(occupants)
	return worktreeRoot, occupants
}

type delegationRollback struct {
	d    *Daemon
	undo []func(foregroundCleanupProtection) error
}

func (d *Daemon) newDelegationRollback() *delegationRollback {
	return &delegationRollback{d: d}
}

func (r *delegationRollback) fail(protection foregroundCleanupProtection, cause error) error {
	for i := len(r.undo) - 1; i >= 0; i-- {
		if err := r.undo[i](protection); err != nil {
			cause = fmt.Errorf("%w; %v", cause, err)
		}
	}
	r.undo = nil
	return cause
}

func (r *delegationRollback) abandon() {
	r.undo = nil
}

func (r *delegationRollback) onWorktreeCreated(path string) {
	r.undo = append(r.undo, func(protection foregroundCleanupProtection) error {
		if err := r.d.doDeleteWorktreeProtected(protection, path, nil, deleteWorktreeOptions{}); err != nil {
			return fmt.Errorf("rollback worktree %s: %v", path, err)
		}
		return nil
	})
}

func (r *delegationRollback) onWorkspaceCreated(workspaceID string) {
	r.undo = append(r.undo, func(foregroundCleanupProtection) error {
		r.d.unregisterWorkspaceIfEmpty(workspaceID)
		return nil
	})
}

func (r *delegationRollback) onPaneCreated(sessionID string) {
	r.undo = append(r.undo, func(foregroundCleanupProtection) error {
		r.d.removeWorkspaceLayoutPaneForSession(sessionID)
		return nil
	})
}

func (r *delegationRollback) onSessionSpawned(sessionID string) {
	r.undo = append(r.undo, func(foregroundCleanupProtection) error {
		r.d.unregisterSession(sessionID, syscall.SIGTERM)
		return nil
	})
}

func (d *Daemon) delegationWorktreeOwnerPath(worktreePath string) (string, error) {
	out, err := d.gitOutput(context.Background(), delegationGitTask, git.OpMetadata, worktreePath, "rev-parse", "--git-path", delegationWorktreeOwnerFile)
	if err != nil {
		return "", fmt.Errorf("resolve delegation worktree owner marker: %w", err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("resolve delegation worktree owner marker: git returned an empty path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(worktreePath, path)
	}
	return filepath.Clean(path), nil
}

func (d *Daemon) writeDelegationWorktreeOwner(worktreePath, token string) error {
	path, err := d.delegationWorktreeOwnerPath(worktreePath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("write delegation worktree owner marker: %w", err)
	}
	return nil
}

func (d *Daemon) verifyDelegationWorktreeOwner(worktreePath, token string) error {
	path, err := d.delegationWorktreeOwnerPath(worktreePath)
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read delegation worktree owner marker: %w", err)
	}
	if token == "" || strings.TrimSpace(string(contents)) != token {
		return fmt.Errorf("delegation worktree owner marker does not match")
	}
	return nil
}

func (d *Daemon) createDelegationWorktree(protection foregroundCleanupProtection, baseDirectory string, request *protocol.DelegateWorktreeRequest, operationID, ownedPath string, worktreeOwned bool, ownedToken string, _ bool) (string, bool, error) {
	branch := strings.TrimSpace(request.Branch)
	if branch == "" {
		return "", false, fmt.Errorf("worktree branch is required")
	}
	repo := strings.TrimSpace(protocol.Deref(request.Repo))
	if repo == "" {
		if baseDirectory == "" {
			return "", false, fmt.Errorf("cannot determine which repository the worktree belongs to; pass --repo")
		}
		repoRoot, err := d.resolveMainRepo(protection.Context(), delegationGitTask, baseDirectory)
		if err != nil {
			return "", false, fmt.Errorf("workspace directory is not in a git repository; pass --repo")
		}
		repo = repoRoot
	}
	expectedPath := strings.TrimSpace(protocol.Deref(request.Path))
	if expectedPath == "" {
		expectedPath = git.GenerateWorktreePath(repo, branch)
	}
	expectedPath = git.CanonicalizePath(expectedPath)
	if _, statErr := os.Stat(expectedPath); statErr == nil {
		if pathRepo, pathErr := d.resolveDelegationRepository(expectedPath, "--worktree-path"); pathErr == nil && git.CanonicalizePath(pathRepo) != git.CanonicalizePath(repo) {
			return "", false, fmt.Errorf("repository placement conflict: selected repository resolves to %s, but --worktree-path resolves to %s; choose a worktree path from the selected repository or correct --repo/--cwd", repo, pathRepo)
		}
		wt := d.discoverWorktree(protection, expectedPath)
		if wt == nil || strings.TrimSpace(wt.Branch) != branch {
			return "", false, fmt.Errorf("worktree path already exists and is not branch %q: %s", branch, expectedPath)
		}
		if worktreeOwned && git.CanonicalizePath(ownedPath) == expectedPath {
			if err := d.verifyDelegationWorktreeOwner(expectedPath, ownedToken); err != nil {
				return "", false, fmt.Errorf("worktree %s was created before delegation preparation was interrupted, but its current ownership cannot be proven (%v), so it was left untouched", expectedPath, err)
			}
			return expectedPath, true, nil
		}
		if operationID != "" && ownedPath != "" && git.CanonicalizePath(ownedPath) == expectedPath {
			return "", false, fmt.Errorf("worktree %s appeared while delegation preparation was interrupted; ownership cannot be proven, so it was left untouched", expectedPath)
		}
		return "", false, fmt.Errorf("worktree %s already exists; creation cannot be reinterpreted as checkout reuse", expectedPath)
	} else if !os.IsNotExist(statErr) {
		return "", false, fmt.Errorf("inspect delegated worktree path: %w", statErr)
	}
	if operationID != "" {
		if err := d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
			"preparing worktree "+expectedPath, "", "", expectedPath, nil, nil, time.Now()); err != nil {
			return "", false, fmt.Errorf("record delegated worktree preparation: %w", err)
		}
	}
	if d.delegationWorktreePrepareHook != nil {
		d.delegationWorktreePrepareHook(expectedPath)
	}
	startingFrom := request.StartingFrom
	if protocol.Deref(request.ExistingBranch) && strings.TrimSpace(protocol.Deref(startingFrom)) != "" {
		return "", false, fmt.Errorf("an existing branch does not accept a starting ref")
	}
	if operationID != "" && !protocol.Deref(request.ExistingBranch) && strings.TrimSpace(protocol.Deref(startingFrom)) == "" {
		return "", false, fmt.Errorf("new delegated worktree requires an explicit resolved base commit")
	}
	var (
		worktreePath string
		err          error
	)
	if protocol.Deref(request.ExistingBranch) {
		worktreePath, err = d.doCreateWorktreeFromBranchProtected(protection, &protocol.CreateWorktreeFromBranchMessage{
			Cmd: protocol.CmdCreateWorktreeFromBranch, MainRepo: repo, Branch: branch, Path: request.Path,
		})
	} else {
		worktreePath, err = d.doCreateWorktreeProtected(protection, &protocol.CreateWorktreeMessage{
			Cmd:          protocol.CmdCreateWorktree,
			MainRepo:     repo,
			Branch:       branch,
			Path:         request.Path,
			StartingFrom: startingFrom,
		})
	}
	if err != nil {
		return worktreePath, worktreePath != "", fmt.Errorf("create delegated worktree: %w", err)
	}
	if operationID != "" {
		ownerToken := uuid.NewString()
		if err := d.writeDelegationWorktreeOwner(worktreePath, ownerToken); err != nil {
			return worktreePath, true, err
		}
		if err := d.store.MarkDelegationWorktreeOwned(operationID, worktreePath, ownerToken, time.Now()); err != nil {
			return worktreePath, true, fmt.Errorf("record delegated worktree ownership: %w", err)
		}
	}
	return worktreePath, true, nil
}

func (d *Daemon) delegateResolved(msg *resolvedDelegationLaunch) (*protocol.DelegateResult, error) {
	var result *protocol.DelegateResult
	err := d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		var delegateErr error
		result, delegateErr = d.delegateResolvedProtected(protection, msg)
		return delegateErr
	})
	return result, err
}

func (d *Daemon) delegateResolvedProtected(protection foregroundCleanupProtection, msg *resolvedDelegationLaunch) (*protocol.DelegateResult, error) {
	resolved, err := d.resolveDelegationPreferences(msg.preferenceRequest())
	if err != nil {
		return nil, err
	}
	sessionID := uuid.NewString()
	operationID := ""
	if msg.Handover != nil {
		operationID = "legacy-" + sessionID
	}
	return d.delegateOperationProtected(protection, msg, operationID, sessionID, "", false, "", "", resolved)
}

func (d *Daemon) spawnDelegatedRuntimeProtected(protection foregroundCleanupProtection, msg *resolvedDelegationLaunch, sessionID, workspaceID, directory, name, agent, model, effort, seedID string, guidance string) error {
	initialPrompt := delegatedSeedPrompt(seedID)
	if guidance != "" {
		initialPrompt = prompts.DelegationOpeningWithGuidance(initialPrompt, guidance)
	}
	spawnMsg := &protocol.SpawnSessionMessage{
		Cmd:           protocol.CmdSpawnSession,
		ID:            sessionID,
		Cwd:           directory,
		WorkspaceID:   workspaceID,
		Agent:         agent,
		Cols:          80,
		Rows:          24,
		Label:         protocol.Ptr(name),
		YoloMode:      msg.YoloMode,
		InitialPrompt: protocol.Ptr(initialPrompt),
	}
	if model != "" {
		spawnMsg.Model = protocol.Ptr(model)
	}
	if effort != "" {
		spawnMsg.Effort = protocol.Ptr(effort)
	}
	spawnClient := newInternalWSClient()
	d.handleSpawnSessionWithPolicyProtected(protection, spawnClient, spawnMsg, internalSpawnPolicy{})
	_, err := readInternalActionResult(spawnClient)
	return err
}

func (d *Daemon) delegateOperationProtected(protection foregroundCleanupProtection, msg *resolvedDelegationLaunch, operationID, reservedSessionID, ownedWorktreePath string, worktreeOwned bool, worktreeToken, initiatingChiefSessionID string, resolved *delegationprefs.Resolved) (*protocol.DelegateResult, error) {
	guidance := ""
	if resolved != nil {
		if err := d.ensureDelegationWorkflowSkill(resolved); err != nil {
			return nil, err
		}
		copy := *msg
		msg = &copy
		s := resolved.Selection
		msg.Agent = &s.Harness
		msg.Model = &s.Model
		msg.Effort = &s.Effort
		if s.Provider != "" {
			joined := s.Provider + "/" + s.Model
			msg.Model = &joined
		}
		guidance = prompts.DelegationExecutionGuidance(resolved.RoleName, resolved.Instructions, resolved.StoppingPoint)
	}
	sessionID := reservedSessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	sourceSessionID := strings.TrimSpace(protocol.Deref(msg.SourceSessionID))
	handover, err := d.prepareSeedHandover(msg, operationID, sessionID)
	if err != nil {
		return nil, err
	}
	brief := strings.TrimSpace(protocol.Deref(msg.Brief))
	if brief == "" && handover == nil {
		return nil, fmt.Errorf("a brief is required")
	}
	var source *protocol.Session
	existingSession := d.store.Get(sessionID)
	if sourceSessionID != "" {
		source = d.store.Get(sourceSessionID)
		if source == nil && existingSession == nil {
			return nil, fmt.Errorf("source session %s was not found; omit --source-session for a standalone launch", sourceSessionID)
		}
	}
	if source == nil && existingSession == nil && msg.Agent == nil {
		return nil, fmt.Errorf("source inheritance is unavailable; choose an agent directly or through a configured role/fallback")
	}
	if source != nil {
		if endpointID := strings.TrimSpace(protocol.Deref(source.EndpointID)); endpointID != "" {
			return nil, fmt.Errorf("delegation from remote session %s on endpoint %s is not supported", sourceSessionID, endpointID)
		}
	}
	sourceAgent := ""
	if source != nil {
		sourceAgent = string(source.Agent)
	} else if existingSession != nil {
		sourceAgent = string(existingSession.Agent)
	}
	agent, err := d.resolveDelegationAgent(sourceAgent, msg.Agent)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(protocol.Deref(msg.Model))
	effort := strings.TrimSpace(strings.ToLower(protocol.Deref(msg.Effort)))
	if err := d.validateDelegationModelEffort(agent, model, effort); err != nil {
		return nil, err
	}
	if resolved == nil && msg.Effort == nil {
		effort = d.defaultDelegationEffort(agent, effort)
	}
	if handover == nil && msg.Assignment.Kind != protocol.DelegateAssignmentKindNew {
		seedID := strings.TrimSpace(protocol.Deref(msg.Plot))
		if err := d.validateDispatchCrown(seedID, sourceSessionID); err != nil {
			return nil, err
		}
		if msg.Assignment.Kind == protocol.DelegateAssignmentKindSeed {
			if seed, _, readErr := d.readSeed(seedID); readErr != nil {
				return nil, readErr
			} else if sourceSessionID != "" && strings.TrimSpace(seed.TenderSession) == sourceSessionID && d.sessionExists(sourceSessionID) {
				return nil, fmt.Errorf("seed %s is tended by the source session; use --handover to transfer it to another worker", seedID)
			}
		}
	}
	name := strings.TrimSpace(protocol.Deref(msg.Label))
	delegatedByChief := initiatingChiefSessionID != "" ||
		(operationID == "" && d.chiefOfStaffSessionID() == sourceSessionID)
	paneID := "pane-" + sessionID
	placement := delegationPlacement(msg)
	explicitLaunch := msg.Assignment.Kind != ""
	if explicitLaunch && source != nil && source.WorkspaceID != "" && d.store.GetWorkspace(source.WorkspaceID) != nil {
		placement = delegationPlacementCurrent
	}
	workspaceID := ""
	directory := ""
	createdWorktreePath := ""
	operationWorktreePath := ""
	rollback := d.newDelegationRollback()
	if existing := d.store.Get(sessionID); existing != nil {
		expectedWorkspaceID := ""
		switch placement {
		case delegationPlacementCurrent:
			if source == nil {
				return nil, fmt.Errorf("source session not found: %s", sourceSessionID)
			}
			expectedWorkspaceID = source.WorkspaceID
		case delegationPlacementNew:
			expectedWorkspaceID = "workspace-" + sessionID
		}
		if existing.WorkspaceID == "" && expectedWorkspaceID != "" {
			d.store.AssignSessionWorkspace(sessionID, expectedWorkspaceID)
			existing.WorkspaceID = expectedWorkspaceID
		}
		if name != "" && existing.Label != name {
			d.store.UpdateSessionLabel(sessionID, name)
			existing.Label = name
		}
		var watch *launchWatch
		seedID := strings.TrimSpace(protocol.Deref(msg.Plot))
		if msg.Handover != nil {
			seedID = strings.TrimSpace(msg.Handover.SeedID)
			if handover != nil && !handover.alreadyBound {
				observed := d.observeGardenDispatchExecution(sessionID, existing.Directory, agent)
				if _, err := d.bindSeedHandoverProtected(protection, msg, operationID, sessionID, existing.Directory, agent, observed, delegatedByChief); err != nil {
					return nil, fmt.Errorf("bind seed handover: %w", err)
				}
			}
		} else if bound, ok := d.gardenDispatchCrown(sessionID); ok {
			seedID = bound
		} else {
			if msg.Assignment.Kind == "" {
				observed := d.observeGardenDispatchExecution(sessionID, existing.Directory, agent)
				seedID, err = d.bindDelegationSeedProtected(protection, sessionID, sourceSessionID, brief, existing.Label, seedID, observed, delegatedByChief)
			} else {
				observed := d.observeGardenDispatchExecution(sessionID, existing.Directory, agent)
				seedID, err = d.bindDelegationAssignmentProtected(protection, operationID, sessionID, sourceSessionID, msg.ParentSeedID, brief, existing.Label, seedID, observed, delegatedByChief, msg.Assignment.Kind == protocol.DelegateAssignmentKindNew)
			}
			if err != nil {
				return nil, err
			}
		}
		if !d.sessionHasLiveWorker(sessionID) {
			if operationID != "" {
				_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
					"recovering delegated runtime", existing.WorkspaceID, "", existing.Directory, nil, nil, time.Now())
			}
			watch = d.watchLaunch(sessionID)
			if err := d.spawnDelegatedRuntimeProtected(protection, msg, sessionID, existing.WorkspaceID, existing.Directory, existing.Label, agent, model, effort, seedID, guidance); err != nil {
				d.forgetLaunchWatch(sessionID, watch)
				return nil, fmt.Errorf("recover delegated session runtime: %w", err)
			}
		}
		if operationID != "" {
			worktreePath := ""
			if msg.Worktree != nil {
				worktreePath = existing.Directory
			}
			_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
				"recovered delegated session", existing.WorkspaceID, "", worktreePath, nil, nil, time.Now())
		}
		result := d.completedDelegationResult(existing, placement, worktreeOwned)
		result.SeedID, result.Agent, result.Model, result.Effort = seedID, agent, model, effort
		if handover != nil && strings.TrimSpace(msg.PreviousTenderSession) != "" {
			result.PredecessorSessionID = protocol.Ptr(strings.TrimSpace(msg.PreviousTenderSession))
		}
		if resolved != nil {
			result.Role = protocol.Ptr(resolved.RoleName)
		}
		if watch != nil {
			if err := d.confirmDelegatedLaunch(operationID, sessionID, agent, watch, result); err != nil {
				return nil, err
			}
		}
		return result, nil
	}

	switch placement {
	case delegationPlacementCurrent:
		if source == nil {
			return nil, fmt.Errorf("source session not found: %s", sourceSessionID)
		}
		if !explicitLaunch && strings.TrimSpace(msg.Cwd) != "" {
			return nil, fmt.Errorf("current_workspace placement does not accept cwd")
		}
		workspaceID = strings.TrimSpace(source.WorkspaceID)
		if workspaceID == "" || d.store.GetWorkspace(workspaceID) == nil {
			return nil, fmt.Errorf("source session has no local workspace")
		}
		if explicitLaunch {
			directory = strings.TrimSpace(msg.Cwd)
		} else {
			directory = source.Directory
		}
	case delegationPlacementNew:
		directory = strings.TrimSpace(msg.Cwd)
		if directory != "" && msg.Worktree != nil {
			validatedCwd, cwdErr := validateDelegationDirectory(directory)
			if cwdErr != nil {
				return nil, cwdErr
			}
			directory = validatedCwd
			if repoErr := d.validateDelegationRepositoryInputs(directory, msg.Worktree); repoErr != nil {
				return nil, repoErr
			}
		}
		if directory == "" {
			if source != nil {
				directory = source.Directory
			}
		}
	default:
		return nil, fmt.Errorf("unsupported placement %q", placement)
	}

	if !explicitLaunch && msg.Worktree == nil && strings.TrimSpace(msg.Cwd) == "" {
		directory = source.Directory
	}

	creatingWorkspace := placement == delegationPlacementNew
	sessionNameWorkspaceID := ""
	if !creatingWorkspace {
		sessionNameWorkspaceID = workspaceID
	}
	if name != "" {
		if err := d.validateDelegationName(name, creatingWorkspace, sessionNameWorkspaceID); err != nil {
			return nil, err
		}
	}

	if msg.Worktree != nil {
		repositorySubdir := ""
		if explicitLaunch {
			sourceRoot, rootErr := d.readRepoRoot(protection.Context(), delegationGitTask, directory)
			if rootErr != nil {
				return nil, rootErr
			}
			var relErr error
			repositorySubdir, relErr = filepath.Rel(sourceRoot, directory)
			if relErr != nil || strings.HasPrefix(repositorySubdir, "..") {
				return nil, fmt.Errorf("resolve cwd subdirectory inside checkout: %v", relErr)
			}
		}
		worktreePath, created, createErr := d.createDelegationWorktree(protection, directory, msg.Worktree, operationID, ownedWorktreePath, worktreeOwned, worktreeToken, protocol.Deref(msg.AllowWorktreeReuse))
		if createErr != nil {
			if operationID != "" && strings.TrimSpace(worktreePath) != "" {
				actualPath := git.CanonicalizePath(worktreePath)
				if recordErr := d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
					"worktree creation failed after creating "+actualPath, "", "", actualPath, nil, nil, time.Now()); recordErr != nil {
					return nil, fmt.Errorf("%w; record actual worktree path %s: %v", createErr, actualPath, recordErr)
				}
			}
			return nil, createErr
		}
		worktreePath = git.CanonicalizePath(worktreePath)
		if requestedPath := strings.TrimSpace(protocol.Deref(msg.Worktree.Path)); requestedPath != "" && worktreePath != git.CanonicalizePath(requestedPath) {
			return nil, fmt.Errorf("worktree provider returned %s, expected requested path %s; checkout was left in place", worktreePath, requestedPath)
		}
		actualBranch, branchErr := gitValue(protection.Context(), d.gitExecution(), delegationGitTask, func(ctx context.Context, client *git.Client) (string, error) {
			return client.GetCurrentBranch(ctx, worktreePath)
		})
		if branchErr != nil || actualBranch != msg.Worktree.Branch {
			return nil, fmt.Errorf("worktree provider returned branch %q at %s, expected %q; checkout was left in place", actualBranch, worktreePath, msg.Worktree.Branch)
		}
		if created {
			createdWorktreePath = worktreePath
		}
		resolvedDirectory := worktreePath
		if repositorySubdir != "." && repositorySubdir != "" {
			resolvedDirectory = filepath.Join(worktreePath, repositorySubdir)
		}
		validatedDirectory, directoryErr := validateDelegationDirectory(resolvedDirectory)
		if directoryErr != nil {
			return nil, rollback.fail(protection, directoryErr)
		}
		directory = validatedDirectory
		operationWorktreePath = worktreePath
	}
	if placement == delegationPlacementNew {
		validatedDirectory, directoryErr := validateDelegationDirectory(directory)
		if directoryErr != nil {
			return nil, rollback.fail(protection, directoryErr)
		}
		directory = validatedDirectory
	}
	d.delegationCheckoutMu.Lock()
	checkoutLocked := true
	defer func() {
		if checkoutLocked {
			d.delegationCheckoutMu.Unlock()
		}
	}()
	predecessorID := ""
	if handover != nil {
		predecessorID = strings.TrimSpace(msg.PreviousTenderSession)
	}
	if worktreeRoot, occupants := d.activeSessionInCheckout(directory, predecessorID, sessionID); len(occupants) > 0 && !protocol.Deref(msg.AllowWorktreeReuse) {
		rollback.abandon()
		return nil, fmt.Errorf("checkout %s is used by active Attn session %s; pass --allow-worktree-reuse only when sharing it is intentional", worktreeRoot, strings.Join(occupants, ", "))
	}

	if name == "" {
		name = truncateDelegationName(filepath.Base(directory))
		if err := d.validateDelegationName(name, creatingWorkspace, sessionNameWorkspaceID); err != nil {
			return nil, rollback.fail(protection, err)
		}
	}
	seedID := strings.TrimSpace(protocol.Deref(msg.Plot))
	if handover != nil {
		seedID = strings.TrimSpace(msg.Handover.SeedID)
		if !handover.alreadyBound {
			observed := d.observeGardenDispatchExecution(sessionID, directory, agent)
			if _, err := d.bindSeedHandoverProtected(protection, msg, operationID, sessionID, directory, agent, observed, delegatedByChief); err != nil {
				return nil, fmt.Errorf("bind seed handover: %w", err)
			}
		}
	} else {
		if msg.Assignment.Kind == "" {
			observed := d.observeGardenDispatchExecution(sessionID, directory, agent)
			seedID, err = d.bindDelegationSeedProtected(protection, sessionID, sourceSessionID, brief, name, seedID, observed, delegatedByChief)
		} else {
			observed := d.observeGardenDispatchExecution(sessionID, directory, agent)
			seedID, err = d.bindDelegationAssignmentProtected(protection, operationID, sessionID, sourceSessionID, msg.ParentSeedID, brief, name, seedID, observed, delegatedByChief, msg.Assignment.Kind == protocol.DelegateAssignmentKindNew)
		}
		if err != nil {
			return nil, err
		}
	}

	if placement == delegationPlacementNew {
		workspaceID = "workspace-" + sessionID
		d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
			Cmd:       protocol.CmdRegisterWorkspace,
			ID:        workspaceID,
			Title:     name,
			Directory: directory,
		})
		if d.store.GetWorkspace(workspaceID) == nil {
			return nil, rollback.fail(protection, fmt.Errorf("create delegated workspace"))
		}
		rollback.onWorkspaceCreated(workspaceID)
	}
	if operationID != "" {
		if err := d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
			"assembling workspace and session", workspaceID, "", operationWorktreePath, nil, nil, time.Now()); err != nil {
			return nil, rollback.fail(protection, err)
		}
	}

	if existingWorkspaceID, _, found := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID); found {
		if existingWorkspaceID != workspaceID {
			return nil, rollback.fail(protection,
				fmt.Errorf("reserved delegated pane belongs to workspace %s, want %s", existingWorkspaceID, workspaceID))
		}
	} else {
		paneClient := newInternalWSClient()
		d.handleWorkspaceLayoutAddSessionPane(paneClient, &protocol.WorkspaceLayoutAddSessionPaneMessage{
			Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
			WorkspaceID: workspaceID,
			PaneID:      protocol.Ptr(paneID),
			SessionID:   sessionID,
			Title:       protocol.Ptr(name),
		})
		if _, err := readInternalActionResult(paneClient); err != nil {
			return nil, rollback.fail(protection, fmt.Errorf("create delegated pane: %w", err))
		}
	}
	rollback.onPaneCreated(sessionID)

	watch := d.watchLaunch(sessionID)
	if err := d.spawnDelegatedRuntimeProtected(protection, msg, sessionID, workspaceID, directory, name, agent, model, effort, seedID, guidance); err != nil {
		d.forgetLaunchWatch(sessionID, watch)
		return nil, rollback.fail(protection, fmt.Errorf("spawn delegated session: %w", err))
	}

	session := d.store.Get(sessionID)
	if session == nil {
		return nil, rollback.fail(protection, fmt.Errorf("delegated session was not persisted"))
	}
	rollback.onSessionSpawned(sessionID)
	d.delegationCheckoutMu.Unlock()
	checkoutLocked = false
	if d.delegationFinalizeHook != nil {
		if err := d.delegationFinalizeHook(); err != nil {
			return nil, rollback.fail(protection, err)
		}
	}
	if delegatedByChief {
		if _, errMsg := d.setWorkspaceMuted(workspaceID, false); errMsg != "" {
			return nil, rollback.fail(protection, fmt.Errorf("make delegated workspace visible: %s", errMsg))
		}
	}
	if operationID != "" {
		_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
			"delegated session bound", workspaceID, "", operationWorktreePath, nil, nil, time.Now())
	}
	result := &protocol.DelegateResult{
		SeedID:      seedID,
		SessionID:   session.ID,
		WorkspaceID: protocol.Ptr(workspaceID),
		Directory:   session.Directory,
		Checkout:    "reused",
		Agent:       agent,
		Model:       model,
		Effort:      effort,
	}
	if predecessorID != "" {
		result.PredecessorSessionID = protocol.Ptr(predecessorID)
	}
	if resolved != nil {
		result.Role = protocol.Ptr(resolved.RoleName)
	}
	if createdWorktreePath != "" {
		result.WorktreeCreated = protocol.Ptr(true)
		result.Checkout = "created"
	}
	if session.Branch != nil && strings.TrimSpace(*session.Branch) != "" {
		result.Branch = protocol.Ptr(strings.TrimSpace(*session.Branch))
	}
	rollback.abandon()
	if err := d.confirmDelegatedLaunch(operationID, sessionID, agent, watch, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (d *Daemon) confirmDelegatedLaunch(operationID, sessionID, agent string, watch *launchWatch, result *protocol.DelegateResult) error {
	if operationID != "" {
		_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
			fmt.Sprintf("waiting for %s's first turn", agent), "", "", "", nil, nil, time.Now())
	}
	outcome := d.awaitDelegatedLaunch(sessionID, watch)
	if outcome.exit != nil {
		seedID, _ := d.gardenDispatchCrown(sessionID)
		d.noteDelegatedExitOnSeed(seedID, agent, sessionID, outcome.exit)
		return delegationExitError(agent, sessionID, outcome.exit)
	}
	if outcome.unconfirmed != "" {
		result.FirstTurnUnconfirmed = protocol.Ptr(outcome.unconfirmed)
	}
	if !outcome.startedAt.IsZero() {
		result.FirstTurnAt = protocol.Ptr(outcome.startedAt.UTC().Format(time.RFC3339Nano))
	}
	return nil
}

func (d *Daemon) completedDelegationResult(session *protocol.Session, placement string, worktreeCreated bool) *protocol.DelegateResult {
	result := &protocol.DelegateResult{SessionID: session.ID, WorkspaceID: protocol.Ptr(session.WorkspaceID), Directory: session.Directory, Checkout: "reused", Agent: string(session.Agent)}
	if worktreeCreated {
		result.WorktreeCreated = protocol.Ptr(true)
		result.Checkout = "created"
	}
	if branch := strings.TrimSpace(protocol.Deref(session.Branch)); branch != "" {
		result.Branch = protocol.Ptr(branch)
	}
	return result
}

func delegatedSeedPrompt(seedID string) string {
	return prompts.RenderText("delegation", "brief", prompts.Values{"seed_id": seedID})
}

func (d *Daemon) handleDelegate(conn net.Conn, msg *protocol.DelegateMessage) {
	operation, err := d.startDelegation(msg)
	if err != nil {
		d.sendError(conn, "delegate: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:                  true,
		DelegationOperation: operation,
	})
}

func (d *Daemon) handleDelegateStatus(conn net.Conn, msg *protocol.DelegateStatusMessage) {
	operation, err := d.delegationOperation(msg.ID)
	if err != nil {
		d.sendError(conn, "delegate status: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, DelegationOperation: operation})
}

func (d *Daemon) handleDelegateWS(client *wsClient, msg *protocol.DelegateMessage) {
	operation, err := d.startDelegation(msg)
	if err == nil {
		for operation.State == protocol.DelegationOperationStateAccepted || operation.State == protocol.DelegationOperationStatePreparing {
			time.Sleep(100 * time.Millisecond)
			operation, err = d.delegationOperation(operation.OperationID)
			if err != nil {
				break
			}
		}
	}
	var result *protocol.DelegateResult
	if operation != nil {
		result = operation.Result
		if operation.State == protocol.DelegationOperationStateFailed && operation.Error != nil {
			err = fmt.Errorf("%s", protocol.Deref(operation.Error))
		}
	}
	response := protocol.DelegateResultMessage{
		Event:     protocol.EventDelegateResult,
		RequestID: protocol.Ptr(msg.RequestID),
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, response)
}
