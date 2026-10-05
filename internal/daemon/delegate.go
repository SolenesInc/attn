package daemon

import (
	"cmp"
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
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	delegationWorktreeOwnerFile = "attn-delegation-owner"
)

var delegationGitTask = gitTask{Kind: gitTaskDelegation, Lane: gitInteractive}

type internalActionResult struct {
	Event     string  `json:"event"`
	Success   bool    `json:"success"`
	Error     *string `json:"error,omitempty"`
	DesktopID *string `json:"desktop_id,omitempty"`
	PaneID    *string `json:"pane_id,omitempty"`
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

func (d *Daemon) validateDelegationName(name string, placement *launchPlacement) error {
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

	taken, err := d.desktopLabelTaken(name, placement)
	if err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("session name %q is already used on this desktop; pass a unique --name", name)
	}
	return nil
}

func (d *Daemon) desktopLabelTaken(name string, placement *launchPlacement) (bool, error) {
	if placement == nil {
		return false, nil
	}
	desktop, err := d.store.GetDesktop(placement.desktopID)
	if err != nil {
		return false, fmt.Errorf("read desktop %s to check the name %q: %w", placement.desktopID, name, err)
	}
	for _, pane := range desktop.Panes {
		existing := d.store.Get(pane.SessionID)
		if existing != nil && strings.EqualFold(strings.TrimSpace(existing.Label), name) {
			return true, nil
		}
	}
	return false, nil
}

func (d *Daemon) derivedDelegationName(seedTitle, directory string, placement *launchPlacement) (string, error) {
	base := cmp.Or(seedTitle, filepath.Base(directory))
	name := truncateDelegationName(base)
	for n := 2; ; n++ {
		taken, err := d.desktopLabelTaken(name, placement)
		if err != nil {
			return "", err
		}
		if !taken {
			return name, d.validateDelegationName(name, placement)
		}
		suffix := fmt.Sprintf(" (%d)", n)
		runes := []rune(base)
		limit := maxSessionNameRunes - len(suffix)
		name = base
		if len(runes) > limit {
			name = strings.TrimRight(string(runes[:limit]), "-_. \t")
		}
		name += suffix
	}
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

func runsInSourceCheckout(msg *resolvedDelegationLaunch, source *protocol.Session) bool {
	return source != nil && (msg.Assignment.Kind != "" || strings.TrimSpace(msg.Cwd) == "")
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
			return "", false, fmt.Errorf("working folder %s is not in a git repository; pass --repo", baseDirectory)
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
		crashAt(crashAfterWorktreeJournaled)
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
		crashAt(crashAfterWorktreeOwned)
	}
	return worktreePath, true, nil
}

func (d *Daemon) spawnDelegatedRuntimeProtected(protection foregroundCleanupProtection, msg *resolvedDelegationLaunch, sessionID, profileID string, placement *launchPlacement, directory, name, agent, model, effort, seedID, seedTitle string, guidance string) (internalActionResult, error) {
	initialPrompt := delegatedSeedPrompt(seedID, seedTitle)
	if guidance != "" {
		initialPrompt = prompts.DelegationOpeningWithGuidance(initialPrompt, guidance)
	}
	spawnMsg := &protocol.SpawnSessionMessage{
		Cmd:           protocol.CmdSpawnSession,
		ID:            sessionID,
		Cwd:           directory,
		ProfileID:     profileID,
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
	if placement != nil {
		spawnMsg.Placement = &protocol.SessionPlacement{DesktopID: protocol.Ptr(placement.desktopID), AnchorPaneID: protocol.Ptr(placement.anchorPaneID)}
	}
	spawnClient := newInternalWSClient()
	d.handleSpawnSessionWithPolicyProtected(protection, spawnClient, spawnMsg, internalSpawnPolicy{})
	return readInternalActionResult(spawnClient)
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
		source = d.gardenSession(sourceSessionID)
		if source == nil && existingSession == nil {
			return nil, fmt.Errorf("source session %s was not found; omit --source-session for a standalone launch", sourceSessionID)
		}
	}
	if pinned := protocol.Deref(msg.ProfileID); pinned != "" && source != nil && source.ProfileID != pinned {
		owner, err := d.store.GetProfile(pinned)
		if err != nil {
			return nil, err
		}
		actual, err := d.store.GetProfile(source.ProfileID)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("delegation was accepted in profile %q; source session %s belongs to profile %q now", owner.Name, source.ID, actual.Name)
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
		(operationID == "" && d.isChiefOfStaffSession(sourceSessionID))
	createdWorktreePath := ""
	operationWorktreePath := ""
	rollback := d.newDelegationRollback()
	if existing := d.store.Get(sessionID); existing != nil {
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
			observed := d.observeGardenDispatchExecution(sessionID, existing.Directory, agent)
			seedID, err = d.bindDelegationAssignmentProtected(protection, operationID, sessionID, sourceSessionID, msg.ParentSeedID, brief, cmp.Or(msg.SeedTitle, existing.Label), seedID, observed, delegatedByChief, msg.Assignment.Kind == protocol.DelegateAssignmentKindNew, existing.ProfileID)
			if err != nil {
				return nil, err
			}
		}
		if recovered := d.takeRecoveredLaunch(sessionID); recovered != nil && (recovered.settled() || d.sessionHasLiveWorker(sessionID)) {
			watch = recovered
		} else if !d.sessionHasLiveWorker(sessionID) {
			if operationID != "" {
				_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
					"recovering delegated runtime", existing.ProfileID, "", existing.Directory, nil, nil, time.Now())
			}
			watch = d.watchLaunch(sessionID)
			if _, err := d.spawnDelegatedRuntimeProtected(protection, msg, sessionID, existing.ProfileID, nil, existing.Directory, existing.Label, agent, model, effort, seedID, cmp.Or(msg.SeedTitle, existing.Label), guidance); err != nil {
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
				"recovered delegated session", existing.ProfileID, "", worktreePath, nil, nil, time.Now())
		}
		result := d.completedDelegationResult(existing, worktreeOwned)
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

	profile, placement, err := d.delegationDestination(source, protocol.Deref(msg.Desktop), protocol.Deref(msg.ProfileID))
	if err != nil {
		return nil, err
	}
	inSourceCheckout := runsInSourceCheckout(msg, source)
	directory := strings.TrimSpace(msg.Cwd)
	if !inSourceCheckout && directory != "" && msg.Worktree != nil {
		validatedCwd, cwdErr := validateDelegationDirectory(directory)
		if cwdErr != nil {
			return nil, cwdErr
		}
		directory = validatedCwd
		if repoErr := d.validateDelegationRepositoryInputs(directory, msg.Worktree); repoErr != nil {
			return nil, repoErr
		}
	}
	if directory == "" && source != nil {
		directory = source.Directory
	}

	if name != "" {
		if err := d.validateDelegationName(name, placement); err != nil {
			return nil, err
		}
	}

	if msg.Worktree != nil {
		repositorySubdir := ""
		if msg.Assignment.Kind != "" {
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
	if !inSourceCheckout {
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
		name, err = d.derivedDelegationName(msg.SeedTitle, directory, placement)
		if err != nil {
			return nil, rollback.fail(protection, err)
		}
	}
	seedTitle := cmp.Or(msg.SeedTitle, name)
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
		observed := d.observeGardenDispatchExecution(sessionID, directory, agent)
		seedID, err = d.bindDelegationAssignmentProtected(protection, operationID, sessionID, sourceSessionID, msg.ParentSeedID, brief, seedTitle, seedID, observed, delegatedByChief, msg.Assignment.Kind == protocol.DelegateAssignmentKindNew, profile.ID)
		if err != nil {
			return nil, err
		}
	}

	if operationID != "" {
		if err := d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
			"assembling the session", profile.ID, "", operationWorktreePath, nil, nil, time.Now()); err != nil {
			return nil, rollback.fail(protection, err)
		}
	}

	watch := d.watchLaunch(sessionID)
	spawned, err := d.spawnDelegatedRuntimeProtected(protection, msg, sessionID, profile.ID, placement, directory, name, agent, model, effort, seedID, seedTitle, guidance)
	if err != nil {
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
	if operationID != "" {
		_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
			"delegated session bound", profile.ID, "", operationWorktreePath, nil, nil, time.Now())
	}
	result := &protocol.DelegateResult{
		SeedID:    seedID,
		SessionID: session.ID,
		ProfileID: protocol.Ptr(profile.ID),
		Directory: session.Directory,
		Checkout:  "reused",
		Agent:     agent,
		Model:     model,
		Effort:    effort,
		DesktopID: spawned.DesktopID,
		PaneID:    spawned.PaneID,
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

func (d *Daemon) delegationDestination(source *protocol.Session, desktopRef string, requestedProfile ...string) (profiles.Profile, *launchPlacement, error) {
	var profile profiles.Profile
	var beside *launchPlacement
	var err error
	if source == nil {
		if profile, err = d.resolveGardenProfile("", firstProfile(requestedProfile), ""); err != nil {
			return profiles.Profile{}, nil, fmt.Errorf("resolve the profile for a delegation without a source session: %w", err)
		}
	} else {
		if profile, err = d.liveLaunchProfile(source.ProfileID); err != nil {
			return profiles.Profile{}, nil, fmt.Errorf("source session %s: %w", source.ID, err)
		}
		if pinned := firstProfile(requestedProfile); pinned != "" && pinned != profile.ID {
			owner, _ := d.store.GetProfile(pinned)
			return profiles.Profile{}, nil, fmt.Errorf("delegation was accepted in profile %q; source session %s belongs to profile %q now", owner.Name, source.ID, profile.Name)
		}
		beside = d.placementBeside(source.ID)
	}
	if strings.TrimSpace(desktopRef) == "" {
		if beside == nil {
			desktop, err := d.store.LaunchDesktop(profile.ID, "")
			if err != nil {
				return profiles.Profile{}, nil, err
			}
			beside = &launchPlacement{desktopID: desktop.ID, direction: layouttree.DirectionVertical}
		}
		return profile, beside, nil
	}
	desktop, err := d.resolveDesktopRef(profile, desktopRef)
	if err != nil {
		return profiles.Profile{}, nil, fmt.Errorf("--desktop: %w", err)
	}
	if beside != nil && beside.desktopID == desktop.ID {
		return profile, beside, nil
	}
	return profile, &launchPlacement{desktopID: desktop.ID, direction: layouttree.DirectionVertical}, nil
}

func (d *Daemon) confirmDelegatedLaunch(operationID, sessionID, agent string, watch *launchWatch, result *protocol.DelegateResult) error {
	if operationID != "" {
		_ = d.store.UpdateDelegationOperation(operationID, protocol.DelegationOperationStatePreparing,
			fmt.Sprintf("waiting for %s's first turn", agent), "", "", "", nil, nil, time.Now())
	}
	outcome := d.awaitDelegatedLaunch(sessionID, watch)
	if outcome.interrupted {
		return errDelegationInterrupted
	}
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

func (d *Daemon) completedDelegationResult(session *protocol.Session, worktreeCreated bool) *protocol.DelegateResult {
	result := &protocol.DelegateResult{SessionID: session.ID, ProfileID: protocol.Ptr(session.ProfileID), Directory: session.Directory, Checkout: "reused", Agent: string(session.Agent)}
	if worktreeCreated {
		result.WorktreeCreated = protocol.Ptr(true)
		result.Checkout = "created"
	}
	if branch := strings.TrimSpace(protocol.Deref(session.Branch)); branch != "" {
		result.Branch = protocol.Ptr(branch)
	}
	if placement, placed, err := d.store.SessionPlacement(session.ID); err == nil && placed {
		result.DesktopID = protocol.Ptr(placement.DesktopID)
		result.PaneID = protocol.Ptr(placement.PaneID)
	}
	return result
}

func delegatedSeedPrompt(seedID, seedTitle string) string {
	return prompts.RenderText("delegation", "brief", prompts.Values{"seed_id": seedID, "seed_title": seedTitle})
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
	operation, err := d.scopedDelegationOperation(msg.ID, protocol.Deref(msg.SourceSessionID), protocol.Deref(msg.ProfileID), "")
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
			// A delegation interrupted by stop stays preparing on purpose; the next daemon resumes it.
			select {
			case <-time.After(100 * time.Millisecond):
			case <-d.life.Done():
				d.sendCommandError(client, protocol.CmdDelegate, errDaemonStopping.Error())
				return
			}
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
