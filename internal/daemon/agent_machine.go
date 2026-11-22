package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
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
	Link                 string
	PreviousLink         string
	Profile              string
	Plugin               *AgentPluginLaunch
	Monitor              sessionmonitor.Launch
	InstallWorkflowSkill bool
	RequireWorkflowSkill bool
	AddedTerminal        bool
	FreshCost            bool
	StartedAt            time.Time
	RepositoryTrust      *RepositoryTrust
}

type RepositoryTrust struct {
	KnownOrigins []string
}

type AgentPluginLaunch struct {
	Plugin             string
	Resume             bool
	Params             pluginDriverSpawnParams
	LaunchInstructions bool
}

func (launch *AgentLaunch) driverRunID() string {
	if launch == nil || launch.Plugin == nil || launch.Link != "" {
		return ""
	}
	return launch.Plugin.Params.RunID
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

func (d *Daemon) prepareMachineAgent(sessionID protocol.SessionID, opts *ptybackend.SpawnOptions, launch *AgentLaunch, reload bool) (*preparedPluginReload, error) {
	if launch == nil {
		return nil, nil
	}
	if launch.InstallWorkflowSkill {
		if err := d.ensureDelegationWorkflowSkill(opts.Agent, launch.RequireWorkflowSkill); err != nil {
			return nil, err
		}
	}
	if launch.Plugin != nil {
		p := launch.Plugin
		reg, ok := d.ensurePluginRegistry().driver(opts.Agent)
		if !ok || reg.PluginName != p.Plugin {
			return nil, fmt.Errorf("agent %q is not available on this machine", opts.Agent)
		}
		p.Params.Executable = configuredLaunchExecutable(nil, opts)
		if p.Params.Executable == "" {
			p.Params.Executable = strings.TrimSpace(d.setting(executableSetting(opts.Agent)))
		}
		serverBound := reg.Capabilities["server_binding"]
		var prepared *preparedPluginReload
		p.Params.AgentTools = nil
		if serverBound {
			l := d.serverLinkFor(opts.Agent)
			p.Params.Executable = l.executable(opts)
			if err := d.prepareLinkedLaunch(opts, launch, reload); err != nil {
				return nil, err
			}
			if reg.Capabilities["agent_tools"] {
				if tools := l.clientAgentTools(); tools != nil {
					p.Params.AgentTools = &pluginAgentTools{Name: tools.Name, Description: tools.Description}
				}
			}
		} else {
			if reg.Capabilities["agent_tools"] {
				p.Params.AgentTools = d.pluginAgentTools(opts.ID)
			}
			d.sessionMonitor(sessionID).BeginLaunch(p.Plugin, p.Params.RunID)
			prepared = &preparedPluginReload{d: d, sessionID: sessionID, pluginName: p.Plugin, runID: p.Params.RunID, route: pluginDriverRoute(reg)}
		}
		if p.LaunchInstructions {
			p.Params.Instructions = renderPluginLaunchInstructions(launch.Context, launch.Profile, p.Params.AgentTools != nil)
		}
		result, err := d.resolvePluginDriverLaunch(reg, p.Params, p.Resume)
		if err != nil {
			prepared.abort()
			return nil, err
		}
		env, err := pluginCommandEnv(result.Env)
		if err != nil {
			prepared.abort()
			return nil, err
		}
		externalCWD := strings.TrimSpace(result.CWD)
		if externalCWD != "" {
			externalCWD, err = d.validateCrewBoundLaunchDir(sessionID, externalCWD)
			if err != nil {
				prepared.abort()
				return nil, err
			}
		}
		if serverBound {
			l := d.serverLinkFor(opts.Agent)
			env = append(env, l.homeEnv())
		}
		opts.LifecycleID, opts.AgentArgv, opts.AgentEnv, opts.ExternalCWD = p.Params.RunID, append([]string(nil), result.Argv...), env, externalCWD
		if reload && !serverBound {
			opts.ResumeSessionID = ""
		}
		return prepared, nil
	}
	driver := agentdriver.Get(opts.Agent)
	if driver == nil {
		return nil, fmt.Errorf("unknown agent %q", opts.Agent)
	}
	if opts.LaunchExecutable == "" && configuredLaunchExecutable(driver, opts) == "" {
		opts.LaunchExecutable = strings.TrimSpace(d.setting(executableSetting(opts.Agent)))
	}
	if err := d.refuseUnknownCodexFolder(opts, launch.Profile); err != nil {
		return nil, err
	}
	if err := d.settleClaudeFolderTrust(opts, launch.RepositoryTrust); err != nil {
		return nil, err
	}
	if err := d.prepareLinkedLaunch(opts, launch, reload); err != nil {
		return nil, err
	}
	if err := d.prepareAgentLaunch(opts, launch); err != nil {
		return nil, fmt.Errorf("prepare %s launch: %w", opts.Agent, err)
	}
	if l, server := d.linkNamed(launch.Link).(*serverLink); server {
		opts.AgentEnv = append(opts.AgentEnv, l.homeEnv())
	}
	return nil, nil
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
	if _, serverView := d.linkNamed(launch.Link).(*serverLink); serverView && opts.ResumeSessionID != "" {
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
	if launch.Link != "" {
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
			for _, link := range d.links() {
				link.profileReleased(msg.ProfileID)
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

func (d *Daemon) readConversationReadiness(_ context.Context, msg protocol.ConversationReadinessMessage) (protocol.ConversationReadinessResult, error) {
	if l := d.serverLinkFor(msg.Agent); l != nil {
		return protocol.ConversationReadinessResult{Known: msg.Conversation != "", Ready: msg.Conversation != ""}, nil
	}
	driver := agentdriver.Get(msg.Agent)
	if driver == nil {
		return protocol.ConversationReadinessResult{}, fmt.Errorf("unknown agent %q", msg.Agent)
	}
	if msg.Restore {
		if err := d.restoreLinkedConversation(msg.Link, msg.Profile, msg.Conversation); err != nil {
			return protocol.ConversationReadinessResult{}, err
		}
	}
	result := protocol.ConversationReadinessResult{Known: d.conversationKnown(driver, msg.Conversation)}
	if msg.Restore {
		result.Ready = d.conversationReady(driver, msg.Conversation)
	}
	return result, nil
}

func (d *Daemon) placedConversationReadiness(session protocol.SessionID, host, agent, profile, conversation string, restore bool) (protocol.ConversationReadinessResult, error) {
	return Call[protocol.ConversationReadinessResult](context.Background(), d.router(), protocol.ConversationReadinessMessage{Cmd: protocol.CmdConversationReadiness, Host: host, Agent: agent, Conversation: conversation, Restore: restore, Profile: profile, Link: d.store.SessionLink(session)})
}

func (d *Daemon) readAgentCatalog(ctx context.Context, msg protocol.AgentCatalogMessage) (protocol.AgentCatalogResult, error) {
	name, err := os.Hostname()
	if err != nil {
		return protocol.AgentCatalogResult{}, err
	}
	result := protocol.AgentCatalogResult{Machine: name, Harnesses: d.delegationHarnesses()}
	if agent := protocol.Deref(msg.ModelsFor); agent != "" {
		catalog, err := d.discoverHarnessModels(ctx, agent)
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
	if listed := i >= 0; listed && (catalog.Harnesses[i].Available || explicit != "") || !listed && !startsSession {
		return nil
	}
	return fmt.Errorf("agent %q is not available on %s", agent, catalog.Machine)
}

func (d *Daemon) prepareLinkedLaunch(opts *ptybackend.SpawnOptions, launch *AgentLaunch, reload bool) error {
	l := d.linkNamed(launch.Link)
	if l != nil {
		if err := l.admit(launch.Profile, opts.ResumeSessionID); err != nil {
			return err
		}
		if opts.ResumeSessionID != "" {
			if err := d.restoreLinkedConversation(launch.Link, launch.Profile, opts.ResumeSessionID); err != nil {
				return err
			}
		}
		if reload {
			if err := l.relaunch(opts); err != nil {
				return err
			}
		} else if err := l.prepareLaunch(opts, launch.Profile); err != nil {
			return err
		}
	}
	return nil
}
