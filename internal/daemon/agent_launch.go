package daemon

import (
	"fmt"
	"github.com/victorarias/attn/internal/harness"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/launchenv"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func (d *Daemon) launchSpawnOptions(req harness.LaunchRequest, agent harness.ID) ptybackend.SpawnOptions {
	opts := ptybackend.SpawnOptions{
		ID: req.Terminal, CWD: req.Dir, Agent: string(agent), Label: req.Label, Cols: req.Cols, Rows: req.Rows,
		ResumeSessionID: req.Conversation, ResumePicker: req.Picker,
		YoloMode: req.Options.Yolo, AutoApprove: req.Options.AutoApprove, Model: req.Options.Model, Effort: req.Options.Effort,
		ExecutionTarget: req.Options.Target, ContextWindowCap: req.Options.ContextCap,
		Executable: req.Executable, UnattendedLaunch: req.Unattended,
	}
	if configuredLaunchExecutable(&opts) == "" {
		opts.LaunchExecutable = strings.TrimSpace(d.setting(executableSetting(opts.Agent)))
	}
	return opts
}

func (d *Daemon) launchWithDriver(req harness.LaunchRequest, opts *ptybackend.SpawnOptions, server bool, shared *agentdriver.SharedAgentTools) (harness.Command, error) {
	tools := d.agentTools(opts.ID)
	if server {
		tools = nil
	}
	if err := d.prepareAgentLaunch(opts, req, tools, shared); err != nil {
		return harness.Command{}, fmt.Errorf("prepare %s launch: %w", opts.Agent, err)
	}
	return harness.Command{Argv: opts.AgentArgv, Env: opts.AgentEnv, LoginShell: opts.AgentLoginShell, Executable: opts.LaunchExecutable}, nil
}

func (d *Daemon) prepareAgentLaunch(opts *ptybackend.SpawnOptions, req harness.LaunchRequest, tools *agentdriver.AgentTools, shared *agentdriver.SharedAgentTools) error {
	terminal := opts.ID
	driver := agentdriver.Get(opts.Agent)
	if driver == nil {
		return fmt.Errorf("unknown agent %q", opts.Agent)
	}
	launch, awareness, initialPrompt := req.Guidance, req.AwarenessDirs, req.InitialPrompt
	if !req.Direct {
		var env []string
		if opts.CodexRemote != "" {
			env = append(env, "ATTN_CODEX_REMOTE="+opts.CodexRemote)
		}
		if launch.Crew != "" {
			env = append(env, "ATTN_CREW_MEMBER=1")
		}
		env = append(env, tools.LauncherEnv()...)
		env = append(env, shared.LauncherEnv()...)
		env = append(env, fmt.Sprintf("ATTN_VENT_ENABLED=%t", launch.Vent), fmt.Sprintf("ATTN_TOOL_VENTS_ENABLED=%t", launch.ToolVents), fmt.Sprintf("ATTN_CODE_REVIEW_ENABLED=%t", launch.CodeReview))
		autoApprove, model, effort := opts.AutoApprove, opts.Model, opts.Effort
		if unattended := opts.UnattendedLaunch; !unattended.IsZero() {
			autoApprove, model, effort = true, unattended.Model, unattended.Effort
		}
		command := harness.LauncherCommand(harness.LauncherLaunch{
			Label: opts.Label, Conversation: opts.ResumeSessionID, Picker: opts.ResumePicker, Yolo: opts.YoloMode, InitialPrompt: initialPrompt,
			ExecutableEnv: driver.ExecutableEnvVar(), Executable: configuredLaunchExecutable(opts),
			AutoApprove: autoApprove, Model: model, Effort: effort, Target: opts.ExecutionTarget, ContextCap: opts.ContextWindowCap,
			Env: env,
		})
		opts.AgentArgv, opts.AgentEnv, opts.AgentLoginShell = command.Argv, command.Env, command.LoginShell
		return nil
	}
	caps := agentdriver.EffectiveCapabilities(driver)
	executable := configuredLaunchExecutable(opts)
	explicitExecutable := executable
	if executable == "" {
		executable = driver.DefaultExecutable()
	}
	spawn := agentdriver.SpawnOpts{
		TerminalID: protocol.TerminalID(terminal), CWD: opts.CWD, Label: opts.Label,
		Cols: opts.Cols, Rows: opts.Rows, InitialPrompt: initialPrompt,
		ResumeSessionID: opts.ResumeSessionID, ResumePicker: opts.ResumePicker,
		YoloMode: opts.YoloMode, AutoApprove: opts.AutoApprove,
		Model: opts.Model, Effort: opts.Effort, ExecutionTarget: opts.ExecutionTarget, AutoCompactWindow: opts.ContextWindowCap,
		Executable: executable, SocketPath: d.socketPath,
		WrapperPath: launchenv.ActiveAttnExecutable(),
		Garden:      launch.Garden, Asks: launch.Asks, Vent: launch.Vent, ToolVents: launch.ToolVents, CodeReview: launch.CodeReview,
		CrewPriming: launch.Crew, AwarenessDirs: awareness,
		SelfReportPullRequests: launch.SelfReportPullRequests,
		CodexRemote:            opts.CodexRemote,
		AgentTools:             tools,
		SharedAgentTools:       shared,
	}
	if unattended := opts.UnattendedLaunch; !unattended.IsZero() {
		spawn.AutoApprove = true
		spawn.Model, spawn.Effort = unattended.Model, unattended.Effort
	}
	if !caps.HasResume {
		spawn.ResumeSessionID, spawn.ResumePicker = "", false
	}
	if preparer, ok := driver.(agentdriver.LaunchPreparer); ok {
		if err := preparer.PrepareLaunch(spawn); err != nil {
			return fmt.Errorf("prepare %s: %w", driver.Name(), err)
		}
	}
	if provider, ok := agentdriver.GetHookProvider(driver); ok {
		spawn.HookSettings = provider.GenerateHooksConfig(spawn)
	}
	if provider, ok := agentdriver.GetConfigOverrideProvider(driver); ok {
		spawn.ConfigOverrides = provider.GenerateConfigOverrides(spawn)
	}
	command := harness.DirectCommand(driver.ExecutableEnvVar(), explicitExecutable, driver.BuildCommand(spawn).Args, driver.BuildEnv(spawn))
	opts.AgentArgv, opts.AgentEnv, opts.AgentLoginShell = command.Argv, append(opts.AgentEnv, command.Env...), command.LoginShell
	return nil
}

func configuredLaunchExecutable(opts *ptybackend.SpawnOptions) string {
	if opts.LaunchExecutable != "" {
		return opts.LaunchExecutable
	}
	if !opts.UnattendedLaunch.IsZero() {
		return opts.UnattendedLaunch.Executable
	}
	return opts.Executable
}
