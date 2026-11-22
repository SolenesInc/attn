package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/setting"
)

type AgentLaunch struct {
	Context              hooks.Launch
	AwarenessDirs        []string
	Direct               bool
	InitialPrompt        string
	Lifetime             harness.Lifetime
	PreviousLifetime     harness.Lifetime
	Profile              string
	Monitor              sessionmonitor.Launch
	InstallWorkflowSkill bool
	RequireWorkflowSkill bool
	AddedTerminal        bool
	FreshCost            bool
	StartedAt            time.Time
	RepositoryTrust      *harness.RepositoryTrust
}

func encodeAgentLaunch(launch *AgentLaunch) (*string, error) {
	if launch == nil {
		return nil, nil
	}
	raw, err := json.Marshal(launch)
	return protocol.Ptr(string(raw)), err
}

func decodeAgentLaunch(raw *string) (*AgentLaunch, error) {
	if raw == nil {
		return nil, nil
	}
	var launch AgentLaunch
	if err := json.Unmarshal([]byte(*raw), &launch); err != nil {
		return nil, fmt.Errorf("decode agent launch: %w", err)
	}
	return &launch, nil
}

func (d *Daemon) agentLaunchContext(sessionID protocol.SessionID, agent string) (*AgentLaunch, error) {
	launch, awareness, err := d.prepareLaunchContext(sessionID, !d.signalsOf(protocol.SessionAgent(agent)).ReportsTools)
	if err != nil {
		return nil, err
	}
	return &AgentLaunch{Context: launch, AwarenessDirs: awareness, Direct: d.on(setting.DirectAgentLaunchEnabled)}, nil
}

func (d *Daemon) prepareMachineAgent(sessionID protocol.SessionID, opts *ptybackend.SpawnOptions, launch *AgentLaunch, reload bool) error {
	if launch == nil {
		return nil
	}
	if launch.InstallWorkflowSkill {
		if err := d.ensureDelegationWorkflowSkill(opts.Agent, launch.RequireWorkflowSkill); err != nil {
			return err
		}
	}
	req := launchRequestOf(sessionID, *opts, launch, reload)
	command, err := d.harnessOf(opts.Agent).Launch(d.life.Context(), req)
	if err != nil {
		d.abortLaunch(sessionID)
		return err
	}
	if command.Dir != "" {
		if command.Dir, err = d.validateCrewBoundLaunchDir(sessionID, command.Dir); err != nil {
			d.abortLaunch(sessionID)
			return err
		}
	}
	opts.AgentArgv, opts.AgentEnv, opts.ExternalCWD = command.Argv, command.Env, command.Dir
	opts.AgentLoginShell, opts.LaunchExecutable, opts.TitleSignals = command.LoginShell, command.Executable, command.TitleSignals
	return nil
}

func launchRequestOf(sessionID protocol.SessionID, opts ptybackend.SpawnOptions, launch *AgentLaunch, reload bool) harness.LaunchRequest {
	req := harness.LaunchRequest{
		Reason: harness.Start, Session: sessionID, Terminal: opts.ID, Lifetime: launch.Lifetime,
		Profile: launch.Profile, Dir: opts.CWD, Label: opts.Label, Cols: opts.Cols, Rows: opts.Rows,
		Conversation: opts.ResumeSessionID, Picker: opts.ResumePicker, InitialPrompt: launch.InitialPrompt,
		Options: harness.LaunchOptions{
			Yolo: opts.YoloMode, AutoApprove: opts.AutoApprove, Model: opts.Model, Effort: opts.Effort,
			Target: opts.ExecutionTarget, ContextCap: opts.ContextWindowCap,
		},
		Executable: opts.Executable, Unattended: opts.UnattendedLaunch,
		Guidance: launch.Context, AwarenessDirs: launch.AwarenessDirs, Trust: launch.RepositoryTrust, Direct: launch.Direct,
		Lifecycle: opts.LifecycleID,
	}
	switch {
	case reload:
		req.Reason = harness.Reload
	case !launch.FreshCost:
		req.Reason = harness.Resume
	}
	if note := strings.TrimSpace(launch.Monitor.RunRecord.Note); note != "" && json.Valid([]byte(note)) {
		req.Note = json.RawMessage(note)
	}
	return req
}

func (d *Daemon) seedAgentMonitor(session *protocol.Session, opts ptybackend.SpawnOptions, launch *AgentLaunch) {
	fresh := sessionmonitor.Evidence{TerminalEvidence: sessionstate.TerminalEvidence{ReviewerInLoop: opts.ApprovalRoute.ReviewerInLoop()}}
	if launch == nil {
		d.startSessionMonitorSeeded(session, fresh)
		d.sessionMonitor(session.ID).RememberInitialPrompt("")
		return
	}
	if launch.AddedTerminal {
		return
	}
	fresh.InitialPromptOwed = strings.TrimSpace(launch.InitialPrompt) != "" && d.signalsOf(protocol.SessionAgent(opts.Agent)).TurnStarts == harness.TurnStartsReported
	seed := d.monitorLaunch(session.ID)
	if !d.store.HasSessionRuntime(session.ID) {
		seed = launch.Monitor
	}
	monitor := d.startSessionMonitor(session.ID)
	if d.ownedByServer(opts.Agent, launch.Lifetime) && opts.ResumeSessionID != "" {
		if monitor.Snapshot().State.V.State == protocol.SessionStateRecoverable {
			monitor.Begin(withSeed(seed, session))
		}
		monitor.RecordReviewer(opts.ApprovalRoute.ReviewerInLoop())
		monitor.RememberInitialPrompt(launch.InitialPrompt)
		return
	}
	monitor.Restart(withSeed(seed, session), fresh)
	monitor.Launched(launch.StartedAt)
	monitor.HarnessStarting(opts.Agent)
	monitor.RememberInitialPrompt(launch.InitialPrompt)
}

func (d *Daemon) agentStarted(sessionID protocol.SessionID, launch *AgentLaunch) {
	if launch == nil || launch.AddedTerminal {
		return
	}
	monitor := d.sessionMonitor(sessionID)
	d.clearExitScreenBefore(sessionID, launch.StartedAt)
	if strings.TrimSpace(launch.InitialPrompt) != "" {
		monitor.TitleFromPrompt(launch.InitialPrompt, false)
	}
	if launch.FreshCost {
		monitor.StartCost()
	}
	monitor.StartTranscriptWatcher("")
	if pending, ok := d.consumeQueuedAgentConversation(sessionID); ok {
		d.observeAgentConversation(pending)
	}
	if launch.Lifetime == harness.InServer {
		d.terminalEvent(d.primaryTerminal(sessionID), harness.Started{Session: sessionID, Prompted: strings.TrimSpace(launch.InitialPrompt) != ""})
	}
}

func (d *Daemon) commitAgentLaunch(_ context.Context, msg protocol.LaunchCommittedMessage) (struct{}, error) {
	sessionID, ok := d.shownIn(msg.TerminalID)
	if !msg.Committed {
		if ok {
			d.abortMachineAgent(sessionID, msg.TerminalID)
		} else {
			d.terminalEvent(msg.TerminalID, harness.Dropped{})
		}
		d.life.Go("releaseLinksAfterLaunchAbort", func() {
			for _, h := range d.harnesses() {
				h.Released(msg.ProfileID)
			}
		})
		d.terminals().unexpect(msg.TerminalID)
		d.kickMonitorTerminals()
		return struct{}{}, nil
	}
	if !ok {
		return struct{}{}, nil
	}
	if exit := d.sessionMonitor(sessionID).FinishLaunch(protocol.Deref(msg.RunID)); exit != nil {
		d.life.Go("handlePTYExitAfterPluginLaunch", func() { d.handlePTYExit(*exit) })
	}
	return struct{}{}, nil
}

type launchCompletion func()

func (complete launchCompletion) finish() {
	if complete != nil {
		complete()
	}
}

func (d *Daemon) abortMachineAgent(sessionID protocol.SessionID, terminal harness.TerminalID) {
	d.sessionMonitor(sessionID).ForgetInitialPrompt()
	d.abortLaunch(sessionID)
	d.terminalEvent(terminal, harness.Dropped{})
}

func (launch *AgentLaunch) runOf(opts ptybackend.SpawnOptions) string {
	if launch == nil || launch.Lifetime == harness.InServer {
		return ""
	}
	return opts.LifecycleID
}

func (d *Daemon) launchCommitted(terminal harness.TerminalID, committed bool, profileID, runID string) {
	if committed && runID != "" {
		pausepoint.At(pausepoint.AgentLaunchCommitReporting)
	}
	msg := protocol.LaunchCommittedMessage{Cmd: protocol.CmdLaunchCommitted, TerminalID: terminal, Committed: committed, ProfileID: profileID}
	if runID != "" {
		msg.RunID = protocol.Ptr(runID)
	}
	_, err := Call[struct{}](context.Background(), d.router(), msg)
	if err != nil {
		d.logf("launch_committed: terminal=%s committed=%t: %v", terminal, committed, err)
	}
}

func (d *Daemon) readConversationReadiness(ctx context.Context, msg protocol.ConversationReadinessMessage) (protocol.ConversationReadinessResult, error) {
	h, err := d.harness(msg.Agent)
	if err != nil {
		return protocol.ConversationReadinessResult{}, fmt.Errorf("unknown agent %q", msg.Agent)
	}
	if msg.Conversation == "" {
		return protocol.ConversationReadinessResult{}, nil
	}
	if h.Facts().Recovery == harness.ByServer {
		return protocol.ConversationReadinessResult{Known: true, Ready: true}, nil
	}
	ref := harness.ConversationRef{Harness: h.Facts().ID, Profile: msg.Profile, ID: msg.Conversation, Lifetime: harness.Lifetime(msg.Lifetime)}
	conversations := h.Conversations()
	if msg.Restore {
		ready, err := conversations.Prepare(ctx, ref)
		if err != nil {
			return protocol.ConversationReadinessResult{}, err
		}
		if ready {
			return protocol.ConversationReadinessResult{Known: true, Ready: true}, nil
		}
	}
	return protocol.ConversationReadinessResult{Known: conversations.Check(ctx, []harness.ConversationRef{ref})[ref].Resumable}, nil
}

func (d *Daemon) placedConversationReadiness(session protocol.SessionID, host, agent, profile, conversation string, restore bool) (protocol.ConversationReadinessResult, error) {
	return Call[protocol.ConversationReadinessResult](context.Background(), d.router(), protocol.ConversationReadinessMessage{Cmd: protocol.CmdConversationReadiness, Host: host, Agent: agent, Conversation: conversation, Restore: restore, Profile: profile, Lifetime: protocol.ConversationLifetime(d.lifetime(session))})
}

func (d *Daemon) readAgentCatalog(ctx context.Context, msg protocol.AgentCatalogMessage) (protocol.AgentCatalogResult, error) {
	name, err := os.Hostname()
	if err != nil {
		return protocol.AgentCatalogResult{}, err
	}
	result := protocol.AgentCatalogResult{Machine: name, Harnesses: d.delegationHarnesses()}
	if agent := protocol.Deref(msg.ModelsFor); agent != "" {
		catalog, err := d.harnessModels(ctx, agent, protocol.Deref(msg.RefreshModels))
		if err != nil {
			return result, err
		}
		result.Models = catalog.Models
	}
	return result, nil
}

func (d *Daemon) handleAgentCatalogWS(client *wsClient, msg *protocol.AgentCatalogMessage) {
	d.life.Go("handleAgentCatalogWS", func() {
		result := protocol.AgentCatalogResultMessage{Event: protocol.EventAgentCatalogResult, RequestID: msg.RequestID, Host: msg.Host}
		catalog, err := Call[protocol.AgentCatalogResult](context.Background(), d.router(), *msg)
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.Catalog, result.Success = &catalog, true
		}
		d.sendToClient(client, result)
	})
}

func (d *Daemon) requireCatalogAgent(host, agent, explicit string, startsSession bool) error {
	catalog, err := Call[protocol.AgentCatalogResult](context.Background(), d.router(), protocol.AgentCatalogMessage{Cmd: protocol.CmdAgentCatalog, Host: host})
	if err != nil {
		return err
	}
	i := slices.IndexFunc(catalog.Harnesses, func(h protocol.Harness) bool { return h.ID == agent })
	if listed := i >= 0; listed && (catalog.Harnesses[i].Available() || explicit != "") || !listed && !startsSession {
		return nil
	}
	return fmt.Errorf("agent %q is not available on %s", agent, catalog.Machine)
}
