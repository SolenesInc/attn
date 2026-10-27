package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

type terminalExitIntent uint8

const terminalExitStop terminalExitIntent = 1

func (d *Daemon) markTerminalExitIntent(id harness.TerminalID, intent terminalExitIntent) {
	d.terminalExitIntentMu.Lock()
	defer d.terminalExitIntentMu.Unlock()
	if d.terminalExitIntents == nil {
		d.terminalExitIntents = make(map[harness.TerminalID]terminalExitIntent)
	}
	d.terminalExitIntents[id] |= intent
}

func (d *Daemon) consumeTerminalExitIntent(id harness.TerminalID, intent terminalExitIntent) bool {
	d.terminalExitIntentMu.Lock()
	defer d.terminalExitIntentMu.Unlock()
	if d.terminalExitIntents[id]&intent == 0 {
		return false
	}
	d.terminalExitIntents[id] &^= intent
	if d.terminalExitIntents[id] == 0 {
		delete(d.terminalExitIntents, id)
	}
	return true
}

func withLifecycleID(opts ptybackend.SpawnOptions) ptybackend.SpawnOptions {
	if opts.LifecycleID == "" {
		opts.LifecycleID = uuid.NewString()
	}
	return opts
}

func (d *Daemon) runtimeLifecycle(t harness.TerminalID) (string, bool) {
	provider, ok := d.ptyBackend.(ptybackend.RuntimeLifecycleProvider)
	if !ok {
		return "", false
	}
	return provider.RuntimeLifecycle(t)
}

func (d *Daemon) replaceRuntime(sessionID protocol.SessionID, t harness.TerminalID) (done func()) {
	unlockEnds := d.lockTerminalEnds(sessionID)
	if current, running := d.runtimeLifecycle(t); running {
		d.markReplaced(t, current)
	}
	return func() {
		d.terminalExitIntentMu.Lock()
		delete(d.replacedRuntimes, t)
		d.terminalExitIntentMu.Unlock()
		unlockEnds()
	}
}

func (d *Daemon) markReplaced(t harness.TerminalID, lifecycleID string) {
	d.terminalExitIntentMu.Lock()
	defer d.terminalExitIntentMu.Unlock()
	if d.replacedRuntimes == nil {
		d.replacedRuntimes = make(map[harness.TerminalID][]string)
	}
	d.replacedRuntimes[t] = append(d.replacedRuntimes[t], lifecycleID)
}

func (d *Daemon) exitOfSupersededRuntime(info ptybackend.ExitInfo) bool {
	d.terminalExitIntentMu.Lock()
	replacing := slices.Contains(d.replacedRuntimes[info.ID], info.LifecycleID)
	d.terminalExitIntentMu.Unlock()
	if replacing {
		return true
	}
	current, running := d.runtimeLifecycle(info.ID)
	return running && current != info.LifecycleID
}

func (d *Daemon) clearTerminalExitIntent(id harness.TerminalID, intent terminalExitIntent) {
	d.consumeTerminalExitIntent(id, intent)
}

type sessionLocks struct {
	mu    sync.Mutex
	locks map[protocol.SessionID]*sessionLockEntry
}

type sessionLockEntry struct {
	lock sync.Mutex
	refs int
}

type sessionLockLease struct {
	table     *sessionLocks
	sessionID protocol.SessionID
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

func (t *sessionLocks) lease(sessionID protocol.SessionID) *sessionLockLease {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.locks == nil {
		t.locks = make(map[protocol.SessionID]*sessionLockEntry)
	}
	entry := t.locks[sessionID]
	if entry == nil {
		entry = &sessionLockEntry{}
		t.locks[sessionID] = entry
	}
	entry.refs++
	return &sessionLockLease{table: t, sessionID: sessionID, entry: entry}
}

func (d *Daemon) sessionLifecycleLockFor(sessionID protocol.SessionID) *sessionLockLease {
	return d.sessionLifecycleLocks.lease(sessionID)
}

// lockSessionLifecycles takes the sessions' lifecycle locks in id order, so two handovers never wait on each other.
func (d *Daemon) lockSessionLifecycles(ids ...protocol.SessionID) (unlock func()) {
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

func (d *Daemon) sessionHasLiveWorker(sessionID protocol.SessionID) bool {
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

func (d *Daemon) reloadSessionAgent(sessionID protocol.SessionID) {
	sessionID = protocol.TrimID(sessionID)
	if sessionID == "" || d.ptyBackend == nil || d.store == nil {
		return
	}
	release, held := d.life.Hold("reloadSessionAgent")
	if !held {
		return
	}
	defer release()
	var complete launchCompletion
	defer func() { complete.finish() }()
	lock := d.sessionLifecycleLockFor(sessionID)
	lock.Lock()
	defer lock.Unlock()

	session := d.store.Get(sessionID)
	if session == nil {
		d.logf("reload: session %s not found (closed); skipping", sessionID)
		return
	}
	agent := session.Agent
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
	complete, err = d.executePreparedSessionReload(sessionID, opts, nil)
	if err != nil {
		d.logf("reload: %v", err)
	}
}

func (d *Daemon) reloadSessionForClient(sessionID protocol.SessionID, cols, rows int) error {
	sessionID = protocol.TrimID(sessionID)
	if sessionID == "" {
		return errors.New("session not found")
	}
	resume, err := d.reloadLiveOrPlanResume(sessionID, cols, rows)
	if err != nil || resume == nil {
		return err
	}
	if rejection := d.runSpawnPipeline(resume.msg, resume.policy); rejection != nil {
		return rejection.reason()
	}
	return nil
}

type storedIntentResume struct {
	msg    *protocol.SpawnSessionMessage
	policy internalSpawnPolicy
}

func (d *Daemon) reloadLiveOrPlanResume(sessionID protocol.SessionID, cols, rows int) (*storedIntentResume, error) {
	var complete launchCompletion
	defer func() { complete.finish() }()
	lock := d.sessionLifecycleLockFor(sessionID)
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireLiveSessionHost(sessionID); err != nil {
		return nil, err
	}
	if d.sessionTeardownInFlight(sessionID) {
		return nil, errors.New("session is closing")
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return nil, errors.New("session not found")
	}

	if d.sessionHasLiveWorker(sessionID) {
		opts, err := d.buildReloadSpawnOptions(session)
		if err != nil {
			return nil, err
		}
		complete, err = d.executePreparedSessionReload(sessionID, opts, nil)
		return nil, err
	}

	if cols <= 0 || rows <= 0 {
		return nil, errors.New("reload requires pty geometry when no live worker exists")
	}
	intent, ok := d.store.LaunchIntent(sessionID)
	if !ok {
		return nil, errors.New("no stored launch intent")
	}
	msg, policy := buildStoredIntentSpawn(session, intent, cols, rows)
	return &storedIntentResume{msg: msg, policy: policy}, nil
}

// persistReloadedConversation records the conversation a reload launches before it runs, or clears a
// stale one when the harness names it, so its report reads as this session's and opens no successor.
func (d *Daemon) persistReloadedConversation(sessionID protocol.SessionID, opts ptybackend.SpawnOptions) {
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

func (d *Daemon) executePreparedSessionReload(sessionID protocol.SessionID, opts ptybackend.SpawnOptions, pluginReload *preparedPluginReload) (launchCompletion, error) {
	var name *string
	if host := d.sessionHost(sessionID); host != "" {
		_, hostName, err := d.outpostConn(host)
		if err != nil {
			return nil, err
		}
		name = protocol.Ptr(hostName)
	}
	var agent *AgentLaunch
	if opts.Agent != protocol.AgentShellValue {
		if pluginReload != nil {
			agent = pluginReload.launch
		} else {
			session := d.store.Get(sessionID)
			var err error
			agent, err = d.buildAgentReloadLaunch(session, &opts, d.isChiefOfStaffSession(sessionID))
			if err != nil {
				return nil, err
			}
		}
	}
	launch, err := encodeLaunch(opts)
	if err != nil {
		return nil, err
	}
	agentJSON, err := encodeAgentLaunch(agent)
	if err != nil {
		return nil, err
	}
	msg := protocol.TerminalRestartMessage{Cmd: protocol.CmdTerminalRestart, HostName: name, SessionID: sessionID, TerminalID: opts.ID, Launch: launch, Agent: agentJSON}
	if opts.Agent != protocol.AgentShellValue {
		d.persistReloadedConversation(sessionID, opts)
	}
	if pluginReload != nil {
		_, err = d.restartPreparedTerminal(context.Background(), msg, opts, pluginReload)
	} else {
		_, err = Call[struct{}](context.Background(), d.router(), msg)
	}
	if err != nil {
		return nil, err
	}
	d.recordReload(sessionID, opts)
	d.announceReload(sessionID, opts)
	if agent != nil {
		return func() { d.launchCommitted(opts.ID, true, agent.Profile, agent.driverRunID()) }, nil
	}
	return nil, nil
}

func (d *Daemon) announceReload(sessionID protocol.SessionID, opts ptybackend.SpawnOptions) {
	d.publishFact(FactSessionRespawned, string(sessionID), ptyRespawn{Terminal: string(opts.ID)})
	d.logf("reload: respawned %s (agent=%s resume=%t yolo=%t)", sessionID, opts.Agent, opts.ResumeSessionID != "", opts.YoloMode)
}

func (d *Daemon) recordReload(sessionID protocol.SessionID, opts ptybackend.SpawnOptions) {
	intent := launchIntentFromSpawnOptions(opts, d.isChiefOfStaffSession(sessionID))
	intent.Asks = d.launchedWithAsks(sessionID)
	d.launchVentIntent(sessionID).applyTo(&intent)
	if prior, ok := d.store.LaunchIntent(sessionID); ok {
		intent.AutoMode = prior.AutoMode
		intent.ApprovalPolicy, intent.SandboxMode = prior.ApprovalPolicy, prior.SandboxMode
	}
	d.store.SetLaunchIntent(sessionID, intent)
}

func (d *Daemon) buildReloadSpawnOptions(session *protocol.Session) (ptybackend.SpawnOptions, error) {
	sessionID := session.ID
	terminal := d.primaryTerminal(sessionID)
	recorded, err := Call[protocol.TerminalLaunchResult](context.Background(), d.router(), protocol.TerminalLaunchMessage{Cmd: protocol.CmdTerminalLaunch, TerminalID: terminal})
	if err != nil {
		return ptybackend.SpawnOptions{}, err
	}
	if protocol.Deref(recorded.Unrecordable) {
		return d.buildReloadSpawnOptionsFromStoredIntent(session, terminal, recorded, fmt.Errorf("backend does not record launch params"))
	}
	if !d.terminalCarriesLaunch(sessionID) {
		return d.buildReloadSpawnOptionsFromStoredIntent(session, terminal, recorded, fmt.Errorf("terminal %s runs a link, not session %s's own launch", terminal, sessionID))
	}
	if recorded.ReadError != nil {
		return ptybackend.SpawnOptions{}, errors.New(*recorded.ReadError)
	}
	if recorded.Params == nil {
		return d.buildReloadSpawnOptionsFromStoredIntent(session, terminal, recorded, errors.New(protocol.Deref(recorded.Unrecorded)))
	}
	var params ptybackend.SessionLaunchParams
	if err := json.Unmarshal([]byte(*recorded.Params), &params); err != nil {
		return ptybackend.SpawnOptions{}, fmt.Errorf("decode launch params: %w", err)
	}
	if _, known, routeErr := recordedApprovalRoute(params.ApprovalRoute, params.YoloMode, params.UnattendedLaunch); routeErr != nil {
		return ptybackend.SpawnOptions{}, fmt.Errorf("read launch params: %w", routeErr)
	} else if !known {
		if intent, exists := d.store.LaunchIntent(sessionID); exists && intent.ApprovalRoute.Valid() {
			params.ApprovalRoute = intent.ApprovalRoute
		}
	}
	return d.buildReloadSpawnOptionsFromLaunchParams(session, terminal, recorded, params)
}

func (d *Daemon) buildReloadSpawnOptionsFromStoredIntent(session *protocol.Session, terminal harness.TerminalID, recorded protocol.TerminalLaunchResult, unrecorded error) (ptybackend.SpawnOptions, error) {
	intent, ok := d.store.LaunchIntent(session.ID)
	if !ok {
		return ptybackend.SpawnOptions{}, fmt.Errorf("%w; no stored launch intent exists either", unrecorded)
	}
	d.logf("reload: using stored launch intent for %s: %v", session.ID, unrecorded)
	return d.buildReloadSpawnOptionsFromLaunchParams(session, terminal, recorded, ptybackend.SessionLaunchParams{
		Recorded:         true,
		YoloMode:         intent.YoloMode,
		ApprovalRoute:    intent.ApprovalRoute,
		Executable:       intent.Executable,
		Model:            intent.Model,
		Effort:           intent.Effort,
		UnattendedLaunch: intent.UnattendedLaunch,
	})
}

func (d *Daemon) buildReloadSpawnOptionsFromLaunchParams(session *protocol.Session, terminal harness.TerminalID, recorded protocol.TerminalLaunchResult, params ptybackend.SessionLaunchParams) (ptybackend.SpawnOptions, error) {
	sessionID := session.ID
	cols, rows := uint16(80), uint16(24)
	if recorded.Cols != nil {
		cols = uint16(*recorded.Cols)
	}
	if recorded.Rows != nil {
		rows = uint16(*recorded.Rows)
	}

	agent := normalizeSpawnAgent(session.Agent)
	pluginDriver, isPlugin := d.ensurePluginRegistry().driver(session.Agent)
	if isPlugin {
		agent = pluginDriver.Agent
	}
	driver := agentdriver.Get(agent)
	if driver == nil && !isPlugin && agent != protocol.AgentShellValue {
		return ptybackend.SpawnOptions{}, fmt.Errorf("agent %q is not available", session.Agent)
	}
	resumeSessionID := agentdriver.ResolveSpawnResumeSessionID(driver, sessionID, "", d.store.GetResumeSessionID(sessionID))
	if resumeSessionID != "" {
		readiness, err := d.placedConversationReadiness(sessionID, d.sessionHost(sessionID), agent, session.ProfileID, resumeSessionID, true)
		if err != nil {
			return ptybackend.SpawnOptions{}, err
		}
		if !readiness.Ready {
			if readiness.Known {
				return ptybackend.SpawnOptions{}, fmt.Errorf("conversation %s could not be prepared for resume; check the daemon log", resumeSessionID)
			}
			d.logf("reload: resume target %s for session %s is not resumable (no transcript yet); fresh-spawning instead", resumeSessionID, sessionID)
			resumeSessionID = ""
		}
	}

	if intent, ok := d.store.LaunchIntent(sessionID); ok {
		params.Executable = intent.Executable
		params.ClaudeExecutable, params.CodexExecutable = "", ""
	}
	opts := ptybackend.SpawnOptions{
		ID:               terminal,
		CWD:              session.Directory,
		Agent:            agent,
		Label:            session.Label,
		Cols:             cols,
		Rows:             rows,
		ResumeSessionID:  resumeSessionID,
		Theme:            d.currentTerminalTheme(),
		YoloMode:         params.YoloMode,
		ApprovalRoute:    params.ApprovalRoute,
		Executable:       params.Executable,
		ClaudeExecutable: params.ClaudeExecutable,
		CodexExecutable:  params.CodexExecutable,
		Model:            params.Model,
		Effort:           params.Effort,
		LoginShellEnv:    d.cachedLoginShellEnv(),
		AutoApprove:      false,
		ContextWindowCap: d.launchContextWindowCap(sessionID, session.Agent, d.isChiefOfStaffSession(sessionID)),
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
	sessionID  protocol.SessionID
	pluginName string
	runID      string
	completed  bool
	launch     *AgentLaunch
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
	if !p.d.beginAgentDriverRun(p.sessionID, p.pluginName, p.runID) {
		return fmt.Errorf("initialize plugin driver run cursor")
	}
	p.completed = true
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

func (d *Daemon) buildAgentReloadLaunch(session *protocol.Session, opts *ptybackend.SpawnOptions, isChief bool) (*AgentLaunch, error) {
	launch, err := d.agentLaunchContext(session.ID, opts.Agent, isChief)
	if err != nil {
		return nil, err
	}
	launch.Profile, launch.Link, launch.Monitor = session.ProfileID, d.store.SessionLink(session.ID), d.monitorLaunch(session.ID)
	reg, ok := d.ensurePluginRegistry().driver(session.Agent)
	if !ok {
		return launch, nil
	}
	if err := pluginReloadCapabilityError(reg, isChief); err != nil {
		return nil, err
	}
	runID := uuid.NewString()
	params := pluginDriverSpawnParams{
		Agent:      reg.Agent,
		TerminalID: opts.ID,
		RunID:      runID,
		CWD:        session.Directory,
		Label:      session.Label,
		Yolo:       opts.YoloMode,
		Model:      opts.Model,
		Effort:     opts.Effort,
	}
	if reg.Capabilities["launch_instructions"] {
		instructions, err := d.preparePluginLaunchInstructions(session.ID, session.ProfileID, isChief,
			!reg.Capabilities["pull_request_reporting"])
		if err != nil {
			return nil, err
		}
		params.Instructions = instructions
	}
	if reg.Capabilities["auto_mode"] {
		cfg, err := d.store.GetAutoModeConfig()
		if err != nil {
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
			return nil, err
		}
		params.AutoMode = &cfg
	}
	if metadata := strings.TrimSpace(d.store.GetAgentMetadata(session.ID)); metadata != "" && json.Valid([]byte(metadata)) {
		params.Metadata = json.RawMessage(metadata)
	}
	launch.Plugin = &AgentPluginLaunch{Plugin: reg.PluginName, Resume: true, Params: params}
	return launch, nil
}

func (d *Daemon) prepareSessionReload(session *protocol.Session, opts *ptybackend.SpawnOptions, isChief bool) (*preparedPluginReload, error) {
	launch, err := d.buildAgentReloadLaunch(session, opts, isChief)
	if err != nil {
		return nil, err
	}
	prepared, err := d.prepareMachineAgent(session.ID, opts, launch, true)
	if prepared != nil {
		prepared.launch = launch
	}
	return prepared, err
}

type preparedPluginRoleReload struct {
	d         *Daemon
	sessionID protocol.SessionID
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
	complete, err := p.d.executePreparedSessionReload(p.sessionID, p.opts, p.plugin)
	p.lock.Unlock()
	complete.finish()
	return err
}

func (d *Daemon) preparePluginRoleReload(sessionID protocol.SessionID, desiredChief bool) (*preparedPluginRoleReload, bool, error) {
	session := d.store.Get(sessionID)
	if session == nil {
		return nil, false, nil
	}
	reg, registered := d.ensurePluginRegistry().driver(session.Agent)
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
	opts.ContextWindowCap = d.launchContextWindowCap(sessionID, session.Agent, desiredChief)
	pluginReload, err := d.prepareSessionReload(session, &opts, desiredChief)
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
