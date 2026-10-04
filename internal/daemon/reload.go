package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

const reloadStuckFlagGrace = 5 * time.Second

func (d *Daemon) markReloading(id harness.TerminalID) {
	d.reloadingMu.Lock()
	defer d.reloadingMu.Unlock()
	if d.reloadingTerminals == nil {
		d.reloadingTerminals = make(map[harness.TerminalID]bool)
	}
	d.reloadingTerminals[id] = true
}

func (d *Daemon) consumeReloading(id harness.TerminalID) bool {
	d.reloadingMu.Lock()
	defer d.reloadingMu.Unlock()
	if d.reloadingTerminals[id] {
		delete(d.reloadingTerminals, id)
		return true
	}
	return false
}

func (d *Daemon) clearReloading(id harness.TerminalID) {
	d.reloadingMu.Lock()
	defer d.reloadingMu.Unlock()
	delete(d.reloadingTerminals, id)
}

// sessionLocks holds one mutex per session id, kept only while a lease on it is out.
type sessionLocks struct {
	mu    sync.Mutex
	locks map[string]*sessionLockEntry
}

type sessionLockEntry struct {
	lock sync.Mutex
	refs int
}

type sessionLockLease struct {
	table     *sessionLocks
	sessionID string
	entry     *sessionLockEntry
}

func (l *sessionLockLease) Lock() {
	l.entry.lock.Lock()
}

func (l *sessionLockLease) Unlock() {
	l.entry.lock.Unlock()
	l.table.mu.Lock()
	defer l.table.mu.Unlock()
	l.entry.refs--
	if l.entry.refs == 0 && l.table.locks[l.sessionID] == l.entry {
		delete(l.table.locks, l.sessionID)
	}
}

func (t *sessionLocks) lease(sessionID string) *sessionLockLease {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.locks == nil {
		t.locks = make(map[string]*sessionLockEntry)
	}
	entry := t.locks[sessionID]
	if entry == nil {
		entry = &sessionLockEntry{}
		t.locks[sessionID] = entry
	}
	entry.refs++
	return &sessionLockLease{table: t, sessionID: sessionID, entry: entry}
}

func (d *Daemon) sessionLifecycleLockFor(sessionID string) *sessionLockLease {
	return d.sessionLifecycleLocks.lease(sessionID)
}

// lockSessionLifecycles takes the sessions' lifecycle locks in id order, so two handovers never wait on each other.
func (d *Daemon) lockSessionLifecycles(ids ...string) (unlock func()) {
	ids = slices.Compact(slices.Sorted(slices.Values(ids)))
	leases := make([]*sessionLockLease, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		lease := d.sessionLifecycleLockFor(id)
		lease.Lock()
		leases = append(leases, lease)
	}
	return func() {
		for _, lease := range slices.Backward(leases) {
			lease.Unlock()
		}
	}
}

func (d *Daemon) sessionHasLiveWorker(sessionID string) bool {
	return d.sessionLive(context.Background(), sessionID)
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
	release, held := d.life.Hold("reloadSessionAgent")
	if !held {
		return
	}
	defer release()
	lock := d.sessionLifecycleLockFor(sessionID)
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

	lock := d.sessionLifecycleLockFor(sessionID)
	lock.Lock()
	defer lock.Unlock()
	if d.sessionTeardownInFlight(sessionID) {
		return errors.New("session is closing")
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return errors.New("session not found")
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
	d.publishFact(FactSessionRespawned, sessionID, ptyRespawn{Terminal: string(d.primaryTerminal(sessionID))})
	return nil
}

// persistReloadedConversation records the conversation a reload launches before it runs, or clears a
// stale one when the harness names it, so its report reads as this session's and opens no successor.
func (d *Daemon) persistReloadedConversation(sessionID string, opts ptybackend.SpawnOptions) {
	driver := agentdriver.Get(opts.Agent)
	conversation := agentdriver.SpawnResumeSessionID(driver, string(opts.ID), opts.ResumeSessionID, opts.ResumePicker)
	if conversation == d.store.GetResumeSessionID(sessionID) || (conversation == "" && !conversationDecidesIdentity(driver)) {
		return
	}
	if conversation == "" {
		d.store.SetResumeSessionID(sessionID, "")
		return
	}
	if _, err := d.store.TransitionSessionResumeID(sessionID, conversation); err != nil {
		d.logf("reload: persist launched conversation for session %s: %v", sessionID, err)
	}
}

// ptyRespawn names the terminal whose process a respawn replaced; the fact's subject is its session.
type ptyRespawn struct {
	Terminal string `json:"terminal,omitempty"`
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
	terminal := opts.ID
	d.markReloading(terminal)
	d.sessionInputs().fenceSession(sessionID)
	unlockEnds := d.lockTerminalEnds(sessionID)

	if killErr := d.ptyBackend.Kill(ctx, terminal, syscall.SIGTERM); killErr != nil {
		d.logf("reload: kill returned error for %s (continuing): %v", sessionID, killErr)
	}
	if removeErr := d.ptyBackend.Remove(ctx, terminal); removeErr != nil {
		d.logf("reload: remove returned error for %s (continuing): %v", sessionID, removeErr)
	}

	d.persistReloadedConversation(sessionID, opts)
	opts.DaemonEnv = d.spawnRoutingEnv()
	if spawnErr := d.ptyBackend.Spawn(ctx, opts); spawnErr != nil {
		unlockEnds()
		d.logf("reload: respawn failed for %s: %v; finalizing as exited", sessionID, spawnErr)
		d.clearReloading(terminal)
		d.handlePTYExit(ptybackend.ExitInfo{ID: terminal, ExitCode: 1})
		return fmt.Errorf("respawn failed for %s: %w", sessionID, spawnErr)
	}
	if pluginReload != nil {
		if commitErr := pluginReload.commit(); commitErr != nil {
			d.logf("reload: activate plugin run failed for %s: %v; finalizing as exited", sessionID, commitErr)
			_ = d.ptyBackend.Kill(ctx, terminal, syscall.SIGTERM)
			_ = d.ptyBackend.Remove(ctx, terminal)
			unlockEnds()
			d.clearReloading(terminal)
			d.closePluginDriverSession(sessionID, "reload_failed", nil, "")
			d.handlePTYExit(ptybackend.ExitInfo{ID: terminal, ExitCode: 1})
			return fmt.Errorf("activate plugin run failed for %s: %w", sessionID, commitErr)
		}
	}
	unlockEnds()
	d.sessionInputs().forgetSession(sessionID)
	d.recordPlacedInputOwed(sessionID, false)

	d.life.AfterFunc("clearReloading", reloadStuckFlagGrace, func() { d.clearReloading(terminal) })
	intent := launchIntentFromSpawnOptions(opts, d.isChiefOfStaffSession(sessionID))
	if prior, ok := d.store.LaunchIntent(sessionID); ok {
		intent.AutoMode = prior.AutoMode
		intent.ApprovalPolicy, intent.SandboxMode = prior.ApprovalPolicy, prior.SandboxMode
	}
	d.store.SetLaunchIntent(sessionID, intent)
	d.recordReviewerEvidence(sessionID, opts.ApprovalRoute.ReviewerInLoop())
	d.publishFact(FactSessionRespawned, sessionID, ptyRespawn{Terminal: string(terminal)})
	d.logf("reload: respawned %s (agent=%s resume=%t yolo=%t)", sessionID, opts.Agent, opts.ResumeSessionID != "", opts.YoloMode)
	return nil
}

func (d *Daemon) buildReloadSpawnOptions(session *protocol.Session) (ptybackend.SpawnOptions, error) {
	sessionID := session.ID
	terminal := d.primaryTerminal(sessionID)
	paramsProvider, ok := d.ptyBackend.(ptybackend.SessionLaunchParamsProvider)
	if !ok {
		return d.buildReloadSpawnOptionsFromStoredIntent(session, terminal, fmt.Errorf("backend does not record launch params"))
	}
	params, err := paramsProvider.SessionLaunchParams(context.Background(), terminal)
	if err != nil {
		registryErr := fmt.Errorf("read launch params: %w", err)
		if errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
			return d.buildReloadSpawnOptionsFromStoredIntent(session, terminal, registryErr)
		}
		return ptybackend.SpawnOptions{}, registryErr
	}
	if !params.Recorded {
		return d.buildReloadSpawnOptionsFromStoredIntent(session, terminal, fmt.Errorf("launch params not recorded (pre-reload worker)"))
	}
	if _, known, routeErr := recordedApprovalRoute(params.ApprovalRoute, params.YoloMode, params.UnattendedLaunch); routeErr != nil {
		return ptybackend.SpawnOptions{}, fmt.Errorf("read launch params: %w", routeErr)
	} else if !known {
		if intent, exists := d.store.LaunchIntent(sessionID); exists && intent.ApprovalRoute.Valid() {
			params.ApprovalRoute = intent.ApprovalRoute
		}
	}
	return d.buildReloadSpawnOptionsFromLaunchParams(session, terminal, params)
}

func (d *Daemon) buildReloadSpawnOptionsFromStoredIntent(session *protocol.Session, terminal harness.TerminalID, registryErr error) (ptybackend.SpawnOptions, error) {
	intent, ok := d.store.LaunchIntent(session.ID)
	if !ok {
		return ptybackend.SpawnOptions{}, fmt.Errorf("%w; no stored launch intent exists either", registryErr)
	}
	d.logf("reload: using stored launch intent for %s (worker registry unavailable)", session.ID)
	return d.buildReloadSpawnOptionsFromLaunchParams(session, terminal, ptybackend.SessionLaunchParams{
		Recorded:         true,
		YoloMode:         intent.YoloMode,
		ApprovalRoute:    intent.ApprovalRoute,
		Executable:       intent.Executable,
		Model:            intent.Model,
		Effort:           intent.Effort,
		UnattendedLaunch: intent.UnattendedLaunch,
	})
}

func (d *Daemon) buildReloadSpawnOptionsFromLaunchParams(session *protocol.Session, terminal harness.TerminalID, params ptybackend.SessionLaunchParams) (ptybackend.SpawnOptions, error) {
	sessionID := session.ID
	cols, rows := uint16(80), uint16(24)
	if infoProvider, ok := d.ptyBackend.(ptybackend.SessionInfoProvider); ok {
		if info, err := infoProvider.SessionInfo(context.Background(), terminal); err == nil {
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
	if resumeSessionID != "" && !d.conversationReady(driver, resumeSessionID) {
		if d.conversationKnown(driver, resumeSessionID) {
			return ptybackend.SpawnOptions{}, fmt.Errorf("conversation %s could not be prepared for resume; check the daemon log", resumeSessionID)
		}
		d.logf("reload: resume target %s for session %s is not resumable (no transcript yet); fresh-spawning instead", resumeSessionID, sessionID)
		resumeSessionID = ""
	}

	opts := ptybackend.SpawnOptions{
		ID:                      terminal,
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
		info := *exit
		p.d.life.Go("handlePTYExit", func() { p.d.handlePTYExit(info) })
	}
	if oldRun.RunID != "" && oldRun.RunID != p.runID {
		p.d.notifyPluginDriverSessionClosed(oldRun.PluginName, p.d.primaryTerminal(p.sessionID), oldRun.RunID, "reloaded", nil, "")
	}
	return nil
}

func pluginReloadCapabilityError(reg pluginDriverRegistration, isChief bool) error {
	if !reg.Capabilities["resume"] {
		return fmt.Errorf("agent %q requires the resume capability to reload", reg.Agent)
	}
	if isChief && !reg.Capabilities["launch_instructions"] {
		return fmt.Errorf("agent %q cannot reload as chief of staff without the launch_instructions capability", reg.Agent)
	}
	return nil
}

func (d *Daemon) preparePluginReload(session *protocol.Session, opts *ptybackend.SpawnOptions, isChief bool) (*preparedPluginReload, error) {
	reg, ok := d.ensurePluginRegistry().driver(string(session.Agent))
	if !ok {
		return nil, nil
	}
	if err := pluginReloadCapabilityError(reg, isChief); err != nil {
		return nil, err
	}
	runID := uuid.NewString()
	d.beginPluginSessionLaunch(session.ID, reg.PluginName, runID)
	prepared := &preparedPluginReload{
		d: d, sessionID: session.ID, pluginName: reg.PluginName, runID: runID,
	}
	params := pluginDriverSpawnParams{
		Agent:     reg.Agent,
		SessionID: string(opts.ID),
		RunID:     runID,
		CWD:       session.Directory,
		Label:     session.Label,
		Yolo:      opts.YoloMode,
		Model:     opts.Model,
		Effort:    opts.Effort,
	}
	if reg.Capabilities["launch_instructions"] {
		instructions, err := d.preparePluginLaunchInstructions(session.ID, session.ProfileID, isChief,
			!reg.Capabilities["pull_request_reporting"])
		if err != nil {
			d.finishPluginSessionLaunch(session.ID, false)
			return nil, err
		}
		params.Instructions = instructions
	}
	if reg.Capabilities["auto_mode"] {
		cfg, err := d.store.GetAutoModeConfig()
		if err != nil {
			prepared.abort()
			return nil, fmt.Errorf("read auto mode config: %w", err)
		}
		if intent, ok := d.store.LaunchIntent(session.ID); ok {
			if intent.AutoMode != nil {
				cfg.EnabledDefault = *intent.AutoMode
			}
			cfg = applySessionPolicyPair(cfg, intent.ApprovalPolicy, intent.SandboxMode)
		}
		cfg, _, err = d.autoModeConfigForSession(cfg, params.CWD)
		if err != nil {
			prepared.abort()
			return nil, err
		}
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
	lock      *sessionLockLease
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
	if err := pluginReloadCapabilityError(reg, desiredChief); err != nil {
		return nil, true, err
	}

	lock := d.sessionLifecycleLockFor(sessionID)
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
