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
	command, err := h.directCommand(req, executable, tools, mod)
	if err != nil {
		return harness.Command{}, fmt.Errorf("prepare %s launch: %w", h.Facts().ID, err)
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
