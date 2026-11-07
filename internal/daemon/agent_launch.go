package daemon

import (
	"fmt"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/launchenv"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func (d *Daemon) prepareAgentLaunch(opts *ptybackend.SpawnOptions, intent *AgentLaunch) error {
	terminal := opts.ID
	driver := agentdriver.Get(opts.Agent)
	if driver == nil {
		return fmt.Errorf("unknown agent %q", opts.Agent)
	}
	launch, awareness, initialPrompt := intent.Context, intent.AwarenessDirs, intent.InitialPrompt
	claudeMod := d.claudeModForLaunch(terminal, driver.Name())
	if !intent.Direct {
		prepareLauncherLaunch(driver, opts, initialPrompt)
		if claudeMod != (agentdriver.ClaudeMod{}) {
			opts.AgentEnv = append(opts.AgentEnv, agentdriver.ClaudeModDirEnv+"="+claudeMod.Dir, agentdriver.ClaudeModSocketEnv+"="+claudeMod.Socket)
		}
		if launch.Crew != "" {
			opts.AgentEnv = append(opts.AgentEnv, "ATTN_CREW_MEMBER=1")
		}
		opts.AgentEnv = append(opts.AgentEnv, fmt.Sprintf("ATTN_QUICK_CAPTURE_ENABLED=%t", launch.QuickCapture), fmt.Sprintf("ATTN_VENT_ENABLED=%t", launch.Vent), fmt.Sprintf("ATTN_TOOL_VENTS_ENABLED=%t", launch.ToolVents))
		return nil
	}
	caps := agentdriver.EffectiveCapabilities(driver)
	executable := configuredLaunchExecutable(driver, opts)
	explicitExecutable := executable
	if executable == "" {
		executable = driver.DefaultExecutable()
	}
	spawn := agentdriver.SpawnOpts{
		TerminalID: protocol.TerminalID(terminal), CWD: opts.CWD, Label: opts.Label,
		Cols: opts.Cols, Rows: opts.Rows, InitialPrompt: initialPrompt,
		ResumeSessionID: opts.ResumeSessionID, ResumePicker: opts.ResumePicker,
		YoloMode: opts.YoloMode, AutoApprove: opts.AutoApprove,
		Model: opts.Model, Effort: opts.Effort, AutoCompactWindow: opts.ContextWindowCap,
		Executable: executable, SocketPath: d.socketPath,
		WrapperPath: launchenv.ActiveAttnExecutable(),
		Garden:      launch.Garden, Asks: launch.Asks, Vent: launch.Vent, ToolVents: launch.ToolVents,
		CrewPriming: launch.Crew, AwarenessDirs: awareness,
		SelfReportPullRequests: launch.SelfReportPullRequests,
		QuickCapture:           launch.QuickCapture,
		CodexRemote:            opts.CodexRemote,
		ClaudeMod:              claudeMod,
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
	opts.AgentArgv = append([]string{"/bin/sh", "-c", directAgentLaunchScript, "attn-agent", driver.ExecutableEnvVar(), explicitExecutable}, driver.BuildCommand(spawn).Args...)
	for _, entry := range driver.BuildEnv(spawn) {
		key, _, _ := strings.Cut(entry, "=")
		if key != driver.ExecutableEnvVar() {
			opts.AgentEnv = append(opts.AgentEnv, entry)
		}
	}
	opts.AgentLoginShell = true
	return nil
}

func configuredLaunchExecutable(driver agentdriver.Driver, opts *ptybackend.SpawnOptions) string {
	if opts.LaunchExecutable != "" {
		return opts.LaunchExecutable
	}
	if !opts.UnattendedLaunch.IsZero() {
		return opts.UnattendedLaunch.Executable
	}
	if opts.Executable != "" {
		return opts.Executable
	}
	switch driver.Name() {
	case "claude":
		return opts.ClaudeExecutable
	case "codex":
		return opts.CodexExecutable
	}
	return ""
}

func prepareLauncherLaunch(driver agentdriver.Driver, opts *ptybackend.SpawnOptions, initialPrompt string) {
	opts.AgentArgv = []string{launchenv.ActiveAttnExecutable(), "-s", opts.Label}
	if opts.ResumeSessionID != "" {
		opts.AgentArgv = append(opts.AgentArgv, "--resume", opts.ResumeSessionID)
	} else if opts.ResumePicker {
		opts.AgentArgv = append(opts.AgentArgv, "--resume")
	}
	if opts.YoloMode {
		opts.AgentArgv = append(opts.AgentArgv, "--yolo")
	}
	if initialPrompt != "" {
		opts.AgentArgv = append(opts.AgentArgv, "--initial-prompt", initialPrompt)
	}
	executable := configuredLaunchExecutable(driver, opts)
	autoApprove, model, effort := opts.AutoApprove, opts.Model, opts.Effort
	if launch := opts.UnattendedLaunch; !launch.IsZero() {
		autoApprove, model, effort = true, launch.Model, launch.Effort
	}
	opts.AgentEnv = []string{"ATTN_DAEMON_MANAGED=1", "ATTN_MODEL=" + model, "ATTN_EFFORT=" + effort, fmt.Sprintf("ATTN_AUTO_COMPACT_WINDOW=%d", opts.ContextWindowCap)}
	if autoApprove {
		opts.AgentEnv = append(opts.AgentEnv, "ATTN_AUTO_APPROVE=1")
	}
	if executable != "" {
		opts.AgentEnv = append(opts.AgentEnv, driver.ExecutableEnvVar()+"="+executable)
	}
	if opts.CodexRemote != "" {
		opts.AgentEnv = append(opts.AgentEnv, "ATTN_CODEX_REMOTE="+opts.CodexRemote)
	}
	opts.AgentLoginShell = true
}

const directAgentLaunchScript = `
trim_space() {
 trimmed=${1#"${1%%[![:space:]]*}"}
 trimmed=${trimmed%"${trimmed##*[![:space:]]}"}
}
executable_var=$1
explicit_executable=$2
shift 2
eval "inherited_executable=\${$executable_var-}"
trim_space "$inherited_executable"
executable=${explicit_executable:-${trimmed:-$1}}
shift
export "$executable_var=$executable"
exec "$executable" "$@"
`
