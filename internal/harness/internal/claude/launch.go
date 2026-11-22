package claude

import (
	"context"
	"fmt"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/launchenv"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

func (h claudeHarness) Launch(_ context.Context, req harness.LaunchRequest) (harness.Command, error) {
	configured, executable := "", claudeExplicitExecutable(req)
	if executable == "" {
		configured = strings.TrimSpace(h.host.Setting(setting.Executable.For(h.Facts().ID)))
		executable = configured
	}
	if err := h.settleFolderTrust(req); err != nil {
		return harness.Command{}, err
	}
	var tools *agentdriver.AgentTools
	if served, ok := h.host.TerminalTools(req.Terminal); ok {
		tools = &agentdriver.AgentTools{URL: served.URL, Credential: served.Credential}
	}
	mod := h.mod.forLaunch(req.Terminal)
	var command harness.Command
	if req.Direct {
		var err error
		if command, err = h.directCommand(req, executable, tools, mod); err != nil {
			return harness.Command{}, fmt.Errorf("prepare %s launch: %w", h.Facts().ID, err)
		}
	} else {
		command = claudeLauncherCommand(req, executable, tools, mod)
	}
	command.Executable = configured
	command.TitleSignals = h.Facts().Signals.Titles
	return command, nil
}

func claudeExplicitExecutable(req harness.LaunchRequest) string {
	if !req.Unattended.IsZero() {
		return req.Unattended.Executable
	}
	return req.Executable
}

func claudeLauncherCommand(req harness.LaunchRequest, executable string, tools *agentdriver.AgentTools, mod agentdriver.ClaudeMod) harness.Command {
	guidance := req.Guidance
	var env []string
	if mod != (agentdriver.ClaudeMod{}) {
		env = append(env, agentdriver.ClaudeModDirEnv+"="+mod.Dir, agentdriver.ClaudeModSocketEnv+"="+mod.Socket)
	}
	if guidance.Crew != "" {
		env = append(env, "ATTN_CREW_MEMBER=1")
	}
	env = append(env, tools.LauncherEnv()...)
	env = append(env, fmt.Sprintf("ATTN_VENT_ENABLED=%t", guidance.Vent), fmt.Sprintf("ATTN_TOOL_VENTS_ENABLED=%t", guidance.ToolVents), fmt.Sprintf("ATTN_CODE_REVIEW_ENABLED=%t", guidance.CodeReview))
	autoApprove, model, effort := req.Options.AutoApprove, req.Options.Model, req.Options.Effort
	if unattended := req.Unattended; !unattended.IsZero() {
		autoApprove, model, effort = true, unattended.Model, unattended.Effort
	}
	return harness.LauncherCommand(harness.LauncherLaunch{
		Label: req.Label, Conversation: req.Conversation, Picker: req.Picker, Yolo: req.Options.Yolo, InitialPrompt: req.InitialPrompt,
		ExecutableEnv: claudeDriver().ExecutableEnvVar(), Executable: executable,
		AutoApprove: autoApprove, Model: model, Effort: effort, Target: req.Options.Target, ContextCap: req.Options.ContextCap,
		Env: env,
	})
}

func (h claudeHarness) directCommand(req harness.LaunchRequest, explicit string, tools *agentdriver.AgentTools, mod agentdriver.ClaudeMod) (harness.Command, error) {
	driver := h.driver()
	executable := explicit
	if executable == "" {
		executable = driver.DefaultExecutable()
	}
	guidance := req.Guidance
	spawn := agentdriver.SpawnOpts{
		TerminalID: protocol.TerminalID(req.Terminal), CWD: req.Dir, Label: req.Label,
		Cols: req.Cols, Rows: req.Rows, InitialPrompt: req.InitialPrompt,
		ResumeSessionID: req.Conversation, ResumePicker: req.Picker,
		YoloMode: req.Options.Yolo, AutoApprove: req.Options.AutoApprove,
		Model: req.Options.Model, Effort: req.Options.Effort, ExecutionTarget: req.Options.Target, AutoCompactWindow: req.Options.ContextCap,
		Executable: executable, SocketPath: h.host.DaemonSocket(),
		WrapperPath: launchenv.ActiveAttnExecutable(),
		Garden:      guidance.Garden, Asks: guidance.Asks, Vent: guidance.Vent, ToolVents: guidance.ToolVents, CodeReview: guidance.CodeReview,
		CrewPriming: guidance.Crew, AwarenessDirs: req.AwarenessDirs,
		SelfReportPullRequests: guidance.SelfReportPullRequests,
		ClaudeMod:              mod,
		AgentTools:             tools,
	}
	if unattended := req.Unattended; !unattended.IsZero() {
		spawn.AutoApprove = true
		spawn.Model, spawn.Effort = unattended.Model, unattended.Effort
	}
	if !agentdriver.EffectiveCapabilities(driver).HasResume {
		spawn.ResumeSessionID, spawn.ResumePicker = "", false
	}
	if preparer, ok := driver.(agentdriver.LaunchPreparer); ok {
		if err := preparer.PrepareLaunch(spawn); err != nil {
			return harness.Command{}, fmt.Errorf("prepare %s: %w", driver.Name(), err)
		}
	}
	if provider, ok := agentdriver.GetHookProvider(driver); ok {
		spawn.HookSettings = provider.GenerateHooksConfig(spawn)
	}
	if provider, ok := agentdriver.GetConfigOverrideProvider(driver); ok {
		spawn.ConfigOverrides = provider.GenerateConfigOverrides(spawn)
	}
	return harness.DirectCommand(driver.ExecutableEnvVar(), explicit, driver.BuildCommand(spawn).Args, driver.BuildEnv(spawn)), nil
}
