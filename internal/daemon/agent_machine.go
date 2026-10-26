package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/sessionstate"
)

type AgentLaunch struct {
	Context       hooks.Launch
	AwarenessDirs []string
	Direct        bool
	InitialPrompt string
	Link          string
	PreviousLink  string
	Profile       string
	Plugin        *AgentPluginLaunch
	Monitor       sessionmonitor.Launch
	AddedTerminal bool
	FreshCost     bool
	StartedAt     time.Time
}

type AgentPluginLaunch struct {
	Plugin string
	Resume bool
	Params pluginDriverSpawnParams
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

func (d *Daemon) agentLaunchContext(sessionID protocol.SessionID, agent string, chief bool) (*AgentLaunch, error) {
	caps := agentdriver.EffectiveCapabilities(agentdriver.Get(agent))
	launch, awareness, err := d.prepareLaunchContext(sessionID, chief && caps.HasLaunchInstructions, !caps.HasHooks)
	if err != nil {
		return nil, err
	}
	return &AgentLaunch{Context: launch, AwarenessDirs: awareness, Direct: parseBooleanSetting(d.homeSetting(SettingDirectAgentLaunchEnabled))}, nil
}

func (d *Daemon) prepareMachineAgent(sessionID protocol.SessionID, opts *ptybackend.SpawnOptions, launch *AgentLaunch, reload bool) (*preparedPluginReload, error) {
	if launch == nil {
		return nil, nil
	}
	if launch.Plugin != nil {
		p := launch.Plugin
		reg, ok := d.ensurePluginRegistry().driver(opts.Agent)
		if !ok || reg.PluginName != p.Plugin {
			return nil, fmt.Errorf("agent %q is not available on this machine", opts.Agent)
		}
		d.sessionMonitor(sessionID).BeginDriverLaunch(p.Plugin, p.Params.RunID)
		prepared := &preparedPluginReload{d: d, sessionID: sessionID, pluginName: p.Plugin, runID: p.Params.RunID}
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
		opts.LifecycleID, opts.AgentArgv, opts.AgentEnv, opts.ExternalCWD = p.Params.RunID, append([]string(nil), result.Argv...), env, externalCWD
		if reload {
			opts.ResumeSessionID = ""
		}
		return prepared, nil
	}
	driver := agentdriver.Get(opts.Agent)
	if driver == nil {
		return nil, fmt.Errorf("unknown agent %q", opts.Agent)
	}
	if opts.LaunchExecutable == "" && configuredLaunchExecutable(driver, opts) == "" {
		opts.LaunchExecutable = strings.TrimSpace(d.machineSetting(MachineSetting(executableSettingKey(opts.Agent))))
	}
	if l := d.linkNamed(launch.Link); l != nil {
		if err := l.admit(launch.Profile, opts.ResumeSessionID); err != nil {
			return nil, err
		}
		if opts.ResumeSessionID != "" {
			if err := l.restore(launch.Profile, opts.ResumeSessionID); err != nil {
				return nil, err
			}
		}
		if reload {
			l.relaunch(opts)
		} else if err := l.prepareLaunch(opts, launch.Profile); err != nil {
			return nil, err
		}
	}
	if err := d.prepareAgentLaunch(opts, launch); err != nil {
		return nil, fmt.Errorf("prepare %s launch: %w", opts.Agent, err)
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
	fresh.InitialPromptOwed = strings.TrimSpace(launch.InitialPrompt) != "" && sessionmonitor.ReportsTurnStarts(opts.Agent)
	seed := d.monitorLaunch(session.ID)
	if !d.store.HasSessionRuntime(session.ID) {
		seed = launch.Monitor
	}
	monitor := d.startSessionMonitor(session.ID)
	monitor.Restart(withSeed(seed, session), fresh)
	monitor.Launched(launch.StartedAt)
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
	if pending, ok := d.consumePendingAgentConversation(sessionID); ok {
		d.observeAgentConversation(pending)
	}
}

func (d *Daemon) commitAgentLaunch(_ context.Context, msg protocol.LaunchCommittedMessage) (struct{}, error) {
	sessionID, ok := d.shownIn(msg.TerminalID)
	if !ok {
		return struct{}{}, nil
	}
	if !msg.Committed {
		if session := d.store.Get(sessionID); session != nil {
			d.abortMachineAgent(sessionID, msg.TerminalID, session.ProfileID)
		}
		d.terminals().unexpect(msg.TerminalID)
		d.kickMonitorTerminals()
		return struct{}{}, nil
	}
	if exit := d.finishPluginSessionLaunch(sessionID, true); exit != nil {
		d.life.Go("handlePTYExitAfterPluginLaunch", func() { d.handlePTYExit(*exit) })
	}
	return struct{}{}, nil
}

func (d *Daemon) abortMachineAgent(sessionID protocol.SessionID, terminal harness.TerminalID, profile string) {
	d.sessionMonitor(sessionID).ForgetInitialPrompt()
	d.abortPluginSessionLaunch(sessionID, "launch_failed")
	if l := d.linkOf(sessionID); l != nil {
		l.launchFailed(terminal, profile)
	}
}

func (d *Daemon) launchCommitted(terminal harness.TerminalID, committed bool) {
	_, err := Call[struct{}](context.Background(), d.router(), protocol.LaunchCommittedMessage{Cmd: protocol.CmdLaunchCommitted, TerminalID: terminal, Committed: committed})
	if err != nil {
		d.logf("launch_committed: terminal=%s committed=%t: %v", terminal, committed, err)
	}
}

func (d *Daemon) readConversationReadiness(_ context.Context, msg protocol.ConversationReadinessMessage) (protocol.ConversationReadinessResult, error) {
	driver := agentdriver.Get(msg.Agent)
	if driver == nil {
		return protocol.ConversationReadinessResult{}, fmt.Errorf("unknown agent %q", msg.Agent)
	}
	if msg.Restore {
		if l := d.linkNamed(msg.Link); l != nil {
			if err := l.restore(msg.Profile, msg.Conversation); err != nil {
				return protocol.ConversationReadinessResult{}, err
			}
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

func (d *Daemon) readAgentCatalog(_ context.Context, _ protocol.AgentCatalogMessage) (protocol.AgentCatalogResult, error) {
	name, err := os.Hostname()
	if err != nil {
		return protocol.AgentCatalogResult{}, err
	}
	return protocol.AgentCatalogResult{Machine: name, Harnesses: d.delegationHarnesses()}, nil
}

func (d *Daemon) requireCatalogAgent(host, agent, explicit string) error {
	catalog, err := Call[protocol.AgentCatalogResult](context.Background(), d.router(), protocol.AgentCatalogMessage{Cmd: protocol.CmdAgentCatalog, Host: host})
	if err != nil {
		return err
	}
	for _, h := range catalog.Harnesses {
		if h.ID == agent && (h.Available || explicit != "") {
			return nil
		}
	}
	return fmt.Errorf("agent %q is not available on %s", agent, catalog.Machine)
}

func (d *Daemon) machineAgentExecutable(driver agentdriver.Driver) string {
	env := pty.MergeEnvironment(os.Environ(), d.cachedLoginShellEnv())
	name := strings.TrimSpace(d.machineSetting(MachineSetting(executableSettingKey(driver.Name()))))
	if name == "" {
		name, _ = pty.LookupEnv(env, driver.ExecutableEnvVar())
		name = strings.TrimSpace(name)
	}
	if name == "" {
		name = driver.DefaultExecutable()
	}
	if path, ok := pty.ResolveCommandPath(name, env); ok {
		return path
	}
	return name
}
