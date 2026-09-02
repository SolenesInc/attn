package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

// Backstop only: the killed worker's exit normally consumes the flag within milliseconds,
// and a never-arriving exit must not wedge it and suppress an unrelated exit.
const reloadStuckFlagGrace = 5 * time.Second

func (d *Daemon) markReloading(sessionID string) {
	d.reloadingMu.Lock()
	defer d.reloadingMu.Unlock()
	if d.reloadingSessions == nil {
		d.reloadingSessions = make(map[string]bool)
	}
	d.reloadingSessions[sessionID] = true
}

func (d *Daemon) consumeReloading(sessionID string) bool {
	d.reloadingMu.Lock()
	defer d.reloadingMu.Unlock()
	if d.reloadingSessions[sessionID] {
		delete(d.reloadingSessions, sessionID)
		return true
	}
	return false
}

func (d *Daemon) clearReloading(sessionID string) {
	d.reloadingMu.Lock()
	defer d.reloadingMu.Unlock()
	delete(d.reloadingSessions, sessionID)
}

// reloadLockFor serializes reloadSessionAgent's kill→remove→spawn composite: two concurrent
// reloads interleave and the Spawn loser's "already exists" tears down the fresh agent.
func (d *Daemon) reloadLockFor(sessionID string) *sync.Mutex {
	d.reloadLocksMu.Lock()
	defer d.reloadLocksMu.Unlock()
	if d.reloadLocks == nil {
		d.reloadLocks = make(map[string]*sync.Mutex)
	}
	lock := d.reloadLocks[sessionID]
	if lock == nil {
		lock = &sync.Mutex{}
		d.reloadLocks[sessionID] = lock
	}
	return lock
}

func (d *Daemon) sessionHasLiveWorker(sessionID string) bool {
	if d.ptyBackend == nil {
		return false
	}
	for _, liveID := range d.ptyBackend.SessionIDs(context.Background()) {
		if liveID == sessionID {
			return true
		}
	}
	return false
}

func (d *Daemon) agentSupportsChiefGuidance(agent string) bool {
	switch strings.TrimSpace(strings.ToLower(agent)) {
	case string(protocol.SessionAgentClaude), string(protocol.SessionAgentCodex):
		return true
	}
	driver, ok := d.ensurePluginRegistry().driver(agent)
	return ok && driver.Capabilities["launch_instructions"]
}

func (d *Daemon) agentSupportsChiefReload(agent string) bool {
	if !d.agentSupportsChiefGuidance(agent) {
		return false
	}
	if driver, ok := d.ensurePluginRegistry().driver(agent); ok {
		return driver.Capabilities["resume"]
	}
	return true
}

func (d *Daemon) reloadSessionAgent(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || d.ptyBackend == nil || d.store == nil {
		return
	}
	lock := d.reloadLockFor(sessionID)
	lock.Lock()
	defer lock.Unlock()

	session := d.store.Get(sessionID)
	if session == nil {
		d.logf("reload: session %s not found (closed or remote); skipping", sessionID)
		return
	}
	agent := string(session.Agent)
	if !d.agentSupportsChiefReload(agent) {
		d.logf("reload: agent %q for session %s has no chief-guidance launch path; skipping", agent, sessionID)
		return
	}
	if !d.sessionHasLiveWorker(sessionID) {
		d.logf("reload: session %s has no live worker; skipping", sessionID)
		return
	}

	opts, err := d.buildReloadSpawnOptions(session)
	if err != nil {
		// Never respawn with defaulted launch flags: a chief that kept stale guidance
		// beats one that silently lost yolo/executable.
		d.logf("reload: cannot reconstruct launch params for %s: %v; aborting (live worker preserved)", sessionID, err)
		return
	}
	pluginReload, err := d.preparePluginReload(session, &opts, d.isChiefOfStaffSession(sessionID))
	if err != nil {
		d.logf("reload: cannot reconstruct plugin launch for %s: %v; aborting (live worker preserved)", sessionID, err)
		return
	}
	if err := d.executePreparedSessionReload(sessionID, opts, pluginReload); err != nil {
		d.logf("reload: %v", err)
	}
}

func (d *Daemon) reloadSessionForClient(sessionID string, cols, rows int) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session not found")
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return errors.New("session not found")
	}

	lock := d.reloadLockFor(sessionID)
	lock.Lock()
	defer lock.Unlock()

	// A conversation session's runtime is a host, not a worker: sessionHasLiveWorker
	// reports false for a live one, so fork before either branch.
	if d.isConversationAgent(string(session.Agent)) {
		return d.reloadConversationSession(session)
	}

	if d.sessionHasLiveWorker(sessionID) {
		opts, err := d.buildReloadSpawnOptions(session)
		if err != nil {
			return err
		}
		pluginReload, err := d.preparePluginReload(session, &opts, d.isChiefOfStaffSession(sessionID))
		if err != nil {
			return err
		}
		return d.executePreparedSessionReload(sessionID, opts, pluginReload)
	}

	if cols <= 0 || rows <= 0 {
		return errors.New("reload requires pty geometry when no live worker exists")
	}
	intent, ok := d.store.LaunchIntent(sessionID)
	if !ok {
		return errors.New("no stored launch intent")
	}
	spawnMsg, policy := buildStoredIntentSpawn(session, intent, cols, rows)
	if rejection := d.runSpawnPipeline(spawnMsg, policy); rejection != nil {
		return rejection.reason()
	}
	d.publishFact(FactSessionRespawned, sessionID, nil)
	return nil
}

func (d *Daemon) handleReloadSession(client *wsClient, msg *protocol.ReloadSessionMessage) {
	err := d.reloadSessionForClient(msg.ID, msg.Cols, msg.Rows)
	result := protocol.ReloadSessionResultMessage{
		Event:   protocol.EventReloadSessionResult,
		ID:      msg.ID,
		Success: err == nil,
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

func (d *Daemon) executePreparedSessionReload(sessionID string, opts ptybackend.SpawnOptions, pluginReload *preparedPluginReload) error {
	if pluginReload != nil {
		defer pluginReload.abort()
	}
	ctx := context.Background()
	// Mark BEFORE kill, so the worker's async exit is suppressed however quickly it
	// fires relative to the kill returning.
	d.markReloading(sessionID)
	d.sessionInputs().fenceSession(sessionID)

	if killErr := d.ptyBackend.Kill(ctx, sessionID, syscall.SIGTERM); killErr != nil {
		d.logf("reload: kill returned error for %s (continuing): %v", sessionID, killErr)
	}
	// Remove synchronously first: the suppressed exit deliberately does NOT remove
	// the backend entry, and Spawn rejects a still-present id.
	if removeErr := d.ptyBackend.Remove(ctx, sessionID); removeErr != nil {
		d.logf("reload: remove returned error for %s (continuing): %v", sessionID, removeErr)
	}

	opts.DaemonEnv = d.spawnRoutingEnv()
	if spawnErr := d.ptyBackend.Spawn(ctx, opts); spawnErr != nil {
		d.logf("reload: respawn failed for %s: %v; finalizing as exited", sessionID, spawnErr)
		d.clearReloading(sessionID)
		d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 1})
		return fmt.Errorf("respawn failed for %s: %w", sessionID, spawnErr)
	}
	if pluginReload != nil {
		if commitErr := pluginReload.commit(); commitErr != nil {
			d.logf("reload: activate plugin run failed for %s: %v; finalizing as exited", sessionID, commitErr)
			_ = d.ptyBackend.Kill(ctx, sessionID, syscall.SIGTERM)
			_ = d.ptyBackend.Remove(ctx, sessionID)
			d.clearReloading(sessionID)
			d.closePluginDriverSession(sessionID, "reload_failed", nil, "")
			d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 1})
			return fmt.Errorf("activate plugin run failed for %s: %w", sessionID, commitErr)
		}
	}
	d.sessionInputs().forgetSession(sessionID)

	// Do NOT clear the flag here — the killed worker's exit consumes it, and the
	// AfterFunc backstop covers an exit that never arrives.
	time.AfterFunc(reloadStuckFlagGrace, func() { d.clearReloading(sessionID) })
	intent := launchIntentFromSpawnOptions(opts, d.isChiefOfStaffSession(sessionID))
	// SpawnOptions does not carry the auto mode choice, so rewriting the intent from
	// it alone would drop the launcher's override on every reload.
	if prior, ok := d.store.LaunchIntent(sessionID); ok {
		intent.AutoMode = prior.AutoMode
	}
	d.store.SetLaunchIntent(sessionID, intent)
	d.recordReviewerEvidence(sessionID, opts.ApprovalRoute.ReviewerInLoop())
	d.publishFact(FactSessionRespawned, sessionID, nil)
	d.logf("reload: respawned %s (agent=%s resume=%t yolo=%t)", sessionID, opts.Agent, opts.ResumeSessionID != "", opts.YoloMode)
	return nil
}

func (d *Daemon) buildReloadSpawnOptions(session *protocol.Session) (ptybackend.SpawnOptions, error) {
	sessionID := session.ID
	paramsProvider, ok := d.ptyBackend.(ptybackend.SessionLaunchParamsProvider)
	if !ok {
		return d.buildReloadSpawnOptionsFromStoredIntent(session, fmt.Errorf("backend does not record launch params"))
	}
	params, err := paramsProvider.SessionLaunchParams(context.Background(), sessionID)
	if err != nil {
		registryErr := fmt.Errorf("read launch params: %w", err)
		if errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
			return d.buildReloadSpawnOptionsFromStoredIntent(session, registryErr)
		}
		return ptybackend.SpawnOptions{}, registryErr
	}
	if !params.Recorded {
		return d.buildReloadSpawnOptionsFromStoredIntent(session, fmt.Errorf("launch params not recorded (pre-reload worker)"))
	}
	if _, known, routeErr := recordedApprovalRoute(params.ApprovalRoute, params.YoloMode, params.UnattendedLaunch); routeErr != nil {
		return ptybackend.SpawnOptions{}, fmt.Errorf("read launch params: %w", routeErr)
	} else if !known {
		if intent, exists := d.store.LaunchIntent(sessionID); exists && intent.ApprovalRoute.Valid() {
			params.ApprovalRoute = intent.ApprovalRoute
		}
	}
	return d.buildReloadSpawnOptionsFromLaunchParams(session, params)
}

func (d *Daemon) buildReloadSpawnOptionsFromStoredIntent(session *protocol.Session, registryErr error) (ptybackend.SpawnOptions, error) {
	intent, ok := d.store.LaunchIntent(session.ID)
	if !ok {
		return ptybackend.SpawnOptions{}, fmt.Errorf("%w; no stored launch intent exists either", registryErr)
	}
	d.logf("reload: using stored launch intent for %s (worker registry unavailable)", session.ID)
	return d.buildReloadSpawnOptionsFromLaunchParams(session, ptybackend.SessionLaunchParams{
		Recorded:         true,
		YoloMode:         intent.YoloMode,
		ApprovalRoute:    intent.ApprovalRoute,
		Executable:       intent.Executable,
		Model:            intent.Model,
		Effort:           intent.Effort,
		UnattendedLaunch: intent.UnattendedLaunch,
	})
}

func (d *Daemon) buildReloadSpawnOptionsFromLaunchParams(session *protocol.Session, params ptybackend.SessionLaunchParams) (ptybackend.SpawnOptions, error) {
	sessionID := session.ID
	cols, rows := uint16(80), uint16(24)
	if infoProvider, ok := d.ptyBackend.(ptybackend.SessionInfoProvider); ok {
		if info, err := infoProvider.SessionInfo(context.Background(), sessionID); err == nil {
			if info.Cols > 0 {
				cols = info.Cols
			}
			if info.Rows > 0 {
				rows = info.Rows
			}
		}
	}

	agent := normalizeSpawnAgent(string(session.Agent))
	if pluginDriver, ok := d.ensurePluginRegistry().driver(string(session.Agent)); ok {
		agent = pluginDriver.Agent
	}
	driver := agentdriver.Get(agent)
	resumeSessionID := agentdriver.ResolveSpawnResumeSessionID(driver, sessionID, "", d.store.GetResumeSessionID(sessionID))
	// Claude writes its transcript lazily on the first turn, so a chief promoted before it ever
	// took one has a resume id pointing at no file; a fresh launch reuses --session-id.
	if resumeSessionID != "" && !agentdriver.ResumeAvailable(driver, resumeSessionID) {
		d.logf("reload: resume target %s for session %s is not resumable (no transcript yet); fresh-spawning instead", resumeSessionID, sessionID)
		resumeSessionID = ""
	}

	opts := ptybackend.SpawnOptions{
		ID:                      sessionID,
		CWD:                     session.Directory,
		Agent:                   agent,
		Label:                   session.Label,
		Cols:                    cols,
		Rows:                    rows,
		ResumeSessionID:         resumeSessionID,
		Theme:                   d.currentTerminalTheme(),
		YoloMode:                params.YoloMode,
		ApprovalRoute:           params.ApprovalRoute,
		Executable:              params.Executable,
		ClaudeExecutable:        params.ClaudeExecutable,
		CodexExecutable:         params.CodexExecutable,
		CopilotExecutable:       params.CopilotExecutable,
		Model:                   params.Model,
		Effort:                  params.Effort,
		LoginShellEnv:           d.cachedLoginShellEnv(),
		WorkflowGuidanceEnabled: parseBooleanSetting(d.store.GetSetting(SettingWorkflowsEnabled)),
		AutoApprove:             false,
		ContextWindowCap:        d.launchContextWindowCap(sessionID, string(session.Agent), d.isChiefOfStaffSession(sessionID)),
	}
	if !params.UnattendedLaunch.IsZero() {
		if err := params.UnattendedLaunch.Validate(); err != nil {
			return ptybackend.SpawnOptions{}, fmt.Errorf("invalid recorded unattended launch contract: %w", err)
		}
		if !strings.EqualFold(agent, params.UnattendedLaunch.Agent) {
			return ptybackend.SpawnOptions{}, fmt.Errorf("recorded unattended launch agent %q does not match session agent %q", params.UnattendedLaunch.Agent, agent)
		}
		opts.YoloMode = false
		opts.Executable = ""
		opts.Model = ""
		opts.Effort = ""
		opts.UnattendedLaunch = params.UnattendedLaunch
	}
	route, known, err := recordedApprovalRoute(params.ApprovalRoute, params.YoloMode, params.UnattendedLaunch)
	if err != nil {
		return ptybackend.SpawnOptions{}, err
	}
	if !known {
		route = launchcontract.ApprovalRouteUser
	}
	if err := applyApprovalRoute(&opts, route); err != nil {
		return ptybackend.SpawnOptions{}, err
	}
	return opts, nil
}

type preparedPluginReload struct {
	d          *Daemon
	sessionID  string
	pluginName string
	runID      string
	completed  bool
}

func (p *preparedPluginReload) abort() {
	if p == nil || p.completed {
		return
	}
	p.completed = true
	p.d.abortPluginSessionLaunch(p.sessionID, "launch_failed")
}

func (p *preparedPluginReload) commit() error {
	if p == nil || p.completed {
		return nil
	}
	oldRun := p.d.store.GetAgentDriverRun(p.sessionID)
	if !p.d.store.BeginAgentDriverRun(p.sessionID, p.pluginName, p.runID) {
		return fmt.Errorf("initialize plugin driver run cursor")
	}
	p.completed = true
	if exit := p.d.finishPluginSessionLaunch(p.sessionID, true); exit != nil {
		go p.d.handlePTYExit(*exit)
	}
	if oldRun.RunID != "" && oldRun.RunID != p.runID {
		p.d.notifyPluginDriverSessionClosed(oldRun.PluginName, p.sessionID, oldRun.RunID, "reloaded", nil, "")
	}
	return nil
}

// preparePluginReload resolves the replacement plugin command BEFORE the live worker is
// killed, so an unavailable plugin or bad metadata leaves the current runtime untouched.
func (d *Daemon) preparePluginReload(session *protocol.Session, opts *ptybackend.SpawnOptions, isChief bool) (*preparedPluginReload, error) {
	reg, ok := d.ensurePluginRegistry().driver(string(session.Agent))
	if !ok {
		return nil, nil
	}
	if !reg.Capabilities["launch_instructions"] || !reg.Capabilities["resume"] {
		return nil, fmt.Errorf("agent %q requires launch_instructions and resume capabilities", reg.Agent)
	}
	runID := uuid.NewString()
	d.beginPluginSessionLaunch(session.ID, reg.PluginName, runID)
	instructions, err := d.preparePluginLaunchInstructions(session.ID, session.WorkspaceID, isChief,
		!reg.Capabilities["pull_request_reporting"])
	if err != nil {
		d.finishPluginSessionLaunch(session.ID, false)
		return nil, err
	}
	prepared := &preparedPluginReload{
		d: d, sessionID: session.ID, pluginName: reg.PluginName, runID: runID,
	}
	params := pluginDriverSpawnParams{
		Agent:        reg.Agent,
		SessionID:    session.ID,
		RunID:        runID,
		CWD:          session.Directory,
		Label:        session.Label,
		Yolo:         opts.YoloMode,
		Model:        opts.Model,
		Effort:       opts.Effort,
		Instructions: instructions,
	}
	if reg.Capabilities["auto_mode"] {
		cfg, err := d.store.GetAutoModeConfig()
		if err != nil {
			prepared.abort()
			return nil, fmt.Errorf("read auto mode config: %w", err)
		}
		if intent, ok := d.store.LaunchIntent(session.ID); ok && intent.AutoMode != nil {
			cfg.EnabledDefault = *intent.AutoMode
		}
		cfg = d.autoModeConfigForSession(cfg, params.CWD)
		params.AutoMode = &cfg
	}
	if metadata := strings.TrimSpace(d.store.GetAgentMetadata(session.ID)); metadata != "" && json.Valid([]byte(metadata)) {
		params.Metadata = json.RawMessage(metadata)
	}
	result, err := d.resolvePluginDriverLaunch(reg, params, true)
	if err != nil {
		prepared.abort()
		return nil, err
	}
	commandEnv, err := pluginCommandEnv(result.Env)
	if err != nil {
		prepared.abort()
		return nil, err
	}
	externalCWD := strings.TrimSpace(result.CWD)
	if externalCWD != "" {
		externalCWD, err = d.validateCrewBoundLaunchDir(session.ID, externalCWD)
		if err != nil {
			prepared.abort()
			return nil, err
		}
	}
	opts.Agent = reg.Agent
	opts.ResumeSessionID = ""
	opts.LifecycleID = runID
	opts.ExternalCommand = append([]string(nil), result.Argv...)
	opts.ExternalEnv = commandEnv
	opts.ExternalCWD = externalCWD
	return prepared, nil
}

type preparedPluginRoleReload struct {
	d         *Daemon
	sessionID string
	opts      ptybackend.SpawnOptions
	plugin    *preparedPluginReload
	lock      *sync.Mutex
	completed bool
}

func (p *preparedPluginRoleReload) abort() {
	if p == nil || p.completed {
		return
	}
	p.completed = true
	p.plugin.abort()
	p.lock.Unlock()
}

func (p *preparedPluginRoleReload) execute() error {
	if p == nil || p.completed {
		return nil
	}
	p.completed = true
	defer p.lock.Unlock()
	return p.d.executePreparedSessionReload(p.sessionID, p.opts, p.plugin)
}

// preparePluginRoleReload runs driver.resume BEFORE a chief-role change is
// persisted, so a failure leaves both the role and the live worker untouched.
func (d *Daemon) preparePluginRoleReload(sessionID string, desiredChief bool) (*preparedPluginRoleReload, bool, error) {
	session := d.store.Get(sessionID)
	if session == nil {
		return nil, false, nil
	}
	reg, registered := d.ensurePluginRegistry().driver(string(session.Agent))
	activeRun := d.store.GetAgentDriverRun(sessionID)
	pluginSession := registered || activeRun.RunID != ""
	if !pluginSession {
		return nil, false, nil
	}
	if !d.sessionHasLiveWorker(sessionID) {
		return nil, true, nil
	}
	if !registered {
		return nil, true, fmt.Errorf("agent %q plugin driver is unavailable", session.Agent)
	}
	if !reg.Capabilities["launch_instructions"] || !reg.Capabilities["resume"] {
		return nil, true, fmt.Errorf("agent %q requires launch_instructions and resume capabilities", reg.Agent)
	}

	lock := d.reloadLockFor(sessionID)
	lock.Lock()
	if !d.sessionHasLiveWorker(sessionID) {
		lock.Unlock()
		return nil, true, nil
	}
	session = d.store.Get(sessionID)
	if session == nil {
		lock.Unlock()
		return nil, true, nil
	}
	opts, err := d.buildReloadSpawnOptions(session)
	if err != nil {
		lock.Unlock()
		return nil, true, err
	}
	opts.ContextWindowCap = d.launchContextWindowCap(sessionID, string(session.Agent), desiredChief)
	pluginReload, err := d.preparePluginReload(session, &opts, desiredChief)
	if err != nil {
		lock.Unlock()
		return nil, true, err
	}
	if pluginReload == nil {
		lock.Unlock()
		return nil, true, fmt.Errorf("agent %q plugin driver became unavailable", session.Agent)
	}
	return &preparedPluginRoleReload{
		d: d, sessionID: sessionID, opts: opts, plugin: pluginReload, lock: lock,
	}, true, nil
}
