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
)

type internalSpawnPolicy struct {
	parentSessionID       protocol.SessionID
	launchPlacement       *launchPlacement
	unattendedLaunch      launchcontract.UnattendedLaunchSpec
	approvalRoute         launchcontract.ApprovalRoute
	preserveApprovalRoute bool
	userStarted           bool
	fromLedger            bool
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
	link            linkRuntime
}

type spawnPlan struct {
	spawnOpts                ptybackend.SpawnOptions
	launchSession            *protocol.Session
	pluginRunID              string
	chiefAssigned            bool
	isChief                  bool
	chiefAssignmentCommitted bool
	launchedConversation     string
	abandonLink              func()
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
	if plan.chiefAssigned && !plan.chiefAssignmentCommitted {
		d.clearChiefOfStaffIfSession(sessionID)
	}
	if plan.spawnOpts.ID != "" {
		d.terminals().unexpect(plan.spawnOpts.ID)
		d.removeAgentLaunchDir(plan.spawnOpts.ID)
	}
	if plan.abandonLink != nil {
		plan.abandonLink()
	}
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
	if req.label == "" && !req.isShell {
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
	if req.policy.parentSessionID != "" && req.isShell {
		req.parentSessionID = req.policy.parentSessionID
	}
	if req.parentSessionID == "" && req.existingSession != nil {
		req.parentSessionID = protocol.TrimID(protocol.Deref(req.existingSession.ParentSessionID))
	}
	return nil
}

func (d *Daemon) resolveSpawnIntent(ctx context.Context, req *spawnRequest) (*spawnPlan, *spawnRejection) {
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
		if l := d.linkOf(msg.ID); l != nil {
			if err := l.restore(req.profile.ID, req.resumeSessionID); err != nil {
				return nil, &spawnRejection{err: err}
			}
		}
		if !d.conversationReady(req.driver, req.resumeSessionID) {
			return nil, &spawnRejection{err: fmt.Errorf("conversation %s could not be prepared for resume; check the daemon log", req.resumeSessionID)}
		}
	}
	configuredExecutable := strings.TrimSpace(protocol.Deref(msg.Executable))
	if configuredExecutable == "" {
		configuredExecutable = legacyExecutableFromSpawnMessage(msg, req.agent)
	}
	plan := &spawnPlan{}
	autoApprove := (req.agent == "claude" || req.agent == "codex") && defaultOnBooleanSetting(d.daemonSetting(SettingAutoApproveEnabled))
	plan.spawnOpts = ptybackend.SpawnOptions{CWD: req.cwd, Agent: req.agent, Label: req.label, Cols: uint16(msg.Cols), Rows: uint16(msg.Rows), ResumeSessionID: req.resumeSessionID, ResumePicker: protocol.Deref(msg.ResumePicker), YoloMode: protocol.Deref(msg.YoloMode), Theme: d.currentTerminalTheme(), Executable: strings.TrimSpace(configuredExecutable), ClaudeExecutable: protocol.Deref(msg.ClaudeExecutable), CodexExecutable: protocol.Deref(msg.CodexExecutable), CopilotExecutable: protocol.Deref(msg.CopilotExecutable), LoginShellEnv: d.cachedLoginShellEnv(), AutoApprove: autoApprove, Model: strings.TrimSpace(protocol.Deref(msg.Model)), Effort: strings.TrimSpace(protocol.Deref(msg.Effort))}
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
	plan.spawnOpts.Model = d.resolveLaunchModel(ctx, req.agent, configuredExecutable, plan.isChief, plan.spawnOpts.Model)
	if err := ctx.Err(); err != nil {
		plan.rollback(d, msg.ID)
		return nil, &spawnRejection{err: fmt.Errorf("resolve launch model: %w", err)}
	}
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
	req.link = d.launchLink(req)
	if req.link != nil {
		if err := req.link.admit(req.profile.ID, req.resumeSessionID); err != nil {
			plan.rollback(d, msg.ID)
			return nil, &spawnRejection{err: err}
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
		plan.spawnOpts.AgentArgv = append([]string(nil), result.Argv...)
		plan.spawnOpts.AgentEnv = commandEnv
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

	if req.link != nil {
		plan.abandonLink = func() { req.link.launchFailed(plan.spawnOpts.ID, req.profile.ID) }
		if err := req.link.prepareLaunch(&plan.spawnOpts, req.profile.ID); err != nil {
			plan.rollback(d, msg.ID)
			return &spawnOutcome{err: err}
		}
	}
	if !req.isShell && !req.hasPluginDriver {
		if err := d.prepareAgentLaunch(msg.ID, terminal, plan.isChief, &plan.spawnOpts, req.initialPrompt); err != nil {
			plan.rollback(d, msg.ID)
			return &spawnOutcome{err: fmt.Errorf("prepare %s launch: %w", req.agent, err)}
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
	intent := launchIntentFromSpawnOptions(plan.spawnOpts, plan.isChief)
	intent.Asks = d.launchedWithAsks(session.ID)
	intent.AutoMode = msg.AutoMode
	if req.autoModeDriver {
		intent.ApprovalPolicy, intent.SandboxMode = effectiveSpawnPolicyPair(msg)
	}
	d.store.SetLaunchIntent(session.ID, intent)
	if prior := d.linkOf(session.ID); prior != nil && prior != req.link {
		defer d.life.Go("linkReleasedAfterLaunch", func() { prior.profileReleased(req.profile.ID) })
	}
	if err := d.store.SetSessionLink(session.ID, linkKind(req.link)); err != nil {
		d.logf("spawn: %v", err)
	}
	d.persistLaunchedConversation(req, plan)
	d.rememberSessionTitleInitialPrompt(msg.ID, req.initialPrompt)
	hasInitialPrompt := strings.TrimSpace(req.initialPrompt) != ""
	d.startEvidence(msg.ID, monitorEvidence{TerminalEvidence: sessionstate.TerminalEvidence{ReviewerInLoop: plan.spawnOpts.ApprovalRoute.ReviewerInLoop()}, ConversationEvidence: sessionstate.ConversationEvidence{InitialPromptOwed: hasInitialPrompt && reportsTurnStarts(req.agent)}})
	if req.hasPluginDriver && !d.store.BeginAgentDriverRun(session.ID, req.pluginDriver.PluginName, plan.pluginRunID) {
		if req.existingSession == nil {
			d.store.Remove(msg.ID)
			d.forgetSessionTrace(msg.ID)
		}
		d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: fmt.Errorf("initialize plugin driver run cursor")}
	}
	if err := d.spawnSessionRuntime(msg.ID, plan.spawnOpts); err != nil {
		d.forgetSessionTitleInitialPrompt(msg.ID)
		if req.existingSession == nil {
			d.store.Remove(msg.ID)
			d.forgetSessionTrace(msg.ID)
		}
		if req.hasPluginDriver {
			d.store.EndAgentDriverRun(msg.ID)
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		}
		if plan.chiefAssigned {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: err}
	}
	if err := d.store.DeleteSessionExitScreenBefore(msg.ID, req.spawnStartedAt); err != nil {
		d.logf("exit screen of the previous process not cleared: session=%s err=%v", msg.ID, err)
	}
	if hasInitialPrompt {
		d.maybeGenerateSessionTitleFromPrompt(msg.ID, req.initialPrompt, sessionInputOrigin{})
	}
	return &spawnOutcome{}
}

func (d *Daemon) spawnSessionRuntime(sessionID protocol.SessionID, opts ptybackend.SpawnOptions) error {
	opts.DaemonEnv = d.spawnRoutingEnv()
	err := d.ptyBackend.Spawn(context.Background(), withLifecycleID(opts))
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
	if d.store.GetResumeSessionID(sessionID) == conversation {
		return
	}
	if clearPicker {
		d.store.SetResumeSessionID(sessionID, "")
		return
	}
	if _, err := d.store.TransitionSessionResumeID(sessionID, conversation); err != nil {
		d.logf("spawn: persist launched conversation for session %s: %v", sessionID, err)
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
	current := d.store.Get(session.ID)
	if current == nil {
		if req.hasPluginDriver {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		}
		if plan.chiefAssigned {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
		killErr := d.killSessionRuntime(plan.spawnOpts.ID)
		removeErr := d.removeSessionRuntime(plan.spawnOpts.ID)
		persistErr := fmt.Errorf("spawned session %s is no longer open", session.ID)
		if killErr != nil {
			persistErr = fmt.Errorf("%w; kill spawned runtime: %v", persistErr, killErr)
		}
		if removeErr != nil {
			persistErr = fmt.Errorf("%w; remove spawned runtime: %v", persistErr, removeErr)
		}
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: persistErr}
	}
	session = current
	fact := FactSessionRegistered
	if req.existingSession != nil {
		fact = FactSessionReregistered
	}
	if req.existingSession == nil {
		req.placed = d.placeAnsweringRequester(session, req.placement, req.policy.requester)
	}
	if req.placed.err != nil {
		if req.hasPluginDriver {
			d.abortPluginSessionLaunch(msg.ID, "launch_failed")
		}
		cleanupErr := errors.Join(d.killSessionRuntime(plan.spawnOpts.ID), d.removeSessionRuntime(plan.spawnOpts.ID))
		if req.existingSession == nil {
			d.store.Remove(session.ID)
			d.forgetSessionTrace(session.ID)
		}
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: errors.Join(req.placed.err, cleanupErr)}
	}
	if !req.isShell && req.existingSession == nil && req.resumeSessionID == "" {
		if err := d.store.InitializeSessionCostTracking(session.ID); err != nil {
			d.logf("initialize session cost tracking for %s: %v", session.ID, err)
		}
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
	var launchedExit *ptybackend.ExitInfo
	if req.hasPluginDriver {
		launchedExit = d.finishPluginSessionLaunch(msg.ID, true)
	}
	plan.commit()
	if req.existingSession != nil {
		d.publishFact(FactSessionRespawned, string(session.ID), ptyRespawn{Terminal: string(plan.spawnOpts.ID)})
	}
	if launchedExit != nil {
		d.life.Go("handlePTYExitAfterPluginLaunch", func() { d.handlePTYExit(*launchedExit) })
	}
	return &spawnOutcome{}
}

func (d *Daemon) runSpawnPipeline(msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) *spawnRejection {
	_, rejection := d.runSpawnPipelineReporting(msg, policy)
	return rejection
}

func (d *Daemon) runSpawnPipelineReporting(msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) (placementOutcome, *spawnRejection) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var placed placementOutcome
	var rejection *spawnRejection
	_ = d.worktreeMaintenance.ProtectFromAutomaticCleanup(ctx, func(protection foregroundCleanupProtection) error {
		placed, rejection = d.runSpawnPipelineProtected(protection, msg, policy)
		return nil
	})
	return placed, rejection
}

func (d *Daemon) runSpawnPipelineProtected(protection foregroundCleanupProtection, msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) (placed placementOutcome, rejection *spawnRejection) {
	defer func() {
		if rejection == nil || d.store.Get(msg.ID) == nil {
			return
		}
		if !d.sessionHasLiveWorker(msg.ID) {
			d.startSessionMonitor(msg.ID)
			d.recordProcessEvidence(msg.ID, true)
		}
		d.publishFact(FactSessionReregistered, string(msg.ID), nil)
	}()
	ctx, cancel := context.WithTimeout(protection.Context(), 30*time.Second)
	defer cancel()
	req, rejection := d.validateSpawnPrelock(msg, policy)
	if rejection != nil {
		return placementOutcome{}, rejection
	}
	releaseSpawnLock := d.acquireSpawnLock(msg.ID)
	defer releaseSpawnLock()

	if err := ctx.Err(); err != nil {
		return placementOutcome{}, &spawnRejection{err: fmt.Errorf("prepare session launch: %w", err)}
	}
	if rejection := d.normalizeSpawnRequest(req); rejection != nil {
		return placementOutcome{}, rejection
	}
	if req.existingSession != nil {
		req.placed = d.placeAnsweringRequester(req.existingSession, req.placement, req.policy.requester)
		if req.placed.err != nil {
			return req.placed, &spawnRejection{err: req.placed.err}
		}
		req.placement = nil
	}
	plan, rejection := d.resolveSpawnIntent(ctx, req)
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
