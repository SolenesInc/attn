package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/launchenv"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func (d *Daemon) prepareAgentLaunch(sessionID protocol.SessionID, terminal harness.TerminalID, isChief bool, opts *ptybackend.SpawnOptions, initialPrompt string) error {
	driver := agentdriver.Get(opts.Agent)
	if driver == nil {
		return fmt.Errorf("unknown agent %q", opts.Agent)
	}
	if !parseBooleanSetting(d.store.GetSetting(SettingDirectAgentLaunchEnabled)) {
		prepareLauncherLaunch(driver, opts, initialPrompt)
		return nil
	}
	caps := agentdriver.EffectiveCapabilities(driver)
	launch, awareness, err := d.prepareLaunchContext(sessionID, isChief && caps.HasLaunchInstructions, !caps.HasHooks)
	if err != nil {
		return err
	}
	executable := configuredLaunchExecutable(driver, opts)
	if !opts.UnattendedLaunch.IsZero() {
		executable = opts.UnattendedLaunch.Executable
	}
	explicitExecutable := executable
	if executable == "" {
		executable = driver.DefaultExecutable()
	}
	spawn := agentdriver.SpawnOpts{
		TerminalID: protocol.TerminalID(terminal), CWD: opts.CWD, Label: opts.Label,
		Cols: opts.Cols, Rows: opts.Rows, InitialPrompt: initialPrompt,
		ResumeSessionID: opts.ResumeSessionID, ResumePicker: opts.ResumePicker,
		YoloMode: opts.YoloMode, AutoApprove: opts.AutoApprove,
		TrustWorkingDirectory: opts.TrustWorkingDirectory,
		Model:                 opts.Model, Effort: opts.Effort, AutoCompactWindow: opts.ContextWindowCap,
		Executable: executable, SocketPath: d.socketPath,
		WrapperPath:  launchenv.ActiveAttnExecutable(),
		NotebookRoot: launch.NotebookRoot, Garden: launch.Garden,
		CrewPriming: launch.Crew, AwarenessDirs: awareness,
		SelfReportPullRequests: launch.SelfReportPullRequests,
		CodexRemote:            opts.CodexRemote,
	}
	if unattended := opts.UnattendedLaunch; !unattended.IsZero() {
		spawn.AutoApprove, spawn.TrustWorkingDirectory = true, true
		spawn.Model, spawn.Effort = unattended.Model, unattended.Effort
	}
	if !caps.HasResume {
		spawn.ResumeSessionID, spawn.ResumePicker = "", false
	}
	if _, err := agentdriver.EnsureAttnSkillInstalled(driver.Name()); err != nil {
		d.logf("failed to ensure %s attn skill for launch: %v", driver.Name(), err)
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
	if provider, ok := agentdriver.GetInstructionsFileProvider(driver); ok {
		name, content := provider.GenerateInstructionsFile(spawn)
		if strings.TrimSpace(content) != "" {
			spawn.InstructionsDir = d.agentLaunchDir(terminal)
			if err := os.MkdirAll(spawn.InstructionsDir, 0o700); err != nil {
				return fmt.Errorf("create launch instructions directory: %w", err)
			}
			if err := os.WriteFile(filepath.Join(spawn.InstructionsDir, name), []byte(content), 0o600); err != nil {
				return fmt.Errorf("write launch instructions: %w", err)
			}
		}
	}
	opts.AgentArgv = append([]string{"/bin/sh", "-c", directAgentLaunchScript, "attn-agent", driver.ExecutableEnvVar(), explicitExecutable, spawn.InstructionsDir}, driver.BuildCommand(spawn).Args...)
	for _, entry := range driver.BuildEnv(spawn) {
		key, _, _ := strings.Cut(entry, "=")
		if key != driver.ExecutableEnvVar() && key != "COPILOT_CUSTOM_INSTRUCTIONS_DIRS" {
			opts.AgentEnv = append(opts.AgentEnv, entry)
		}
	}
	opts.AgentLoginShell = true
	return nil
}

func (d *Daemon) agentLaunchDir(terminal harness.TerminalID) string {
	root := d.dataRoot
	if root == "" {
		root = filepath.Dir(d.socketPath)
	}
	return filepath.Join(root, "runtime", "launch", string(terminal))
}

func (d *Daemon) removeAgentLaunchDir(terminal harness.TerminalID) {
	if err := os.RemoveAll(d.agentLaunchDir(terminal)); err != nil {
		d.logf("remove launch instructions for terminal %s: %v", terminal, err)
	}
}

func configuredLaunchExecutable(driver agentdriver.Driver, opts *ptybackend.SpawnOptions) string {
	if opts.Executable != "" {
		return opts.Executable
	}
	switch driver.Name() {
	case "claude":
		return opts.ClaudeExecutable
	case "codex":
		return opts.CodexExecutable
	case "copilot":
		return opts.CopilotExecutable
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
	autoApprove, trust, model, effort := opts.AutoApprove, opts.TrustWorkingDirectory, opts.Model, opts.Effort
	if launch := opts.UnattendedLaunch; !launch.IsZero() {
		autoApprove, trust, model, effort, executable = true, true, launch.Model, launch.Effort, launch.Executable
	}
	opts.AgentEnv = []string{"ATTN_DAEMON_MANAGED=1", "ATTN_MODEL=" + model, "ATTN_EFFORT=" + effort, fmt.Sprintf("ATTN_AUTO_COMPACT_WINDOW=%d", opts.ContextWindowCap)}
	if autoApprove {
		opts.AgentEnv = append(opts.AgentEnv, "ATTN_AUTO_APPROVE=1")
	}
	if trust {
		opts.AgentEnv = append(opts.AgentEnv, "ATTN_TRUST_WORKING_DIRECTORY=1")
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
instructions_dir=$3
shift 3
case "$executable_var" in
 ATTN_CLAUDE_EXECUTABLE) inherited_executable=${ATTN_CLAUDE_EXECUTABLE-} ;;
 ATTN_CODEX_EXECUTABLE) inherited_executable=${ATTN_CODEX_EXECUTABLE-} ;;
 ATTN_COPILOT_EXECUTABLE) inherited_executable=${ATTN_COPILOT_EXECUTABLE-} ;;
esac
trim_space "$inherited_executable"
executable=${explicit_executable:-${trimmed:-$1}}
shift
export "$executable_var=$executable"
if [ -n "$instructions_dir" ]; then
 remaining_dirs=${COPILOT_CUSTOM_INSTRUCTIONS_DIRS-}
 instructions=
 while :; do
  entry=${remaining_dirs%%,*}
  trim_space "$entry"
  if [ -n "$trimmed" ] && [ "$trimmed" != "$instructions_dir" ]; then
   instructions=${instructions:+$instructions,}$trimmed
  fi
  case "$remaining_dirs" in
   *,*) remaining_dirs=${remaining_dirs#*,} ;;
   *) break ;;
  esac
 done
 export COPILOT_CUSTOM_INSTRUCTIONS_DIRS=${instructions:+$instructions,}$instructions_dir
fi
exec "$executable" "$@"
`
