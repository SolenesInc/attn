package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/hostsession"
	"github.com/victorarias/attn/internal/launchenv"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

const pluginDriverConversationCapability = "conversation"

func (d *Daemon) ensureHostSessions() *hostsession.Manager {
	d.hostSessionsMu.Lock()
	defer d.hostSessionsMu.Unlock()
	if d.hostSessions == nil {
		d.hostSessions = hostsession.New(d.logf, d.handleHostEvent, d.handleHostExit)
	}
	return d.hostSessions
}

func (d *Daemon) isConversationAgent(agent string) bool {
	driver, ok := d.ensurePluginRegistry().driver(agent)
	return ok && driver.Capabilities[pluginDriverConversationCapability]
}

func (d *Daemon) isHostSession(sessionID string) bool {
	return d.ensureHostSessions().Has(sessionID)
}

// Liveness checks must ask this, never the PTY backend alone.
func (d *Daemon) liveRuntimeSessionIDs(ctx context.Context) []string {
	var ids []string
	if d.ptyBackend != nil {
		ids = append(ids, d.ptyBackend.SessionIDs(ctx)...)
	}
	return append(ids, d.ensureHostSessions().SessionIDs()...)
}

func hostSessionLogPath(sessionID string) string {
	return filepath.Join(config.DataDir(), "hosts", "log", sessionID+".log")
}

// hostSessionStateDir is under attn's data dir, never the agent's own home.
func hostSessionStateDir(sessionID string) string {
	return filepath.Join(config.DataDir(), "hosts", "state", sessionID)
}

// The host consults `ATTN_NISSE_RESUME_FILE` only when its dir is empty, so anything checking the resume file has to check this first.
func hostSessionStateDirHoldsConversation(sessionID string) bool {
	entries, err := os.ReadDir(hostSessionStateDir(sessionID))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			return true
		}
	}
	return false
}

func (d *Daemon) loginShellEnvForSpawn() []string {
	if cached := d.cachedLoginShellEnv(); len(cached) > 0 {
		return cached
	}
	shell := pty.GetUserLoginShell()
	if shell == "" {
		return nil
	}
	env, err := pty.ReadLoginShellEnv(shell)
	if err != nil {
		d.logf("host spawn: failed to capture login shell env from %s: %v", shell, err)
		return nil
	}
	return env
}

func (d *Daemon) spawnHostSession(opts ptybackend.SpawnOptions) error {
	if synced, err := agentdriver.EnsureAgentsSkillInstalled(); err != nil {
		d.logf("host spawn: failed to ensure the attn skill under ~/.agents: %v", err)
	} else if !synced {
		d.logf("host spawn: skipping user-global attn skill sync for profile %q", config.ProfileLabel())
	}
	env := pty.MergeEnvironment(os.Environ(), d.loginShellEnvForSpawn())
	env = pty.MergeEnvironment(env, opts.ExternalEnv)
	// Mirrors the PTY path's identity block (buildSpawnEnv, internal/pty/manager.go): without it a session resolves the wrong profile's `attn` and reports as nobody.
	env = launchenv.WithActiveAttnFirst(env, launchenv.ActiveAttnExecutable())
	hostEnv := []string{
		"ATTN_INSIDE_APP=1",
		"ATTN_DAEMON_MANAGED=1",
		"ATTN_SESSION_ID=" + opts.ID,
		"ATTN_AGENT=" + opts.Agent,
		"ATTN_NISSE_SESSION_ID=" + opts.ID,
		"ATTN_NISSE_SESSION_DIR=" + hostSessionStateDir(opts.ID),
		"ATTN_NISSE_CWD=" + opts.CWD,
	}
	// Passing the resume file on a revive too is what covers a host that died before writing anything.
	if resume := strings.TrimSpace(opts.ResumeConversationFile); resume != "" {
		hostEnv = append(hostEnv, "ATTN_NISSE_RESUME_FILE="+resume)
	}
	env = pty.MergeEnvironment(env, hostEnv)
	routingEnv := opts.DaemonEnv
	if len(routingEnv) == 0 {
		routingEnv = d.spawnRoutingEnv()
	}
	env = pty.MergeEnvironment(env, routingEnv)
	cwd := strings.TrimSpace(opts.ExternalCWD)
	if cwd == "" {
		cwd = opts.CWD
	}
	return d.ensureHostSessions().Spawn(hostsession.SpawnOptions{
		SessionID:    opts.ID,
		LifecycleID:  opts.LifecycleID,
		Command:      opts.ExternalCommand,
		Env:          env,
		CWD:          cwd,
		LogPath:      hostSessionLogPath(opts.ID),
		RegistryPath: hostsession.RegistryPath(config.DataDir(), opts.ID),
	})
}

func (d *Daemon) spawnSessionRuntime(req *spawnRequest, opts ptybackend.SpawnOptions) error {
	opts.DaemonEnv = d.spawnRoutingEnv()
	var err error
	if req.hasPluginDriver && req.pluginDriver.Capabilities[pluginDriverConversationCapability] {
		err = d.spawnHostSession(opts)
	} else {
		err = d.ptyBackend.Spawn(context.Background(), opts)
	}
	if err == nil {
		d.sessionInputs().forgetSession(opts.ID)
	}
	return err
}

func (d *Daemon) killSessionRuntime(sessionID string) error {
	if d.isHostSession(sessionID) {
		if err := d.ensureHostSessions().Kill(sessionID); err != nil && !errors.Is(err, hostsession.ErrNotFound) {
			return err
		}
		return nil
	}
	return d.ptyBackend.Kill(context.Background(), sessionID, syscall.SIGTERM)
}

func (d *Daemon) removeSessionRuntime(sessionID string) error {
	if d.isHostSession(sessionID) {
		return nil
	}
	return d.ptyBackend.Remove(context.Background(), sessionID)
}

// A tool boundary is deliberately absent: applyState restamps `state_since`, so re-applying `working` per tool call would reset "working for 4m".
var hostStateDeclarationKinds = map[string]bool{
	"session_ready": true,
	"run_started":   true,
	"run_settled":   true,
}

func (d *Daemon) handleHostEvent(event hostsession.Event) {
	d.wsHub.BroadcastValue(&protocol.AgentEventMessage{
		Event: protocol.EventAgentEvent,
		ID:    event.SessionID,
		Seq:   event.Seq,
		Kind:  event.Kind,
		Body:  event.Body,
	})
	if hostStateDeclarationKinds[event.Kind] && event.Seq > 0 {
		d.applyHostDeclaredState(event)
	}
	if event.Kind == "model_changed" && event.Seq > 0 {
		d.handleHostModelChanged(event)
	}
	if event.Kind == "input_taken" && event.Seq > 0 {
		inputID, _ := event.Body["input_id"].(string)
		active := d.store.GetAgentDriverRun(event.SessionID)
		if strings.TrimSpace(inputID) != "" && active.RunID == event.LifecycleID {
			d.observeStructuredInputTaken(event.SessionID, strings.TrimSpace(inputID), time.Now())
		}
	}
}

// Not a state declaration: applyState restamps `state_since`, and a model switch does not move the session.
func (d *Daemon) handleHostModelChanged(event hostsession.Event) {
	model, _ := event.Body["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	intent, ok := d.store.LaunchIntent(event.SessionID)
	if !ok || intent.Model == model {
		return
	}
	intent.Model = model
	d.store.SetLaunchIntent(event.SessionID, intent)
}

func (d *Daemon) applyHostDeclaredState(event hostsession.Event) {
	state, ok := event.Body["state"].(string)
	if !ok {
		d.logf("host session %s: %s declaration carries no state", event.SessionID, event.Kind)
		return
	}
	state = strings.TrimSpace(state)
	params := pluginReportStateParams{
		SessionID: event.SessionID,
		RunID:     event.LifecycleID,
		Seq:       uint64(event.Seq),
		State:     state,
	}
	if err := validatePluginReportedState(params); err != nil {
		d.logf("host session %s: rejected %s declaration: %v", event.SessionID, event.Kind, err)
		return
	}
	// `session_ready` regularly beats the spawn's own commit, which opens the run cursor; queue it there or the session's first state is lost.
	if d.queueHostReportDuringLaunch(event.SessionID, params) {
		return
	}
	d.applyPluginReportedState(params)
}

func (d *Daemon) handleHostExit(info hostsession.ExitInfo) {
	if !d.handlePTYExit(ptybackend.ExitInfo{
		ID:          info.SessionID,
		ExitCode:    info.ExitCode,
		Signal:      info.Signal,
		LifecycleID: info.LifecycleID,
	}) {
		return
	}
	if d.store.Get(info.SessionID) == nil {
		return
	}
	d.applyState(sessionStateChange{
		sessionID: info.SessionID,
		state:     string(protocol.SessionStateRecoverable),
		cause:     hostExitRecovery{},
	})
}

func (d *Daemon) reloadConversationSession(session *protocol.Session) error {
	sessionID := session.ID
	intent, ok := d.store.LaunchIntent(sessionID)
	if !ok {
		return errors.New("no stored launch intent")
	}
	killed := false
	if d.isHostSession(sessionID) {
		d.markReloading(sessionID)
		if err := d.ensureHostSessions().Kill(sessionID); err != nil && !errors.Is(err, hostsession.ErrNotFound) {
			d.clearReloading(sessionID)
			return err
		}
		killed = true
		d.closePluginDriverSession(sessionID, "reloaded", nil, "")
	}
	spawnMsg, policy := buildStoredIntentSpawn(session, intent, 80, 24)
	if rejection := d.runSpawnPipeline(spawnMsg, policy); rejection != nil {
		d.clearReloading(sessionID)
		if killed {
			d.handleHostExit(hostsession.ExitInfo{SessionID: sessionID, ExitCode: 1})
		}
		return rejection.reason()
	}
	time.AfterFunc(reloadStuckFlagGrace, func() { d.clearReloading(sessionID) })
	d.publishFact(FactSessionRespawned, sessionID, nil)
	return nil
}

func (d *Daemon) deliverToHostSessionWithInput(sessionID string, how hostsession.Delivery, text, inputID string) error {
	return d.ensureHostSessions().DeliverWithInput(sessionID, how, text, inputID)
}

// An absent or unknown mode is a plain prompt: what every client before this protocol
// version meant, and what the composer means when no run is open.
func hostDeliveryFor(mode string) hostsession.Delivery {
	switch hostsession.Delivery(strings.TrimSpace(mode)) {
	case hostsession.DeliverySteer:
		return hostsession.DeliverySteer
	case hostsession.DeliveryFollowUp:
		return hostsession.DeliveryFollowUp
	default:
		return hostsession.DeliveryPrompt
	}
}

func (d *Daemon) handleAgentPrompt(client *wsClient, msg *protocol.AgentPromptMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	inputID := strings.TrimSpace(msg.InputID)
	text := strings.TrimSpace(msg.Text)
	if sessionID == "" {
		d.sendCommandError(client, protocol.CmdAgentPrompt, "agent_prompt is missing a session id")
		return
	}
	if inputID == "" {
		d.sendCommandError(client, protocol.CmdAgentPrompt, "agent_prompt is missing an input id")
		return
	}
	if text == "" {
		d.sendCommandError(client, protocol.CmdAgentPrompt, "agent_prompt is missing text")
		return
	}
	how := hostDeliveryFor(protocol.Deref(msg.Mode))
	delivery := userConversationSessionInput(inputID, sessionID, text, sessionInputAtTurnBoundary)
	delivery.hostDelivery = how
	attempt := d.sessionInputs().try(context.Background(), delivery)
	if attempt.err != nil {
		d.logf("agent_prompt (%s) for session %s failed: %v", how, sessionID, attempt.err)
		d.sendCommandError(client, protocol.CmdAgentPrompt, "no live conversation host for session "+sessionID)
		if how != hostsession.DeliveryPrompt {
			return
		}
		d.handleHostEvent(hostsession.Event{
			SessionID: sessionID,
			Kind:      "run_settled",
			Body:      map[string]interface{}{"error": "this conversation's agent is no longer running"},
		})
		return
	}
	d.sessionInputs().release(sessionID, delivery.id)
}

func (d *Daemon) handleAgentToolDetail(client *wsClient, msg *protocol.AgentToolDetailMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	callID := strings.TrimSpace(msg.CallID)
	if sessionID == "" || callID == "" {
		d.sendCommandError(client, protocol.CmdAgentToolDetail, "agent_tool_detail needs a session id and a call id")
		return
	}
	if err := d.ensureHostSessions().ToolDetail(sessionID, callID, protocol.Deref(msg.Full)); err != nil {
		d.logf("agent_tool_detail for session %s call %s failed: %v", sessionID, callID, err)
		d.sendCommandError(client, protocol.CmdAgentToolDetail, "no live conversation host for session "+sessionID)
	}
}

func (d *Daemon) handleAgentAttach(client *wsClient, msg *protocol.AgentAttachMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	if sessionID == "" {
		d.sendCommandError(client, protocol.CmdAgentAttach, "agent_attach is missing a session id")
		return
	}
	if err := d.ensureHostSessions().Snapshot(sessionID); err != nil {
		d.logf("agent_attach for session %s failed: %v", sessionID, err)
		d.sendCommandError(client, protocol.CmdAgentAttach, "no live conversation host for session "+sessionID)
	}
}

func (d *Daemon) handleAgentHistory(client *wsClient, msg *protocol.AgentHistoryMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	before := strings.TrimSpace(msg.Before)
	if sessionID == "" || before == "" {
		d.sendCommandError(client, protocol.CmdAgentHistory, "agent_history needs a session id and a before cursor")
		return
	}
	if err := d.ensureHostSessions().History(sessionID, before); err != nil {
		d.logf("agent_history for session %s before %s failed: %v", sessionID, before, err)
		d.sendCommandError(client, protocol.CmdAgentHistory, "no live conversation host for session "+sessionID)
	}
}

func (d *Daemon) handleAgentSetModel(client *wsClient, msg *protocol.AgentSetModelMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	model := strings.TrimSpace(msg.Model)
	if sessionID == "" || model == "" {
		d.sendCommandError(client, protocol.CmdAgentSetModel, "agent_set_model needs a session id and a model")
		return
	}
	if err := d.ensureHostSessions().SetModel(sessionID, model); err != nil {
		d.logf("agent_set_model for session %s to %s failed: %v", sessionID, model, err)
		d.sendCommandError(client, protocol.CmdAgentSetModel, "no live conversation host for session "+sessionID)
	}
}

func (d *Daemon) handleAgentClearQueue(client *wsClient, msg *protocol.AgentClearQueueMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	if sessionID == "" {
		d.sendCommandError(client, protocol.CmdAgentClearQueue, "agent_clear_queue is missing a session id")
		return
	}
	if err := d.ensureHostSessions().ClearQueue(sessionID); err != nil {
		d.logf("agent_clear_queue for session %s failed: %v", sessionID, err)
		d.sendCommandError(client, protocol.CmdAgentClearQueue, "no live conversation host for session "+sessionID)
	}
}
