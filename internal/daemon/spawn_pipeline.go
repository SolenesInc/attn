package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/store"
)

type internalSpawnPolicy struct {
	launchPlacement       *launchPlacement
	unattendedLaunch      launchcontract.UnattendedLaunchSpec
	approvalRoute         launchcontract.ApprovalRoute
	preserveApprovalRoute bool
	userStarted           bool
	// The client that asked; it gets the placement as its own answer before any broadcast of it.
	requester *wsClient
}

type spawnRequest struct {
	msg             *protocol.SpawnSessionMessage
	policy          internalSpawnPolicy
	agent           string
	pluginDriver    pluginDriverRegistration
	hasPluginDriver bool
	isShell         bool
	initialPrompt   string
	profile         profiles.Profile
	placement       *launchPlacement
	placed          placementOutcome
	existingSession *protocol.Session
	cwd             string
	label           string
	spawnStartedAt  time.Time
	driver          agentdriver.Driver
	resumeSessionID string
	parentSessionID protocol.SessionID
	autoModeDriver  bool
	codexShared     bool
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
	launchedConversation         string
	priorConversation            store.SessionConversation
	conversationPersisted        bool
}

type spawnRejection struct {
	commandError string
	err          error
}

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

func (plan *spawnPlan) rollback(d *Daemon, sessionID protocol.SessionID) {
	if plan.cleanupInitialPromptOnReturn {
		plan.cleanupInitialPrompt()
	}
	if plan.chiefAssigned && !plan.chiefAssignmentCommitted {
		d.clearChiefOfStaffIfSession(sessionID)
	}
	if plan.spawnOpts.ID != "" {
		d.terminals().unexpect(plan.spawnOpts.ID)
	}
}

func (plan *spawnPlan) restoreLaunchIntent(d *Daemon, sessionID protocol.SessionID) {
	if plan.conversationPersisted {
		plan.conversationPersisted = false
		d.restoreSessionConversation(sessionID, plan.priorConversation)
	}
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
	autoModeDriver := hasPluginDriver && pluginDriver.Capabilities["auto_mode"]
	if policy, sandbox := requestedSpawnPolicyPair(msg); policy != "" || sandbox != "" {
		if err := automode.ValidatePolicyPair(policy, sandbox); err != nil {
			return nil, &spawnRejection{err: err}
		}
		if !autoModeDriver {
			return nil, &spawnRejection{err: fmt.Errorf(
				"agent %q does not support a per-session approval policy or sandbox mode", agent)}
		}
	}
	profile, err := d.liveLaunchProfile(msg.ProfileID)
	if err != nil {
		return nil, &spawnRejection{err: err}
	}
	placement := requestedLaunchPlacement(msg.Placement)
	if policy.launchPlacement != nil || placement == nil {
		_, placed, err := d.store.SessionPlacement(msg.ID)
		if err != nil {
			return nil, &spawnRejection{err: err}
		}
		if !placed {
			placement = policy.launchPlacement
			if placement == nil {
				placement = &launchPlacement{direction: layouttree.DirectionVertical}
			}
		}
	}
	if placement != nil {
		placement.focus = policy.userStarted
	}
	if err := d.checkLaunchPlacement(profile, placement); err != nil {
		return nil, &spawnRejection{err: err}
	}
	return &spawnRequest{msg: msg, policy: policy, agent: agent, pluginDriver: pluginDriver, hasPluginDriver: hasPluginDriver, isShell: isShell, initialPrompt: initialPrompt, profile: profile, placement: placement, autoModeDriver: autoModeDriver}, nil
}

func (d *Daemon) checkExistingSessionMembership(req *spawnRequest) *spawnRejection {
	existing := req.existingSession
	if existing == nil {
		return nil
	}
	if existing.ProfileID != "" && existing.ProfileID != req.profile.ID {
		return &spawnRejection{err: fmt.Errorf("session %s belongs to profile %s, not %s; profile ownership cannot change", existing.ID, existing.ProfileID, req.profile.ID)}
	}
	if req.placement == nil {
		return nil
	}
	placement, placed, err := d.store.SessionPlacement(existing.ID)
	if err != nil {
		return &spawnRejection{err: fmt.Errorf("read the placement of session %s: %w", existing.ID, err)}
	}
	if placed {
		return &spawnRejection{err: fmt.Errorf("session %s is already placed in pane %s of desktop %s", existing.ID, placement.PaneID, placement.DesktopID)}
	}
	return nil
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
	if rejection := d.checkExistingSessionMembership(req); rejection != nil {
		return rejection
	}
	req.parentSessionID = protocol.SessionID(d.resolveSpawnParent(string(protocol.Deref(req.msg.SpawnedFrom)), req.profile, req.placement, req.isShell))
	if req.parentSessionID == "" && req.existingSession != nil {
		req.parentSessionID = protocol.TrimID(protocol.Deref(req.existingSession.ParentSessionID))
	}
	return nil
}

func (d *Daemon) resolveSpawnIntent(req *spawnRequest) (*spawnPlan, *spawnRejection) {
	msg := req.msg
	if !req.hasPluginDriver && protocol.Deref(msg.ResumePicker) && req.resumeSessionID != "" &&
		!d.conversationReady(req.driver, req.resumeSessionID) {
		d.logf("spawn: explicit resume target %s for session %s is not resumable; using resume picker", req.resumeSessionID, msg.ID)
		req.resumeSessionID = ""
	}
	if req.existingSession != nil && req.hasPluginDriver && req.resumeSessionID == "" && req.pluginDriver.Capabilities["resume"] {
		req.resumeSessionID = d.store.GetResumeSessionID(msg.ID)
	}
	if req.existingSession != nil && !req.hasPluginDriver {
		req.resumeSessionID = agentdriver.ResolveSpawnResumeSessionID(req.driver, req.existingSession.ID, req.resumeSessionID, d.store.GetResumeSessionID(msg.ID))
		if d.launchedHere(msg.ID, req.resumeSessionID) && !d.conversationKnown(req.driver, req.resumeSessionID) {
			d.logf("spawn: self-resume target %s has no transcript yet; fresh-spawning instead", req.resumeSessionID)
			req.resumeSessionID = ""
		}
	}
	if !req.hasPluginDriver && req.resumeSessionID != "" {
		if !d.conversationReady(req.driver, req.resumeSessionID) {
			return nil, &spawnRejection{err: fmt.Errorf("conversation %s could not be prepared for resume; check the daemon log", req.resumeSessionID)}
		}
	}
	configuredExecutable := strings.TrimSpace(protocol.Deref(msg.Executable))
	if configuredExecutable == "" {
		configuredExecutable = legacyExecutableFromSpawnMessage(msg, req.agent)
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
	plan.spawnOpts = ptybackend.SpawnOptions{CWD: req.cwd, Agent: req.agent, Label: req.label, Cols: uint16(msg.Cols), Rows: uint16(msg.Rows), ResumeSessionID: req.resumeSessionID, ResumePicker: protocol.Deref(msg.ResumePicker), YoloMode: protocol.Deref(msg.YoloMode), InitialPromptFile: plan.spawnOpts.InitialPromptFile, Theme: d.currentTerminalTheme(), Executable: strings.TrimSpace(configuredExecutable), ClaudeExecutable: protocol.Deref(msg.ClaudeExecutable), CodexExecutable: protocol.Deref(msg.CodexExecutable), CopilotExecutable: protocol.Deref(msg.CopilotExecutable), LoginShellEnv: d.cachedLoginShellEnv(), WorkflowGuidanceEnabled: parseBooleanSetting(d.store.GetSetting(SettingWorkflowsEnabled)), AutoApprove: parseBooleanSetting(d.store.GetSetting(SettingAutoApproveEnabled)), Model: strings.TrimSpace(protocol.Deref(msg.Model)), Effort: strings.TrimSpace(protocol.Deref(msg.Effort))}
	requestedChief := protocol.Deref(msg.ChiefOfStaff)
	if req.hasPluginDriver && requestedChief && !req.pluginDriver.Capabilities["launch_instructions"] {
		plan.rollback(d, msg.ID)
		return nil, &spawnRejection{err: fmt.Errorf("agent %q cannot be chief of staff without launch_instructions capability", req.agent)}
	}
	if req.hasPluginDriver && requestedChief && !req.pluginDriver.Capabilities["resume"] {
		plan.rollback(d, msg.ID)
		return nil, &spawnRejection{err: fmt.Errorf("agent %q cannot be chief of staff without resume capability", req.agent)}
	}
	plan.chiefAssigned = d.maybeAssignChiefOnSpawn(msg.ID, req.agent, req.profile.ID, requestedChief, req.existingSession)
	plan.isChief = d.chiefOfProfile(req.profile.ID) == msg.ID
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
	req.codexShared = d.launchesSharedCodex(req)
	if req.codexShared && req.resumeSessionID != "" {
		if other := d.codexShared().loadedElsewhere(req.profile.ID, req.resumeSessionID); other != "" {
			plan.rollback(d, msg.ID)
			return nil, &spawnRejection{err: fmt.Errorf("conversation %s is open in the shared Codex of profile %s; it can open here once that profile lets it go, about a minute after no terminal shows it", req.resumeSessionID, other)}
		}
	}
	if !req.codexShared && req.agent == string(protocol.SessionAgentCodex) {
		if holder := d.codexShared().holder(req.profile.ID, req.resumeSessionID); holder != "" && holder != msg.ID {
			plan.rollback(d, msg.ID)
			return nil, &spawnRejection{err: fmt.Errorf("conversation %s is open in shared Codex session %s, and two Codex processes must not write one conversation; show that session instead", req.resumeSessionID, holder)}
		}
	}

	return plan, nil
}

func (d *Daemon) executeSpawn(req *spawnRequest, plan *spawnPlan) *spawnOutcome {
	msg := req.msg
	if req.existingSession != nil && d.sessionLive(context.Background(), msg.ID) {
		plan.rollback(d, msg.ID)
		return &spawnOutcome{alreadyLive: true}
	}
	terminal := d.spawnTerminal(req)
	plan.spawnOpts.ID = terminal
	if req.hasPluginDriver {
		plan.pluginRunID = uuid.NewString()
		plan.spawnOpts.LifecycleID = plan.pluginRunID
		d.beginPluginSessionLaunch(msg.ID, req.pluginDriver.PluginName, plan.pluginRunID)
		params := pluginDriverSpawnParams{
			Agent:           req.agent,
			TerminalID:      terminal,
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
			instructions, err := d.preparePluginLaunchInstructions(msg.ID, req.profile.ID, plan.isChief,
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
			policy, sandbox := effectiveSpawnPolicyPair(msg)
			cfg = applySessionPolicyPair(cfg, policy, sandbox)
			cfg, _, err = d.autoModeConfigForSession(cfg, params.CWD)
			if err != nil {
				d.finishPluginSessionLaunch(msg.ID, false)
				plan.rollback(d, msg.ID)
				return &spawnOutcome{err: err}
			}
			params.AutoMode = &cfg
		}
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

	branchInfo, _ := d.readBranchInfo(context.Background(), gitTask{Kind: gitTaskSessionIdentity, Lane: gitInteractive}, req.cwd)
	plan.launchSession = buildSpawnSessionRecord(msg, req.agent, req.cwd, req.label, req.profile.ID, req.existingSession, req.isShell, req.hasPluginDriver && !req.pluginDriver.Capabilities["state_reporting"], req.parentSessionID, branchInfo)
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
	intent.CodexShared = req.codexShared
	intent.AutoMode = msg.AutoMode
	if req.autoModeDriver {
		intent.ApprovalPolicy, intent.SandboxMode = effectiveSpawnPolicyPair(msg)
	}
	d.store.SetLaunchIntent(session.ID, intent)
	d.persistLaunchedConversation(req, plan)
	d.rememberSessionTitleInitialPrompt(msg.ID, req.initialPrompt)
	priorExit := d.store.GetSessionExitScreen(msg.ID)
	if err := d.store.DeleteSessionExitScreen(msg.ID); err != nil {
		d.logf("exit screen of the previous process not cleared: session=%s err=%v", msg.ID, err)
	}
	hasInitialPrompt := strings.TrimSpace(req.initialPrompt) != ""
	priorEvidence, _ := d.evidenceTable().snapshot(msg.ID)
	d.startEvidence(msg.ID, sessionstate.Evidence{
		InitialPromptOwed: hasInitialPrompt && reportsTurnStarts(req.agent),
		ReviewerInLoop:    plan.spawnOpts.ApprovalRoute.ReviewerInLoop(),
	})
	err := d.prepareSharedCodexLaunch(req, plan)
	if err == nil {
		err = d.spawnSessionRuntime(msg.ID, plan.spawnOpts)
	}
	if err != nil {
		if req.codexShared {
			d.codexShared().closeView(plan.spawnOpts.ID)
		}
		d.forgetSessionTitleInitialPrompt(msg.ID)
		d.restoreExitScreen(msg.ID, priorExit)
		if req.existingSession == nil {
			d.store.Remove(msg.ID)
			d.forgetSessionTrace(msg.ID)
		} else if restoreErr := d.store.AddCheckedUnlessTeardown(req.existingSession); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restore prior session after spawn failure: %w", restoreErr))
		}
		if req.codexShared {
			d.codexShared().idleSoon(req.profile.ID)
		}
		if req.existingSession != nil {
			d.startEvidence(msg.ID, priorEvidence)
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
	if hasInitialPrompt {
		d.maybeGenerateSessionTitleFromPrompt(msg.ID, req.initialPrompt, sessionInputOrigin{})
	}
	if plan.spawnOpts.InitialPromptFile != "" {
		plan.cleanupInitialPromptOnReturn = false
		d.life.AfterFunc("cleanupInitialPrompt", initialPromptCleanupAfter, plan.cleanupInitialPrompt)
	}
	return &spawnOutcome{}
}

func (d *Daemon) prepareSharedCodexLaunch(req *spawnRequest, plan *spawnPlan) error {
	if !req.codexShared {
		return nil
	}
	executable := plan.spawnOpts.Executable
	if executable == "" {
		executable = plan.spawnOpts.CodexExecutable
	}
	remote, err := d.codexShared().prepareLaunch(plan.spawnOpts.ID, req.profile.ID, executable)
	if err != nil {
		return err
	}
	plan.spawnOpts.ExternalEnv = append(plan.spawnOpts.ExternalEnv, "ATTN_CODEX_REMOTE="+remote)
	return nil
}

func (d *Daemon) spawnSessionRuntime(sessionID protocol.SessionID, opts ptybackend.SpawnOptions) error {
	opts.DaemonEnv = d.spawnRoutingEnv()
	err := d.ptyBackend.Spawn(context.Background(), opts)
	if err == nil {
		d.sessionInputs().forgetSession(sessionID)
	}
	return err
}

// spawnTerminal picks the terminal a launch runs in before its worker starts: the one a pane already
// holds for the session, else a new one its pane will record, else the session's own id when unplaced.
func (d *Daemon) spawnTerminal(req *spawnRequest) harness.TerminalID {
	session := req.msg.ID
	if terminal, ok := d.terminals().Primary(session); ok {
		return terminal
	}

	terminal := harness.TerminalID(uuid.NewString())
	if req.placement != nil {
		req.placement.terminal = terminal
	}
	d.terminals().expect(terminal, session)
	return terminal
}

// launchedHere reports whether a resume id names the conversation a launch of this session
// created: the session's own id before terminals had ids, else one of its terminals.
func (d *Daemon) launchedHere(sessionID protocol.SessionID, resumeID string) bool {

	return slices.Contains(d.terminals().Of(sessionID), harness.TerminalID(resumeID))
}

// persistLaunchedConversation records the conversation a launch starts before its worker runs, so
// the harness's first report of it is never a move: a fresh Claude's is its terminal id, a picker's none.
func (d *Daemon) persistLaunchedConversation(req *spawnRequest, plan *spawnPlan) {
	sessionID := req.msg.ID
	conversation := agentdriver.SpawnResumeSessionID(req.driver, string(plan.spawnOpts.ID), req.resumeSessionID, plan.spawnOpts.ResumePicker)
	clearPicker := conversation == "" && !req.hasPluginDriver && (plan.spawnOpts.ResumePicker || conversationDecidesIdentity(req.driver))
	if conversation == "" && !clearPicker {
		return
	}
	plan.launchedConversation = conversation
	plan.priorConversation = d.store.GetSessionConversation(sessionID)
	if plan.priorConversation.NativeID == conversation {
		return
	}
	plan.conversationPersisted = true
	if clearPicker {
		d.store.SetResumeSessionID(sessionID, "")
		return
	}
	if _, err := d.store.TransitionSessionResumeID(sessionID, conversation); err != nil {
		d.logf("spawn: persist launched conversation for session %s: %v", sessionID, err)
	}
}

func (d *Daemon) restoreSessionConversation(sessionID protocol.SessionID, prior store.SessionConversation) {
	var err error
	switch {
	case prior.NativeID == "":
		d.store.SetResumeSessionID(sessionID, "")
	case prior.TranscriptPath != "":
		_, err = d.store.TransitionSessionConversation(sessionID, prior.NativeID, prior.TranscriptPath)
	default:
		_, err = d.store.TransitionSessionResumeID(sessionID, prior.NativeID)
	}
	if err != nil {
		d.logf("spawn: restore conversation for session %s: %v", sessionID, err)
	}
}

func (d *Daemon) killSessionRuntime(terminal harness.TerminalID) error {
	return d.ptyBackend.Kill(context.Background(), terminal, syscall.SIGTERM)
}

func (d *Daemon) removeSessionRuntime(terminal harness.TerminalID) error {
	return d.ptyBackend.Remove(context.Background(), terminal)
}

func (d *Daemon) commitSpawn(req *spawnRequest, plan *spawnPlan) *spawnOutcome {
	msg, session := req.msg, plan.launchSession
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
		killErr := d.killSessionRuntime(plan.spawnOpts.ID)
		removeErr := d.removeSessionRuntime(plan.spawnOpts.ID)
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
	fact := FactSessionRegistered
	if req.existingSession != nil {
		fact = FactSessionReregistered
	}
	req.placed = d.placeAnsweringRequester(session, req.placement, req.policy.requester)
	if req.placed.err != nil {
		if req.hasPluginDriver {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		}
		cleanupErr := errors.Join(d.killSessionRuntime(plan.spawnOpts.ID), d.removeSessionRuntime(plan.spawnOpts.ID))
		if req.existingSession == nil {
			d.store.Remove(session.ID)
			d.forgetSessionTrace(session.ID)
		} else {
			cleanupErr = errors.Join(cleanupErr, d.store.AddCheckedUnlessTeardown(req.existingSession))
			plan.restoreLaunchIntent(d, msg.ID)
		}
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: errors.Join(req.placed.err, cleanupErr)}
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
		killErr := d.killSessionRuntime(plan.spawnOpts.ID)
		removeErr := d.removeSessionRuntime(plan.spawnOpts.ID)
		if req.existingSession == nil {
			d.store.Remove(session.ID)
			d.forgetSessionTrace(session.ID)
		} else {
			if req.placed.paneID != "" {
				_, _ = d.store.RemoveSessionPlacement(session.ID)
			}
			_ = d.store.AddCheckedUnlessTeardown(req.existingSession)
			plan.restoreLaunchIntent(d, msg.ID)
		}
		if req.placed.paneID != "" {
			d.publishArrangementChanged(session.ProfileID)
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
	if plan.launchedConversation != "" {
		d.rememberDispatchResume(session.ID, plan.launchedConversation)
	}

	d.store.SetSessionLaunchedAt(session.ID, req.spawnStartedAt)
	if !req.isShell {
		d.startTranscriptWatcher(session.ID, session.Agent, session.Directory, req.spawnStartedAt)
	}
	if pending, ok := d.consumePendingAgentConversation(session.ID); ok {
		d.observeAgentConversation(pending)
	}
	d.store.UpsertRecentLocation(req.cwd)
	d.publishFact(fact, string(session.ID), nil)
	if req.hasPluginDriver {
		if exit := d.finishPluginSessionLaunch(msg.ID, true); exit != nil {
			d.handlePTYExit(*exit)
		}
	}
	plan.commit()
	return &spawnOutcome{}
}

func (d *Daemon) runSpawnPipeline(msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) *spawnRejection {
	_, rejection := d.runSpawnPipelineReporting(msg, policy)
	return rejection
}

func (d *Daemon) runSpawnPipelineReporting(msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) (placementOutcome, *spawnRejection) {
	var placed placementOutcome
	var rejection *spawnRejection
	_ = d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		placed, rejection = d.runSpawnPipelineProtected(protection, msg, policy)
		return nil
	})
	return placed, rejection
}

func (d *Daemon) runSpawnPipelineProtected(_ foregroundCleanupProtection, msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) (placementOutcome, *spawnRejection) {
	req, rejection := d.validateSpawnPrelock(msg, policy)
	if rejection != nil {
		return placementOutcome{}, rejection
	}
	releaseSpawnLock := d.acquireSpawnLock(msg.ID)
	defer releaseSpawnLock()

	if rejection := d.normalizeSpawnRequest(req); rejection != nil {
		return placementOutcome{}, rejection
	}
	plan, rejection := d.resolveSpawnIntent(req)
	if rejection != nil {
		return placementOutcome{}, rejection
	}
	if outcome := d.executeSpawn(req, plan); outcome.err != nil {
		return placementOutcome{}, &spawnRejection{err: outcome.err}
	} else if outcome.alreadyLive {
		return placementOutcome{}, nil
	}
	if outcome := d.commitSpawn(req, plan); outcome.err != nil {
		d.forgetSessionTitleInitialPrompt(msg.ID)
		return placementOutcome{}, &spawnRejection{err: outcome.err}
	}
	return req.placed, nil
}
