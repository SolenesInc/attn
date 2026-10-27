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
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

type internalSpawnPolicy struct {
	installWorkflowSkill  bool
	requireWorkflowSkill  bool
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
	addsTerminal    bool
}

type spawnPlan struct {
	spawnOpts                ptybackend.SpawnOptions
	launchSession            *protocol.Session
	chiefAssigned            bool
	isChief                  bool
	chiefAssignmentCommitted bool
	launchedConversation     string
	agent                    *AgentLaunch
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
	if plan.agent != nil {
		d.launchCommitted(plan.spawnOpts.ID, false, plan.agent.Profile, plan.agent.driverRunID())
	}
	if d.store.Get(sessionID) == nil {
		d.forgetMonitor(sessionID)
	}
	if plan.chiefAssigned && !plan.chiefAssignmentCommitted {
		d.clearChiefOfStaffIfSession(sessionID)
	}
	if plan.spawnOpts.ID != "" {
		d.terminals().unexpect(plan.spawnOpts.ID)
		d.kickMonitorTerminals()
	}
}

func (plan *spawnPlan) commit() {
	plan.chiefAssignmentCommitted = true
}

func (d *Daemon) validateSpawnPrelock(msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) (*spawnRequest, *spawnRejection) {
	requestedAgent := strings.TrimSpace(strings.ToLower(msg.Agent))
	if host := protocol.Deref(msg.Host); host != "" && requestedAgent != "" && requestedAgent != protocol.AgentShellValue && requestedAgent != "claude" && requestedAgent != "codex" {
		return nil, &spawnRejection{err: fmt.Errorf("outpost limit: plugin harness %s runs only at the home, not on %s", requestedAgent, host)}
	}
	pluginDriver, hasPluginDriver := d.ensurePluginRegistry().driver(requestedAgent)
	agent := normalizeSpawnAgent(msg.Agent)
	if hasPluginDriver {
		agent = pluginDriver.Agent
	} else if requestedAgent != "" && requestedAgent != protocol.AgentShellValue && agentdriver.Get(requestedAgent) == nil {
		return nil, &spawnRejection{err: fmt.Errorf("agent %q is not available", requestedAgent)}
	}
	isShell := agent == protocol.AgentShellValue
	if host := protocol.Deref(msg.Host); host != "" {
		if protocol.Deref(msg.ChiefOfStaff) || (policy.launchPlacement != nil && policy.launchPlacement.kind == "crew") || d.crewMemberBoundTo(msg.ID) != "" {
			return nil, &spawnRejection{err: fmt.Errorf("outpost limit: the chief and crew run only at the home, not on %s", host)}
		}
		if !d.outpostsEnabled() {
			return nil, &spawnRejection{err: errors.New("outposts_enabled is off; remote terminals need it on")}
		}
		row, err := d.resolveOutpost(host)
		if err != nil {
			return nil, &spawnRejection{err: err}
		}
		msg.Host = protocol.Ptr(row.ID)
		if _, _, err = d.outpostConn(row.ID); err != nil {
			return nil, &spawnRejection{err: err}
		}
		if !isShell {
			if err := d.NotebookReady(row.ID); err != nil {
				return nil, &spawnRejection{err: err}
			}
		}
	}
	if !isShell && !hasPluginDriver {
		explicit := strings.TrimSpace(protocol.Deref(msg.Executable))
		if explicit == "" {
			explicit = legacyExecutableFromSpawnMessage(msg, agent)
		}
		if err := d.requireCatalogAgent(protocol.Deref(msg.Host), agent, explicit); err != nil {
			return nil, &spawnRejection{err: err}
		}
	}
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
	addsTerminal := d.addsTerminal(msg, policy)
	placement := requestedLaunchPlacement(msg.Placement)
	if policy.launchPlacement != nil || placement == nil {
		placed := false
		if !addsTerminal {
			if _, placed, err = d.store.SessionPlacement(msg.ID); err != nil {
				return nil, &spawnRejection{err: err}
			}
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
		placement.addsTerminal = addsTerminal
	}
	if err := d.checkLaunchPlacement(profile, placement); err != nil {
		return nil, &spawnRejection{err: err}
	}
	return &spawnRequest{msg: msg, policy: policy, agent: agent, pluginDriver: pluginDriver, hasPluginDriver: hasPluginDriver, isShell: isShell, initialPrompt: initialPrompt, profile: profile, placement: placement, autoModeDriver: autoModeDriver, addsTerminal: addsTerminal}, nil
}

func (d *Daemon) addsTerminal(msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) bool {
	conversation := d.linkConversation(msg.ID)
	return !policy.fromLedger && conversation != "" && protocol.Deref(msg.ResumeSessionID) == conversation
}

func (d *Daemon) checkExistingSessionMembership(req *spawnRequest) *spawnRejection {
	existing := req.existingSession
	if existing == nil {
		return nil
	}
	if existing.ProfileID != "" && existing.ProfileID != req.profile.ID {
		return &spawnRejection{err: fmt.Errorf("session %s belongs to profile %s, not %s; profile ownership cannot change", existing.ID, existing.ProfileID, req.profile.ID)}
	}
	if req.placement == nil || req.addsTerminal {
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
	if req.existingSession != nil {
		req.msg.Host = req.existingSession.Host
	}
	if host := protocol.Deref(req.msg.Host); host != "" {
		if _, _, err := d.outpostConn(host); err != nil {
			return &spawnRejection{err: err}
		}
	}
	req.cwd = req.msg.Cwd
	if namesHome(req.cwd) {
		machine, err := Call[protocol.TerminalShellResult](context.Background(), d.router(), protocol.TerminalShellMessage{Cmd: protocol.CmdTerminalShell, Host: protocol.Deref(req.msg.Host)})
		if err != nil {
			return &spawnRejection{err: err}
		}
		req.cwd = expandHome(req.cwd, machine.Home)
	}
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
	if !req.hasPluginDriver && protocol.Deref(msg.ResumePicker) && req.resumeSessionID != "" {
		readiness, err := d.placedConversationReadiness(msg.ID, protocol.Deref(msg.Host), req.agent, req.profile.ID, req.resumeSessionID, true)
		if err != nil {
			return nil, &spawnRejection{err: err}
		}
		if !readiness.Ready {
			d.logf("spawn: explicit resume target %s for session %s is not resumable; using resume picker", req.resumeSessionID, msg.ID)
			req.resumeSessionID = ""
		}
	}
	if req.existingSession != nil && req.hasPluginDriver && req.resumeSessionID == "" && req.pluginDriver.Capabilities["resume"] {
		req.resumeSessionID = d.store.GetResumeSessionID(msg.ID)
	}
	if req.existingSession != nil && !req.hasPluginDriver && !req.isShell {
		req.resumeSessionID = agentdriver.ResolveSpawnResumeSessionID(req.driver, req.existingSession.ID, req.resumeSessionID, d.store.GetResumeSessionID(msg.ID))
		readiness, err := d.placedConversationReadiness(msg.ID, protocol.Deref(msg.Host), req.agent, req.profile.ID, req.resumeSessionID, false)
		if err != nil {
			return nil, &spawnRejection{err: err}
		}
		if d.launchedHere(msg.ID, req.resumeSessionID) && !readiness.Known {
			d.logf("spawn: self-resume target %s has no transcript yet; fresh-spawning instead", req.resumeSessionID)
			req.resumeSessionID = ""
		}
	}
	if !req.hasPluginDriver && req.resumeSessionID != "" {
		readiness, err := d.placedConversationReadiness(msg.ID, protocol.Deref(msg.Host), req.agent, req.profile.ID, req.resumeSessionID, true)
		if err != nil {
			return nil, &spawnRejection{err: err}
		}
		if !readiness.Ready {
			return nil, &spawnRejection{err: fmt.Errorf("conversation %s could not be prepared for resume; check the daemon log", req.resumeSessionID)}
		}
	}
	configuredExecutable := strings.TrimSpace(protocol.Deref(msg.Executable))
	if configuredExecutable == "" {
		configuredExecutable = legacyExecutableFromSpawnMessage(msg, req.agent)
	}
	plan := &spawnPlan{}
	autoApprove := (req.agent == "claude" || req.agent == "codex") && defaultOnBooleanSetting(d.homeSetting(SettingAutoApproveEnabled))
	plan.spawnOpts = ptybackend.SpawnOptions{CWD: req.cwd, Agent: req.agent, Label: req.label, Cols: uint16(msg.Cols), Rows: uint16(msg.Rows), ResumeSessionID: req.resumeSessionID, ResumePicker: protocol.Deref(msg.ResumePicker), YoloMode: protocol.Deref(msg.YoloMode), Theme: d.currentTerminalTheme(), Executable: strings.TrimSpace(configuredExecutable), ClaudeExecutable: protocol.Deref(msg.ClaudeExecutable), CodexExecutable: protocol.Deref(msg.CodexExecutable), LoginShellEnv: d.cachedLoginShellEnv(), AutoApprove: autoApprove, Model: strings.TrimSpace(protocol.Deref(msg.Model)), Effort: strings.TrimSpace(protocol.Deref(msg.Effort))}
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

	return plan, nil
}

func (d *Daemon) executeSpawn(req *spawnRequest, plan *spawnPlan) *spawnOutcome {
	if req.addsTerminal {
		return d.executeAddedTerminal(req, plan)
	}
	msg := req.msg
	if req.existingSession != nil && !req.policy.fromLedger && d.sessionLive(context.Background(), msg.ID) {
		plan.rollback(d, msg.ID)
		return &spawnOutcome{alreadyLive: true}
	}
	terminal := d.launchTerminal(req)
	plan.spawnOpts.ID = terminal
	plan.launchSession = buildSpawnSessionRecord(msg, req.agent, req.cwd, req.label, req.profile.ID, req.existingSession, req.isShell, req.hasPluginDriver && !req.pluginDriver.Capabilities["state_reporting"], req.parentSessionID, nil)
	session := plan.launchSession
	session.Host = msg.Host
	if err := d.store.AddCheckedUnlessTeardown(session); err != nil {
		if plan.chiefAssigned {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
		plan.rollback(d, msg.ID)
		return &spawnOutcome{err: fmt.Errorf("persist session launch intent: %w", err)}
	}
	intent := launchIntentFromSpawnOptions(plan.spawnOpts, plan.isChief)
	intent.Asks = d.launchedWithAsks(session.ID)
	d.launchVentIntent(session.ID).applyTo(&intent)
	intent.AutoMode = msg.AutoMode
	if req.autoModeDriver {
		intent.ApprovalPolicy, intent.SandboxMode = effectiveSpawnPolicyPair(msg)
	}
	d.store.SetLaunchIntent(session.ID, intent)
	previousLink := d.store.SessionLink(session.ID)
	if err := d.store.SetSessionLink(session.ID, linkKind(req.link)); err != nil {
		d.logf("spawn: %v", err)
	}
	d.persistLaunchedConversation(req, plan)
	if !req.isShell {
		var err error
		plan.agent, err = d.buildAgentSpawnLaunch(req, plan)
		if err != nil {
			plan.rollback(d, msg.ID)
			if req.existingSession == nil {
				d.discardSpawnSession(msg.ID)
				d.forgetMonitor(msg.ID)
			}
			return &spawnOutcome{err: err}
		}
		plan.agent.PreviousLink = previousLink
	}
	if err := d.startSessionTerminal(session, terminal, plan.spawnOpts, plan.agent); err != nil {
		plan.rollback(d, msg.ID)
		if req.existingSession == nil {
			d.discardSpawnSession(msg.ID)
		}
		if plan.chiefAssigned {
			d.clearChiefOfStaffIfSession(msg.ID)
		}
		return &spawnOutcome{err: err}
	}
	if req.isShell {
		d.clearExitScreenBefore(msg.ID, req.spawnStartedAt)
	} else {
		pausepoint.At(pausepoint.AgentTerminalStarted)
	}
	return &spawnOutcome{}
}

func (d *Daemon) startSessionTerminal(session *protocol.Session, terminal harness.TerminalID, opts ptybackend.SpawnOptions, agent *AgentLaunch) error {
	launch, err := encodeLaunch(opts)
	if err != nil {
		return err
	}
	agentJSON, err := encodeAgentLaunch(agent)
	if err != nil {
		return err
	}
	msg := protocol.TerminalStartMessage{Agent: agentJSON, Cmd: protocol.CmdTerminalStart, Host: protocol.Deref(session.Host), Session: *session, TerminalID: terminal, Launch: launch}
	if msg.Host != "" {
		_, name, err := d.outpostConn(msg.Host)
		if err != nil {
			return err
		}
		msg.HostName = protocol.Ptr(name)
		msg.Copy, err = d.store.SessionCopy(session.ID)
		if err != nil {
			return err
		}
	}
	d.expectTerminal(terminal, session.ID)
	_, err = Call[struct{}](context.Background(), d.router(), msg)
	return err
}

func (d *Daemon) executeAddedTerminal(req *spawnRequest, plan *spawnPlan) *spawnOutcome {
	plan.spawnOpts.ID = d.launchTerminal(req)
	plan.launchSession = req.existingSession
	var err error
	plan.agent, err = d.buildAgentSpawnLaunch(req, plan)
	if err == nil {
		err = d.startSessionTerminal(plan.launchSession, plan.spawnOpts.ID, plan.spawnOpts, plan.agent)
	}
	if err != nil {
		plan.rollback(d, req.msg.ID)
	}
	return &spawnOutcome{err: err}
}

// launchTerminal picks the terminal a launch runs in before its worker starts: the one a pane already
// holds for the session unless the launch adds a terminal, else a new one its pane will record.
func (d *Daemon) launchTerminal(req *spawnRequest) harness.TerminalID {
	if terminal, ok := d.terminals().Primary(req.msg.ID); ok && !req.addsTerminal {
		return terminal
	}
	terminal := harness.TerminalID(uuid.NewString())
	if req.placement != nil {
		req.placement.terminal = terminal
	}
	return terminal
}

func (d *Daemon) expectTerminal(terminal harness.TerminalID, session protocol.SessionID) {
	if _, shown := d.shownIn(terminal); !shown {
		d.terminals().expect(terminal, session, d.copiedSession(session))
		d.kickMonitorTerminals()
	}
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
	return d.killTerminalRuntime(terminal, syscall.SIGTERM)
}

func (d *Daemon) removeSessionRuntime(terminal harness.TerminalID) error {
	return d.removeTerminalRuntime(context.Background(), terminal)
}

func (d *Daemon) commitSpawn(req *spawnRequest, plan *spawnPlan) *spawnOutcome {
	msg, session := req.msg, plan.launchSession
	current := d.store.Get(session.ID)
	if current == nil {
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
	if req.addsTerminal {
		return d.commitAddedTerminal(req, plan, session)
	}
	fact := FactSessionRegistered
	if req.existingSession != nil {
		fact = FactSessionReregistered
	}
	if req.existingSession == nil {
		req.placed = d.placeAnsweringRequester(session, req.placement, req.policy.requester)
	}
	if req.placed.err != nil {
		cleanupErr := errors.Join(d.killSessionRuntime(plan.spawnOpts.ID), d.removeSessionRuntime(plan.spawnOpts.ID))
		plan.rollback(d, msg.ID)
		if req.existingSession == nil {
			d.discardSpawnSession(session.ID)
		}
		return &spawnOutcome{err: errors.Join(req.placed.err, cleanupErr)}
	}
	d.store.SetSessionLaunchedAt(session.ID, req.spawnStartedAt)
	if protocol.Deref(msg.Host) == "" {
		d.store.UpsertRecentLocation(req.cwd)
	}
	d.publishFact(fact, string(session.ID), nil)
	plan.commit()
	if req.existingSession != nil {
		d.publishFact(FactSessionRespawned, string(session.ID), ptyRespawn{Terminal: string(plan.spawnOpts.ID)})
	}
	return &spawnOutcome{}
}

func (d *Daemon) commitAddedTerminal(req *spawnRequest, plan *spawnPlan, session *protocol.Session) *spawnOutcome {
	req.placed = d.placeAnsweringRequester(session, req.placement, req.policy.requester)
	if req.placed.err != nil {
		cleanupErr := errors.Join(d.killSessionRuntime(plan.spawnOpts.ID), d.removeSessionRuntime(plan.spawnOpts.ID))
		plan.rollback(d, session.ID)
		return &spawnOutcome{err: errors.Join(req.placed.err, cleanupErr)}
	}
	plan.commit()
	d.publishFact(FactSessionReregistered, string(session.ID), nil)
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
	placed.launch.finish()
	return placed, rejection
}

func (d *Daemon) runSpawnPipelineProtected(protection foregroundCleanupProtection, msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) (placed placementOutcome, rejection *spawnRejection) {
	defer func() {
		if rejection == nil || d.store.Get(msg.ID) == nil {
			return
		}
		if !d.sessionHasLiveWorker(msg.ID) {
			d.startSessionMonitor(msg.ID)
			d.sessionMonitor(msg.ID).RecordProcessExit()
		}
		d.publishFact(FactSessionReregistered, string(msg.ID), nil)
	}()
	ctx, cancel := context.WithTimeout(protection.Context(), 30*time.Second)
	defer cancel()
	req, rejection := d.validateSpawnPrelock(msg, policy)
	if rejection != nil {
		return placementOutcome{}, rejection
	}
	if holder := d.linkHolder(req.profile.ID, protocol.Deref(msg.ResumeSessionID)); holder != "" && holder != msg.ID {
		return d.showHolderProtected(protection, holder, msg, policy)
	}
	releaseSpawnLock := d.acquireSpawnLock(msg.ID)
	defer releaseSpawnLock()

	if err := ctx.Err(); err != nil {
		return placementOutcome{}, &spawnRejection{err: fmt.Errorf("prepare session launch: %w", err)}
	}
	if rejection := d.normalizeSpawnRequest(req); rejection != nil {
		return placementOutcome{}, rejection
	}
	if req.existingSession != nil && !req.addsTerminal {
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
		return placementOutcome{session: msg.ID}, nil
	}
	if outcome := d.commitSpawn(req, plan); outcome.err != nil {
		return placementOutcome{}, &spawnRejection{err: outcome.err}
	}
	req.placed.session = msg.ID
	if plan.agent != nil {
		req.placed.launch = func() { d.launchCommitted(plan.spawnOpts.ID, true, plan.agent.Profile, plan.agent.driverRunID()) }
	}
	return req.placed, nil
}

func (d *Daemon) showHolderProtected(protection foregroundCleanupProtection, holder protocol.SessionID, msg *protocol.SpawnSessionMessage, policy internalSpawnPolicy) (placementOutcome, *spawnRejection) {
	spawn, shown, err := d.spawnShowing(holder, msg.Cols, msg.Rows)
	if err != nil {
		return placementOutcome{}, &spawnRejection{err: err}
	}
	if placement := requestedLaunchPlacement(msg.Placement); placement != nil {
		shown.launchPlacement = placement
	}
	shown.userStarted, shown.requester = policy.userStarted, policy.requester
	return d.runSpawnPipelineProtected(protection, spawn, shown)
}

func (d *Daemon) buildAgentSpawnLaunch(req *spawnRequest, plan *spawnPlan) (*AgentLaunch, error) {
	opts, msg, chief, terminal := plan.spawnOpts, req.msg, plan.isChief, plan.spawnOpts.ID
	launch, err := d.agentLaunchContext(msg.ID, req.agent, chief)
	if err != nil {
		return nil, err
	}
	launch.InitialPrompt, launch.Link, launch.Profile = req.initialPrompt, linkKind(req.link), req.profile.ID
	launch.Monitor, launch.StartedAt = d.monitorLaunch(msg.ID), req.spawnStartedAt
	launch.InstallWorkflowSkill, launch.RequireWorkflowSkill = req.policy.installWorkflowSkill, req.policy.requireWorkflowSkill
	launch.AddedTerminal = req.addsTerminal
	launch.FreshCost = req.existingSession == nil && req.resumeSessionID == ""
	if req.hasPluginDriver {
		runID := uuid.NewString()
		params := pluginDriverSpawnParams{
			Agent:           req.agent,
			TerminalID:      terminal,
			RunID:           runID,
			CWD:             req.cwd,
			Label:           req.label,
			Yolo:            protocol.Deref(msg.YoloMode),
			Model:           opts.Model,
			Effort:          opts.Effort,
			InitialPrompt:   req.initialPrompt,
			ResumeSessionID: strings.TrimSpace(req.resumeSessionID),
		}
		if metadata := strings.TrimSpace(d.store.GetAgentMetadata(msg.ID)); metadata != "" && json.Valid([]byte(metadata)) {
			params.Metadata = json.RawMessage(metadata)
		}
		if req.pluginDriver.Capabilities["launch_instructions"] {
			instructions, err := d.preparePluginLaunchInstructions(msg.ID, req.profile.ID, chief,
				!req.pluginDriver.Capabilities["pull_request_reporting"])
			if err != nil {
				return nil, err
			}
			params.Instructions = instructions
		}
		if req.pluginDriver.Capabilities["auto_mode"] {
			cfg, err := d.store.GetAutoModeConfig()
			if err != nil {
				return nil, fmt.Errorf("read auto mode config: %w", err)
			}
			if msg.AutoMode != nil {
				cfg.EnabledDefault = *msg.AutoMode
			}
			policy, sandbox := effectiveSpawnPolicyPair(msg)
			cfg = applySessionPolicyPair(cfg, policy, sandbox)
			cfg, _, err = d.autoModeConfigForSession(cfg, params.CWD)
			if err != nil {
				return nil, err
			}
			params.AutoMode = &cfg
		}
		launch.Plugin = &AgentPluginLaunch{Plugin: req.pluginDriver.PluginName, Params: params, Resume: req.pluginDriver.Capabilities["resume"] && (req.existingSession != nil || params.ResumeSessionID != "")}
	}
	return launch, nil
}

func (d *Daemon) discardSpawnSession(id protocol.SessionID) {
	session, link := d.store.Get(id), d.linkOf(id)
	d.store.Remove(id)
	d.forgetMonitor(id)
	if session != nil && link != nil {
		d.life.Go("linkReleasedAfterSpawnDiscard", func() { link.profileReleased(session.ProfileID) })
	}
}
