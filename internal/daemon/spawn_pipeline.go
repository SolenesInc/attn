package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/workspacelayout"
)

type internalSpawnPolicy struct {
	unattendedLaunch      launchcontract.UnattendedLaunchSpec
	approvalRoute         launchcontract.ApprovalRoute
	preserveApprovalRoute bool
}

type spawnRequest struct {
	msg             *protocol.SpawnSessionMessage
	policy          internalSpawnPolicy
	agent           string
	pluginDriver    pluginDriverRegistration
	hasPluginDriver bool
	isShell         bool
	initialPrompt   string
	workspaceID     string
	existingSession *protocol.Session
	cwd             string
	label           string
	spawnStartedAt  time.Time
	driver          agentdriver.Driver
	resumeSessionID string
	parentSessionID string
}

type spawnPlan struct {
	spawnOpts                    ptybackend.SpawnOptions
	launchSession                *protocol.Session
	pluginRunID                  string
	cleanupInitialPrompt         func()
	cleanupInitialPromptOnReturn bool
	chiefAssigned                bool
	isChief                      bool
	chiefAssignmentCommitted     bool
	priorIntent                  store.LaunchIntent
	hadPriorIntent               bool
}

type spawnRejection struct {
	commandError string
	err          error
}

// command error to. A rejection carries one or the other and never both, so
// reading `err` alone silently turns "missing workspace_id" into success.
func (r *spawnRejection) reason() error {
	if r == nil {
		return nil
	}
	if r.err != nil {
		return r.err
	}
	if r.commandError != "" {
		return errors.New(r.commandError)
	}
	return errors.New("spawn rejected")
}

type spawnOutcome struct {
	alreadyLive bool
	err         error
}

func (plan *spawnPlan) rollback(d *Daemon, sessionID string) {
	if plan.cleanupInitialPromptOnReturn {
		plan.cleanupInitialPrompt()
	}
	if plan.chiefAssigned && !plan.chiefAssignmentCommitted {
		d.clearChiefOfStaffIfSession(sessionID)
	}
}

func (plan *spawnPlan) restoreLaunchIntent(d *Daemon, sessionID string) {
	if plan.hadPriorIntent {
		d.store.SetLaunchIntent(sessionID, plan.priorIntent)
		return
	}
	d.store.ClearLaunchIntent(sessionID)
}

func (plan *spawnPlan) commit() {
	plan.chiefAssignmentCommitted = true
}

func (d *Daemon) validateSpawnPrelock(msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) (*spawnRequest, *spawnRejection) {
	requestedAgent := strings.TrimSpace(strings.ToLower(msg.Agent))
	pluginDriver, hasPluginDriver := d.ensurePluginRegistry().driver(requestedAgent)
	agent := normalizeSpawnAgent(msg.Agent)
	if hasPluginDriver {
		agent = pluginDriver.Agent
	} else if requestedAgent != "" && requestedAgent != protocol.AgentShellValue && agentdriver.Get(requestedAgent) == nil {
		return nil, &spawnRejection{err: fmt.Errorf("agent %q is not available", requestedAgent)}
	}
	isShell := agent == protocol.AgentShellValue
	initialPrompt := protocol.Deref(msg.InitialPrompt)
	if isShell && strings.TrimSpace(initialPrompt) != "" {
		return nil, &spawnRejection{err: errors.New("shell sessions do not accept an initial prompt")}
	}
	if strings.TrimSpace(initialPrompt) != "" {
		if hasPluginDriver && !pluginDriver.Capabilities["initial_prompt"] {
			return nil, &spawnRejection{err: fmt.Errorf("agent %q does not support initial prompts", requestedAgent)}
		}
		if !hasPluginDriver {
			driver := agentdriver.Get(agent)
			if driver == nil || !agentdriver.EffectiveCapabilities(driver).HasInitialPrompt {
				return nil, &spawnRejection{err: fmt.Errorf("agent %q does not support initial prompts", agent)}
			}
		}
	}
	workspaceID := strings.TrimSpace(msg.WorkspaceID)
	if workspaceID == "" {
		return nil, &spawnRejection{commandError: "missing workspace_id"}
	}
	if d.store.GetWorkspace(workspaceID) == nil {
		d.setWorkspacePaneStatusForSession(msg.ID, workspacelayout.PaneStatusFailed, "unknown workspace")
		return nil, &spawnRejection{commandError: "unknown workspace"}
	}
	return &spawnRequest{msg: msg, policy: policy, agent: agent, pluginDriver: pluginDriver, hasPluginDriver: hasPluginDriver, isShell: isShell, initialPrompt: initialPrompt, workspaceID: workspaceID}, nil
}

func (d *Daemon) normalizeSpawnRequest(req *spawnRequest) *spawnRejection {
	req.spawnStartedAt = time.Now()
	req.existingSession = d.store.Get(req.msg.ID)
	req.cwd = resolveSpawnCWD(req.msg.Cwd)
	req.label = protocol.Deref(req.msg.Label)
	if req.label == "" {
		req.label = filepath.Base(req.cwd)
	}
	if req.existingSession != nil && strings.TrimSpace(req.existingSession.Label) != "" {
		req.label = req.existingSession.Label
	}
	if req.msg.Cols <= 0 || req.msg.Rows <= 0 || req.msg.Cols > maxPTYDimValue || req.msg.Rows > maxPTYDimValue {
		return &spawnRejection{err: fmt.Errorf("invalid terminal size cols=%d rows=%d (expected 1..%d)", req.msg.Cols, req.msg.Rows, maxPTYDimValue)}
	}
	req.resumeSessionID = protocol.Deref(req.msg.ResumeSessionID)
	req.driver = agentdriver.Get(req.agent)
	req.parentSessionID = d.resolveSpawnParent(protocol.Deref(req.msg.SpawnedFrom), req.workspaceID, req.isShell)
	if req.parentSessionID == "" && req.existingSession != nil {
		req.parentSessionID = strings.TrimSpace(protocol.Deref(req.existingSession.ParentSessionID))
	}
	return nil
}

func (d *Daemon) resolveSpawnIntent(req *spawnRequest) (*spawnPlan, *spawnRejection) {
	msg := req.msg
	if !req.hasPluginDriver && protocol.Deref(msg.ResumePicker) && req.resumeSessionID != "" &&
		!agentdriver.ResumeAvailable(req.driver, req.resumeSessionID) {
		d.logf("spawn: explicit resume target %s for session %s is not resumable; using resume picker", req.resumeSessionID, msg.ID)
		req.resumeSessionID = ""
	}
	if req.existingSession != nil && req.hasPluginDriver && req.resumeSessionID == "" {
		req.resumeSessionID = d.store.GetResumeSessionID(msg.ID)
	}
	if req.existingSession != nil && !req.hasPluginDriver {
		req.resumeSessionID = agentdriver.ResolveSpawnResumeSessionID(req.driver, req.existingSession.ID, req.resumeSessionID, d.store.GetResumeSessionID(msg.ID))
		// Claude writes its transcript lazily, so a session that booted but was never
		// prompted has nothing on disk and `claude --resume <id>` exits non-zero.
		if req.resumeSessionID == msg.ID && !agentdriver.ResumeAvailable(req.driver, req.resumeSessionID) {
			d.logf("spawn: self-resume target %s has no transcript yet; fresh-spawning instead", msg.ID)
			req.resumeSessionID = ""
		}
	} else if !req.hasPluginDriver && req.resumeSessionID == "" && protocol.Deref(msg.ResumePicker) {
		mirroredResumeID := d.gardenDispatchResume(msg.ID)
		if mirroredResumeID == "" {
			mirroredResumeID = d.store.GetTicketResumeSessionID(msg.ID)
		}
		if ticketResumeID := mirroredResumeID; ticketResumeID != "" {
			// Claude writes its transcript lazily, so a mirrored id can point at a
			// transcript that does not exist and `claude -r <dead-id>` would exit non-zero.
			if agentdriver.ResumeAvailable(req.driver, ticketResumeID) {
				req.resumeSessionID = ticketResumeID
			} else {
				d.logf("spawn: resume target %s for session %s is not resumable (no transcript yet); using resume picker", ticketResumeID, msg.ID)
			}
		}
	}
	configuredExecutable := strings.TrimSpace(protocol.Deref(msg.Executable))
	if configuredExecutable == "" {
		configuredExecutable = legacyExecutableFromSpawnMessage(msg, req.agent)
	}
	// A conversation to pick up has to still be there: without this the fork throws,
	// the revive re-forks the same missing path, and the session flaps silently.
	if resume := strings.TrimSpace(protocol.Deref(msg.ResumeConversationFile)); resume != "" && !hostSessionStateDirHoldsConversation(msg.ID) {
		if info, err := os.Stat(resume); err != nil {
			return nil, &spawnRejection{err: fmt.Errorf("cannot pick up the conversation at %s: %w", resume, err)}
		} else if info.IsDir() {
			return nil, &spawnRejection{err: fmt.Errorf("cannot pick up the conversation at %s: it is a directory, not a conversation file", resume)}
		}
	}
	plan := &spawnPlan{cleanupInitialPrompt: func() {}}
	if !req.hasPluginDriver {
		initialPromptFile, cleanup, err := d.writeInitialPromptFile(msg.ID, req.initialPrompt)
		if err != nil {
			return nil, &spawnRejection{err: err}
		}
		plan.cleanupInitialPrompt = cleanup
		plan.cleanupInitialPromptOnReturn = initialPromptFile != ""
		plan.spawnOpts.InitialPromptFile = initialPromptFile
	}
	plan.spawnOpts = ptybackend.SpawnOptions{ID: msg.ID, CWD: req.cwd, Agent: req.agent, Label: req.label, Cols: uint16(msg.Cols), Rows: uint16(msg.Rows), ResumeSessionID: req.resumeSessionID, ResumePicker: protocol.Deref(msg.ResumePicker), YoloMode: protocol.Deref(msg.YoloMode), InitialPromptFile: plan.spawnOpts.InitialPromptFile, Theme: d.currentTerminalTheme(), Executable: strings.TrimSpace(configuredExecutable), ClaudeExecutable: protocol.Deref(msg.ClaudeExecutable), CodexExecutable: protocol.Deref(msg.CodexExecutable), CopilotExecutable: protocol.Deref(msg.CopilotExecutable), LoginShellEnv: d.cachedLoginShellEnv(), WorkflowGuidanceEnabled: parseBooleanSetting(d.store.GetSetting(SettingWorkflowsEnabled)), AutoApprove: parseBooleanSetting(d.store.GetSetting(SettingAutoApproveEnabled)), Model: strings.TrimSpace(protocol.Deref(msg.Model)), Effort: strings.TrimSpace(protocol.Deref(msg.Effort)), ResumeConversationFile: strings.TrimSpace(protocol.Deref(msg.ResumeConversationFile))}
	requestedChief := protocol.Deref(msg.ChiefOfStaff)
	if req.hasPluginDriver && requestedChief && !req.pluginDriver.Capabilities["launch_instructions"] {
		plan.rollback(d, msg.ID)
		return nil, &spawnRejection{err: fmt.Errorf("agent %q cannot be chief of staff without launch_instructions capability", req.agent)}
	}
	if req.hasPluginDriver && requestedChief && !req.pluginDriver.Capabilities["resume"] {
		plan.rollback(d, msg.ID)
		return nil, &spawnRejection{err: fmt.Errorf("agent %q cannot be chief of staff without resume capability", req.agent)}
	}
	plan.chiefAssigned = d.maybeAssignChiefOnSpawn(msg.ID, req.agent, requestedChief, req.existingSession)
	plan.isChief = d.isChiefOfStaffSession(msg.ID)
	plan.spawnOpts.Model = d.resolveLaunchModel(req.agent, plan.isChief, plan.spawnOpts.Model)
	plan.spawnOpts.Effort = d.resolveLaunchEffort(req.agent, plan.isChief, plan.spawnOpts.Effort)
	if launch := req.policy.unattendedLaunch; !launch.IsZero() {
		if err := launch.Validate(); err != nil {
			plan.rollback(d, msg.ID)
			return nil, &spawnRejection{err: err}
		}
		if !strings.EqualFold(req.agent, launch.Agent) {
			plan.rollback(d, msg.ID)
			return nil, &spawnRejection{err: fmt.Errorf("unattended launch agent %q does not match spawn agent %q", launch.Agent, req.agent)}
		}
		if strings.TrimSpace(protocol.Deref(msg.Model)) != strings.TrimSpace(launch.Model) || strings.TrimSpace(protocol.Deref(msg.Effort)) != strings.TrimSpace(launch.Effort) || strings.TrimSpace(configuredExecutable) != strings.TrimSpace(launch.Executable) {
			plan.rollback(d, msg.ID)
			return nil, &spawnRejection{err: errors.New("spawn message disagrees with unattended launch contract")}
		}
		plan.spawnOpts.AutoApprove, plan.spawnOpts.TrustWorkingDirectory, plan.spawnOpts.Model, plan.spawnOpts.Effort, plan.spawnOpts.Executable, plan.spawnOpts.UnattendedLaunch = false, false, "", "", "", launch
	}
	if req.policy.preserveApprovalRoute {
		route, known, err := recordedApprovalRoute(req.policy.approvalRoute, plan.spawnOpts.YoloMode, plan.spawnOpts.UnattendedLaunch)
		if err != nil {
			plan.rollback(d, msg.ID)
			return nil, &spawnRejection{err: err}
		}
		if !known {
			route = launchcontract.ApprovalRouteUser
		}
		if err := applyApprovalRoute(&plan.spawnOpts, route); err != nil {
			plan.rollback(d, msg.ID)
			return nil, &spawnRejection{err: err}
		}
	} else {
		plan.spawnOpts.ApprovalRoute = launchcontract.ResolveApprovalRoute(plan.spawnOpts.YoloMode, plan.spawnOpts.AutoApprove, plan.spawnOpts.UnattendedLaunch)
	}
	plan.spawnOpts.ContextWindowCap = d.launchContextWindowCap(msg.ID, req.agent, plan.isChief)

	return plan, nil
}

func (d *Daemon) executeSpawn(req *spawnRequest, plan *spawnPlan) *spawnOutcome {
	msg := req.msg
	if req.existingSession != nil {
		for _, liveID := range d.liveRuntimeSessionIDs(context.Background()) {
			if liveID == msg.ID {
				plan.rollback(d, msg.ID)
				return &spawnOutcome{alreadyLive: true}
			}
		}
	}
	if req.hasPluginDriver {
		plan.pluginRunID = uuid.NewString()
		plan.spawnOpts.LifecycleID = plan.pluginRunID
		d.beginPluginSessionLaunch(msg.ID, req.pluginDriver.PluginName, plan.pluginRunID)
		params := pluginDriverSpawnParams{
			Agent:           req.agent,
			SessionID:       msg.ID,
			RunID:           plan.pluginRunID,
			CWD:             req.cwd,
			Label:           req.label,
			Yolo:            protocol.Deref(msg.YoloMode),
			Model:           plan.spawnOpts.Model,
			Effort:          plan.spawnOpts.Effort,
			InitialPrompt:   req.initialPrompt,
			ResumeSessionID: strings.TrimSpace(req.resumeSessionID),
		}
		if metadata := strings.TrimSpace(d.store.GetAgentMetadata(msg.ID)); metadata != "" && json.Valid([]byte(metadata)) {
			params.Metadata = json.RawMessage(metadata)
		}
		if req.pluginDriver.Capabilities["launch_instructions"] {
			instructions, err := d.preparePluginLaunchInstructions(msg.ID, req.workspaceID, plan.isChief,
				!req.pluginDriver.Capabilities["pull_request_reporting"])
			if err != nil {
				d.finishPluginSessionLaunch(msg.ID, false)
				plan.rollback(d, msg.ID)
				return &spawnOutcome{err: err}
			}
			params.Instructions = instructions
		}
		if req.pluginDriver.Capabilities["auto_mode"] {
			cfg, err := d.store.GetAutoModeConfig()
			if err != nil {
				d.finishPluginSessionLaunch(msg.ID, false)
				plan.rollback(d, msg.ID)
				return &spawnOutcome{err: fmt.Errorf("read auto mode config: %w", err)}
			}
			if msg.AutoMode != nil {
				cfg.EnabledDefault = *msg.AutoMode
			}
			cfg = d.autoModeConfigForSession(cfg, params.CWD)
			params.AutoMode = &cfg
		}
		// A relaunch of a known session or an explicit conversation id resumes; a
		// driver without the capability relaunches fresh and refuses the explicit ask.
		resume := req.pluginDriver.Capabilities["resume"] && (req.existingSession != nil || params.ResumeSessionID != "")
		result, err := d.resolvePluginDriverLaunch(req.pluginDriver, params, resume)
		if err != nil {
			d.finishPluginSessionLaunch(msg.ID, false)
			plan.rollback(d, msg.ID)
			return &spawnOutcome{err: err}
		}
		commandEnv, err := pluginCommandEnv(result.Env)
		if err != nil {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
			plan.rollback(d, msg.ID)
			return &spawnOutcome{err: err}
		}
		plan.spawnOpts.ExternalCommand = append([]string(nil), result.Argv...)
		plan.spawnOpts.ExternalEnv = commandEnv
		plan.spawnOpts.ExternalCWD = strings.TrimSpace(result.CWD)
		if plan.spawnOpts.ExternalCWD != "" {
			resolved, err := d.validateCrewBoundLaunchDir(msg.ID, plan.spawnOpts.ExternalCWD)
			if err != nil {
				d.abortPluginSessionLaunch(msg.ID, "launch_failed")
				plan.rollback(d, msg.ID)
				return &spawnOutcome{err: err}
			}
			plan.spawnOpts.ExternalCWD = resolved
		}
	}

	// Persist the complete launch intent before creating the worker: a daemon death
	// after Spawn otherwise leaves a worker with no durable session row to recover.
	plan.launchSession = buildSpawnSessionRecord(msg, req.agent, req.cwd, req.label, req.existingSession, req.isShell, req.hasPluginDriver && !req.pluginDriver.Capabilities["state_reporting"], req.parentSessionID)
	session := plan.launchSession
	if err := d.store.AddCheckedUnlessTeardown(session); err != nil {
		if req.hasPluginDriver {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		}
		if plan.chiefAssigned {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: fmt.Errorf("persist session launch intent: %w", err)}
	}
	plan.priorIntent, plan.hadPriorIntent = d.store.LaunchIntent(session.ID)
	intent := launchIntentFromSpawnOptions(plan.spawnOpts, plan.isChief)
	if req.hasPluginDriver && req.pluginDriver.Capabilities[pluginDriverConversationCapability] {
		intent.InitialPrompt = req.initialPrompt
	}
	intent.AutoMode = msg.AutoMode
	d.store.SetLaunchIntent(session.ID, intent)
	// After the already-live no-op returns, before the runtime whose first
	// UserPromptSubmit can beat commitSpawn.
	d.rememberSessionTitleInitialPrompt(msg.ID, req.initialPrompt)
	priorExit := d.store.GetSessionExitScreen(msg.ID)
	if err := d.store.DeleteSessionExitScreen(msg.ID); err != nil {
		d.logf("exit screen of the previous process not cleared: session=%s err=%v", msg.ID, err)
	}
	if err := d.spawnSessionRuntime(req, plan.spawnOpts); err != nil {
		d.forgetSessionTitleInitialPrompt(msg.ID)
		d.restoreExitScreen(msg.ID, priorExit)
		if req.existingSession == nil {
			d.store.Remove(msg.ID)
		} else if restoreErr := d.store.AddCheckedUnlessTeardown(req.existingSession); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restore prior session after spawn failure: %w", restoreErr))
		}
		if req.existingSession != nil {
			plan.restoreLaunchIntent(d, msg.ID)
		}
		if req.hasPluginDriver {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		}
		if plan.chiefAssigned {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: err}
	}
	// Codex reports no permission mode to the daemon at any point, so without this
	// its guardian would be invisible — the arrangement the dwell exists for.
	d.recordReviewerEvidence(msg.ID, plan.spawnOpts.ApprovalRoute.ReviewerInLoop())
	if plan.spawnOpts.InitialPromptFile != "" {
		// The spawned wrapper removes the file after reading it. Keep a fallback
		// for failures between PTY spawn and wrapper startup.
		plan.cleanupInitialPromptOnReturn = false
		time.AfterFunc(5*time.Minute, plan.cleanupInitialPrompt)
	}
	return &spawnOutcome{}
}

func (d *Daemon) commitSpawn(req *spawnRequest, plan *spawnPlan) *spawnOutcome {
	msg, session := req.msg, plan.launchSession
	// A state transition or a rename (auto-title included) can land between
	// executeSpawn's persist and this commit; the upsert must not rewind them.
	if current := d.store.Get(session.ID); current != nil {
		session.State = current.State
		session.StateSince = current.StateSince
		session.StateUpdatedAt = current.StateUpdatedAt
		if strings.TrimSpace(current.Label) != "" {
			session.Label = current.Label
		}
	}
	if err := d.store.AddCheckedUnlessTeardown(session); err != nil {
		if req.hasPluginDriver {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		}
		if plan.chiefAssigned {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
		killErr := d.killSessionRuntime(msg.ID)
		removeErr := d.removeSessionRuntime(msg.ID)
		persistErr := fmt.Errorf("persist spawned session: %w", err)
		if killErr != nil {
			persistErr = fmt.Errorf("%w; kill spawned runtime: %v", persistErr, killErr)
		}
		if removeErr != nil {
			persistErr = fmt.Errorf("%w; remove spawned runtime: %v", persistErr, removeErr)
		}
		if req.existingSession != nil {
			plan.restoreLaunchIntent(d, msg.ID)
		}
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: persistErr}
	}
	if !req.isShell && req.existingSession == nil && req.resumeSessionID == "" {
		if err := d.store.InitializeSessionCostTracking(session.ID); err != nil {
			d.logf("initialize session cost tracking for %s: %v", session.ID, err)
		}
	}
	if req.hasPluginDriver && !d.store.BeginAgentDriverRun(session.ID, req.pluginDriver.PluginName, plan.pluginRunID) {
		d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		if plan.chiefAssigned {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
		killErr := d.killSessionRuntime(msg.ID)
		removeErr := d.removeSessionRuntime(msg.ID)
		if req.existingSession == nil {
			d.store.Remove(session.ID)
		} else {
			plan.restoreLaunchIntent(d, msg.ID)
		}
		cursorErr := fmt.Errorf("initialize plugin driver run cursor")
		if killErr != nil {
			cursorErr = fmt.Errorf("%w; kill spawned runtime: %v", cursorErr, killErr)
		}
		if removeErr != nil {
			cursorErr = fmt.Errorf("%w; remove spawned runtime: %v", cursorErr, removeErr)
		}
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: cursorErr}
	}
	if persistResumeID := agentdriver.SpawnResumeSessionID(req.driver, session.ID, req.resumeSessionID, protocol.Deref(msg.ResumePicker)); persistResumeID != "" {
		d.persistResumeSessionID(session.ID, persistResumeID)
	}
	if err := d.store.ClearTicketReconciliationForAssignee(session.ID); err != nil {
		d.logf("clear ticket reconciliation on spawn for %s: %v", session.ID, err)
	}
	d.reviveCrashedTicketsForSession(session.ID)
	if !req.isShell {
		d.startTranscriptWatcher(session.ID, session.Agent, session.Directory, req.spawnStartedAt)
	}
	if pending, ok := d.consumePendingAgentConversation(session.ID); ok {
		d.observeAgentConversation(pending)
	}
	d.store.UpsertRecentLocation(req.cwd)
	d.associateSessionWithWorkspace(session.ID, req.workspaceID)
	if req.workspaceID != "" {
		if _, err := d.ensureWorkspaceSessionPane(req.workspaceID, session.ID, session.Label); err != nil {
			d.logf("ensure workspace pane for session %s: %v", session.ID, err)
		}
	}
	d.setWorkspacePaneStatusForSession(session.ID, workspacelayout.PaneStatusReady, "")
	fact := FactSessionRegistered
	if req.existingSession != nil {
		fact = FactSessionReregistered
	}
	d.publishFact(fact, session.ID, nil)
	d.recomputeAndBroadcastWorkspaceForSession(session.ID)
	if req.hasPluginDriver {
		if exit := d.finishPluginSessionLaunch(msg.ID, true); exit != nil {
			d.handlePTYExit(*exit)
		}
	}
	plan.commit()
	return &spawnOutcome{}
}

func (d *Daemon) runSpawnPipeline(msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) *spawnRejection {
	req, rejection := d.validateSpawnPrelock(msg, policy)
	if rejection != nil {
		return rejection
	}
	releaseSpawnLock := d.acquireSpawnLock(msg.ID)
	defer releaseSpawnLock()

	if rejection := d.normalizeSpawnRequest(req); rejection != nil {
		return rejection
	}
	plan, rejection := d.resolveSpawnIntent(req)
	if rejection != nil {
		return rejection
	}
	if outcome := d.executeSpawn(req, plan); outcome.err != nil {
		return &spawnRejection{err: outcome.err}
	} else if outcome.alreadyLive {
		return nil
	}
	if outcome := d.commitSpawn(req, plan); outcome.err != nil {
		d.forgetSessionTitleInitialPrompt(msg.ID)
		return &spawnRejection{err: outcome.err}
	}
	return nil
}
