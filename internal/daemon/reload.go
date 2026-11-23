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

func recordedLegacyExecutable(agent string, params ptybackend.SessionLaunchParams) string {
	switch agent {
	case "claude":
		return params.ClaudeExecutable
	case "codex":
		return params.CodexExecutable
	}
	return ""
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

func (d *Daemon) agentSupportsChiefReload(agent string) bool {
	options := d.factsOrNone(agent).Options
	return options.LaunchInstructions && options.Resume
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
	complete, err = d.executePreparedSessionReload(sessionID, opts)
	if err != nil {
		d.logf("reload: %v", err)
	}
}

func (d *Daemon) reloadSession(sessionID protocol.SessionID, cols, rows int) error {
	sessionID = protocol.TrimID(sessionID)
	if sessionID == "" {
		return errors.New("session not found")
	}
	if session := d.store.Get(sessionID); session != nil {
		d.awaitHarness(session.Agent)
	}
	resume, err := d.reloadLiveOrPlanResume(sessionID, cols, rows)
	if err != nil || resume == nil {
		return err
	}
	return d.runStoredIntentSpawn(resume.msg, resume.policy)
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
		complete, err = d.executePreparedSessionReload(sessionID, opts)
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
	if owed := d.sessionMonitor(sessionID).OwedInitialPrompt(); owed != "" {
		msg.InitialPrompt = protocol.Ptr(owed)
	}
	return &storedIntentResume{msg: msg, policy: policy}, nil
}

// ptyRespawn names the terminal whose process a respawn replaced; the fact's subject is its session.
type ptyRespawn struct {
	Terminal string `json:"terminal,omitempty"`
}

func (d *Daemon) handleReloadSession(client *wsClient, msg *protocol.ReloadSessionMessage) {
	err := d.reloadSession(msg.ID, msg.Cols, msg.Rows)
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

func (d *Daemon) executePreparedSessionReload(sessionID protocol.SessionID, opts ptybackend.SpawnOptions) (launchCompletion, error) {
	var name *string
	if host := d.sessionHost(sessionID); host != "" {
		_, hostName, err := d.outpostConn(host)
		if err != nil {
			return nil, err
		}
		name = protocol.Ptr(hostName)
	}
	var agent *AgentLaunch
	var planned harness.Planned
	if opts.Agent != protocol.AgentShellValue {
		var err error
		planned, err = d.harnessOf(opts.Agent).Plan(harness.LaunchRequest{Reason: harness.Reload, Session: sessionID, Terminal: opts.ID, Lifetime: d.lifetime(sessionID), Conversation: opts.ResumeSessionID, Picker: opts.ResumePicker})
		if err != nil {
			return nil, err
		}
		opts = withLifecycleID(opts)
		agent, err = d.buildAgentReloadLaunch(d.store.Get(sessionID), opts)
		if err != nil {
			return nil, err
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
	running, err := Call[protocol.TerminalLaunchResult](context.Background(), d.router(), protocol.TerminalLaunchMessage{Cmd: protocol.CmdTerminalLaunch, TerminalID: opts.ID})
	if err != nil {
		return nil, err
	}
	stillRunning := d.monitors().replacingRun(sessionID, protocol.Deref(running.LifecycleID))
	if opts.Agent != protocol.AgentShellValue {
		d.recordLaunchConversation(sessionID, planned, "reload")
	}
	if _, err = Call[struct{}](context.Background(), d.router(), msg); err != nil {
		stillRunning()
		return nil, err
	}
	d.recordReload(sessionID, opts)
	d.announceReload(sessionID, opts)
	if agent != nil {
		return func() { d.launchCommitted(opts.ID, true, agent.Profile, agent.runOf(opts)) }, nil
	}
	return nil, nil
}

func (d *Daemon) announceReload(sessionID protocol.SessionID, opts ptybackend.SpawnOptions) {
	d.publishFact(FactSessionRespawned, string(sessionID), ptyRespawn{Terminal: string(opts.ID)})
	d.logf("reload: respawned %s (agent=%s resume=%t yolo=%t)", sessionID, opts.Agent, opts.ResumeSessionID != "", opts.YoloMode)
}

func (d *Daemon) recordReload(sessionID protocol.SessionID, opts ptybackend.SpawnOptions) {
	intent := launchIntentFromSpawnOptions(opts)
	intent.Asks = d.launchedWithAsks(sessionID)
	d.launchVentIntent(sessionID).applyTo(&intent)
	if prior, ok := d.store.LaunchIntent(sessionID); ok {
		intent.Command = prior.Command
	}
	if err := d.store.SetLaunchIntent(sessionID, intent); err != nil {
		d.logf("save launch intent: %v", err)
	}
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

	agent := harnessID(session.Agent)
	if agent == "" {
		return ptybackend.SpawnOptions{}, fmt.Errorf("session %s names no harness to reload", sessionID)
	}
	if agent != protocol.AgentShellValue {
		h, err := d.harness(session.Agent)
		if err != nil {
			return ptybackend.SpawnOptions{}, fmt.Errorf("agent %q is not available", session.Agent)
		}
		agent = h.Facts().ID
	}
	resumeSessionID := d.resumeTarget(agent, sessionID, "")
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

	executionTarget := ""
	if intent, ok := d.store.LaunchIntent(sessionID); ok {
		params.Executable = intent.Executable
		executionTarget = intent.ExecutionTarget
	} else if params.Executable == "" {
		params.Executable = recordedLegacyExecutable(agent, params)
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
		Model:            params.Model,
		Effort:           params.Effort,
		ExecutionTarget:  executionTarget,
		AutoApprove:      false,
		ContextWindowCap: d.launchContextWindowCap(sessionID, session.Agent),
	}
	if intent, ok := d.store.LaunchIntent(sessionID); ok && intent.Command != nil {
		opts.AgentArgv = append([]string(nil), intent.Command.Argv...)
	}
	if member, bound := d.crewMemberForSession(sessionID); bound && strings.EqualFold(strings.TrimSpace(session.Agent), member.Harness()) {
		opts.Model = protocol.Deref(d.crewStartModel(member, session.Agent))
		opts.Effort = protocol.Deref(d.crewStartEffort(member, session.Agent))
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

func (d *Daemon) buildAgentReloadLaunch(session *protocol.Session, opts ptybackend.SpawnOptions) (*AgentLaunch, error) {
	launch, err := d.agentLaunchContext(session.ID, opts.Agent)
	if err != nil {
		return nil, err
	}
	launch.Profile, launch.Lifetime, launch.Monitor = session.ProfileID, d.lifetime(session.ID), d.monitorLaunch(session.ID)
	launch.InitialPrompt = d.sessionMonitor(session.ID).OwedInitialPrompt()
	return launch, nil
}
