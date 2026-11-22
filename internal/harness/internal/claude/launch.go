package claude

import (
	"context"
	"fmt"
	"strings"

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
	var tools *harness.AgentTools
	if served, ok := h.host.TerminalTools(req.Terminal); ok {
		tools = &harness.AgentTools{URL: served.URL, Credential: served.Credential}
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

func claudeLauncherCommand(req harness.LaunchRequest, executable string, tools *harness.AgentTools, mod claudeModSpec) harness.Command {
	guidance := req.Guidance
	var env []string
	if mod != (claudeModSpec{}) {
		env = append(env, claudeModDirEnv+"="+mod.Dir, claudeModSocketEnv+"="+mod.Socket)
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
		ExecutableEnv: claudeExecutableEnvVar, Executable: executable,
		AutoApprove: autoApprove, Model: model, Effort: effort, Target: req.Options.Target, ContextCap: req.Options.ContextCap,
		Env: env,
	})
}

func (h claudeHarness) directCommand(req harness.LaunchRequest, explicit string, tools *harness.AgentTools, mod claudeModSpec) (harness.Command, error) {
	executable := explicit
	if executable == "" {
		executable = claudeDefaultExecutable
	}
	spawn := harness.DirectLaunchOpts{
		TerminalID: protocol.TerminalID(req.Terminal), CWD: req.Dir, InitialPrompt: req.InitialPrompt,
		ResumeSessionID: req.Conversation, ResumePicker: req.Picker,
		YoloMode: req.Options.Yolo, AutoApprove: req.Options.AutoApprove,
		Model: req.Options.Model, Effort: req.Options.Effort, AutoCompactWindow: req.Options.ContextCap,
		Executable: executable, SocketPath: h.host.DaemonSocket(),
		WrapperPath: launchenv.ActiveAttnExecutable(),
		Guidance:    req.Guidance, AwarenessDirs: req.AwarenessDirs,
		AgentTools: tools,
	}
	if unattended := req.Unattended; !unattended.IsZero() {
		spawn.AutoApprove = true
		spawn.Model, spawn.Effort = unattended.Model, unattended.Effort
	}
	facts := h.Facts()
	if !facts.Options.Resume {
		spawn.ResumeSessionID, spawn.ResumePicker = "", false
	}
	if err := prepareLaunch(spawn); err != nil {
		return harness.Command{}, fmt.Errorf("prepare %s: %w", claudeID, err)
	}
	hookSettings := ""
	if facts.Signals.ReportsTools {
		hookSettings = generateHooksConfig(spawn)
	}
	return harness.DirectCommand(claudeExecutableEnvVar, explicit, buildCommand(spawn, hookSettings, mod).Args, buildEnv(spawn, mod)), nil
}
