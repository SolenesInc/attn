package codex

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/launchenv"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/transcript"
)

type codexHarness struct {
	host   harness.Host
	shared *codexShared
	trust  *sync.Mutex
}

//go:embed logo.svg
var codexLogo string

func New(host func(owner string) harness.Host) harness.Harness {
	return newCodexHarness(host(codexSharedTerminalOwner))
}

func newCodexHarness(host harness.Host) codexHarness {
	h := codexHarness{
		host:  host,
		trust: &sync.Mutex{},
	}
	h.shared = newCodexShared(host, h.Facts, h.trust)
	host.RegisterClientCommand(protocol.CmdRestartSharedCodex, true, h.handleRestartSharedCodex)
	host.RegisterClientCommand(protocol.CmdInstallCodexUpdate, true, h.handleInstallCodexUpdate)
	host.RegisterSocketCommand(protocol.CmdCodexStatus, h.handleCodexStatus)
	host.RegisterSocketCommand(protocol.CmdRestartSharedCodex, h.handleRestartSharedCodexCLI)
	host.RegisterCatalogCommand("session instructions", command.Typed(h.runSessionInstructions))
	return h
}

func (codexHarness) Facts() harness.Facts { return Facts() }

func Facts() harness.Facts {
	return harness.ApplyCapabilityEnv(harness.Facts{
		ID:          codexID,
		DisplayName: codexDisplayName,
		Logo:        harness.Logo{View: 24, Body: codexLogo},
		Options: harness.Options{
			Resume:               true,
			Yolo:                 true,
			AutoApprove:          true,
			AutomationExecutable: false,
			InitialPrompt:        true,
			LaunchInstructions:   true,
			ModelPin:             true,
			EffortPin:            true,
			ContextCap:           true,
			Picker:               true,
		},
		Executable: harness.Executable{Default: codexDefaultExecutable, Env: codexExecutableEnvVar},
		SkillDirs: func(_ func(string) string, homeDir string) ([]string, error) {
			return []string{filepath.Join(homeDir, ".agents", "skills")}, nil
		},
		CacheTTL: 30 * time.Minute,
		ReservesServer: func(kind string) bool {
			return kind == codexLinkKind || strings.HasPrefix(kind, codexServerTerminalPrefix)
		},
		Tiers:   map[modeltiers.Tier]harness.ModelChoice{modeltiers.Light: {Model: "gpt-6-luna", Effort: "low"}, modeltiers.Standard: {Model: "gpt-5.6-terra"}, modeltiers.Deep: {Model: "gpt-6.1-sol"}},
		Advisor: harness.ModelChoice{Model: "gpt-5.6-luna", Effort: "xhigh"},
		Signals: harness.Signals{
			TurnStarts:            harness.TurnStartsReported,
			Heartbeat:             500 * time.Millisecond,
			ReviewerDwell:         time.Minute,
			ReportsTools:          true,
			ConfirmsFirstTurn:     true,
			Titles:                harness.TitlesCodex,
			StopsReadable:         true,
			StopsClassified:       true,
			ForgetsRecoveredClaim: true,
		},
		ModelDiscovery:        true,
		Activity:              true,
		ConversationIsSession: true,
		ServerTerminal:        codexServerTerminal,
		Caller:                harness.CallerEnv{Scope: SharedProfileEnv, Conversation: ThreadEnv},
	})
}

func (h codexHarness) Terminal(t harness.TerminalID, e harness.TerminalEvent) {
	switch e := e.(type) {
	case harness.Exited:
		h.shared.terminalExited(t)
	case harness.Dropped:
		h.shared.terminalDropped(t)
	case harness.Handed:
		h.shared.terminalMoved(t, e.Profile)
	case harness.Working:
		h.shared.checkWorkingSoon(e.Session)
	case harness.Restarting:
		h.shared.retireConnections(t)
	}
}

func (h codexHarness) Launch(_ context.Context, req harness.LaunchRequest) (harness.Command, error) {
	if !req.Unattended.IsZero() {
		if err := h.refuseUnknownCodexFolder(req.Dir, req.Profile); err != nil {
			return harness.Command{}, err
		}
	}
	unattended := !req.Unattended.IsZero()
	var remote, executable, launchExecutable string
	if req.Lifetime == harness.InServer {
		if err := h.shared.admit(req.Profile, req.Conversation); err != nil {
			return harness.Command{}, err
		}
		if req.Conversation != "" {
			if err := h.shared.restore(req.Profile, req.Conversation); err != nil {
				return harness.Command{}, err
			}
		}
		configured := req.Executable
		if configured == "" {
			configured = strings.TrimSpace(h.host.Setting(setting.Executable.For(h.Facts().ID)))
		}
		var err error
		if req.Reason == harness.Reload {
			remote, launchExecutable, err = h.shared.relaunch(req.Terminal, req.Dir, unattended, configured)
		} else {
			remote, launchExecutable, err = h.shared.prepareLaunch(req.Terminal, req.Dir, req.Profile, unattended, configured)
		}
		if err != nil {
			return harness.Command{}, err
		}
		executable = launchExecutable
	} else {
		executable = req.Executable
		if unattended {
			executable = req.Unattended.Executable
		}
		if executable == "" {
			launchExecutable = strings.TrimSpace(h.host.Setting(setting.Executable.For(h.Facts().ID)))
			executable = launchExecutable
		}
	}
	var tools *harness.AgentTools
	if served, ok := h.host.TerminalTools(req.Terminal); ok {
		tools = &harness.AgentTools{URL: served.URL, Credential: served.Credential}
	}
	var cmd harness.Command
	if req.Direct {
		var err error
		if cmd, err = h.directCommand(req, executable, remote, tools); err != nil {
			return harness.Command{}, fmt.Errorf("prepare %s launch: %w", h.Facts().ID, err)
		}
	} else {
		cmd = h.launcherCommand(req, executable, remote, tools)
	}
	cmd.Executable = launchExecutable
	cmd.TitleSignals = h.Facts().Signals.Titles
	return cmd, nil
}

func (h codexHarness) launcherCommand(req harness.LaunchRequest, executable, remote string, tools *harness.AgentTools) harness.Command {
	guidance := req.Guidance
	var env []string
	if remote != "" {
		env = append(env, codexRemoteEnv+"="+remote)
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
		ExecutableEnv: codexExecutableEnvVar, Executable: executable,
		AutoApprove: autoApprove, Model: model, Effort: effort, Target: req.Options.Target, ContextCap: req.Options.ContextCap,
		Env: env,
	})
}

func (h codexHarness) directCommand(req harness.LaunchRequest, explicit, remote string, tools *harness.AgentTools) (harness.Command, error) {
	executable := explicit
	if executable == "" {
		executable = codexDefaultExecutable
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
	var overrides []string
	if facts.Signals.ReportsTools {
		overrides = generateConfigOverrides(spawn, remote)
	}
	return harness.DirectCommand(codexExecutableEnvVar, explicit, buildCommand(spawn, remote, overrides).Args, buildEnv(spawn, remote)), nil
}

func (h codexHarness) TrustFolder(dir string) error { return h.trustCodexFolder(dir) }

func (h codexHarness) Rename(session harness.SessionID, label string) {
	h.shared.renamed(session, label)
}

func (h codexHarness) Plan(req harness.LaunchRequest) (harness.Planned, error) {
	lifetime := harness.InTerminal
	if req.Lifetime == harness.InServer || req.Lifetime == "" && h.host.AgentSockets() {
		lifetime = harness.InServer
	}
	conversation := harness.ConversationCleared
	if req.Conversation != "" {
		conversation = harness.ConversationIs(req.Conversation)
	}
	return harness.Planned{Lifetime: lifetime, Conversation: conversation}, nil
}

func (h codexHarness) Start(context.Context) error {
	enqueue := h.host.RegisterCron(codexUpdateCheckKind, codexUpdateCheckInterval, true, h.shared.runUpdateCheck)
	h.shared.setEnqueueUpdateCheck(enqueue)
	return nil
}

func (h codexHarness) Recover(ctx context.Context, point harness.RecoverPoint) {
	switch point {
	case harness.RecoverProcesses:
		h.shared.recoverProcesses(ctx)
	case harness.RecoverViews:
		h.shared.recoverViews(ctx)
	}
}

func (h codexHarness) SettingChanged(key string) { h.shared.settingChanged(key) }

func (h codexHarness) Released(profile string) { h.shared.profileReleased(profile) }

func (h codexHarness) Stop() { h.shared.close() }

func (h codexHarness) Conversations() harness.ConversationStore { return h }

func (h codexHarness) Admit(ref harness.ConversationRef) error {
	if ref.Lifetime != harness.InServer {
		return nil
	}
	return h.shared.admit(ref.Profile, ref.ID)
}

func (h codexHarness) resumable(id string) bool {
	return strings.TrimSpace(id) != "" && transcript.FindCodexTranscriptForResume(id) != ""
}

func (h codexHarness) Check(_ context.Context, refs []harness.ConversationRef) map[harness.ConversationRef]harness.Availability {
	return h.host.Files().Check(refs, h.resumable)
}

func (h codexHarness) Prepare(_ context.Context, ref harness.ConversationRef) (bool, error) {
	if ref.Lifetime == harness.InServer {
		if err := h.shared.restore(ref.Profile, ref.ID); err != nil {
			return false, err
		}
	}
	return h.host.Files().Ready(ref, h.resumable), nil
}

func (h codexHarness) End(_ context.Context, e harness.Ending) {
	h.shared.sessionClosed(e.Session, e.Profile, e.Released)
}

func (h codexHarness) Health(context.Context) harness.Health {
	return h.host.ExecutableHealth(h.Facts(), true)
}

func (h codexHarness) Page(ctx context.Context, ref harness.ConversationRef, cursor string, limit int) (transcript.EventPage, error) {
	return h.host.Files().Page(ctx, ref, cursor, limit)
}

func (h codexHarness) LastReply(ctx context.Context, ref harness.ConversationRef, max int) (string, error) {
	return h.host.Files().LastReply(ctx, ref, max)
}

func (h codexHarness) Window(ctx context.Context, ref harness.ConversationRef) (harness.AssistantWindow, bool) {
	return h.host.Files().Window(ctx, ref)
}

func (h codexHarness) Activity(ctx context.Context, ref harness.ConversationRef, kind, cursor string) (protocol.TranscriptActivityResult, error) {
	return h.host.Files().Activity(ctx, ref, kind, cursor)
}

func (h codexHarness) Version(ref harness.ConversationRef) string {
	path := h.host.Files().Path(ref)
	if path == "" {
		return ""
	}
	return transcript.CodexVersion(path)
}

func (h codexHarness) Locate(ref harness.ConversationRef) string {
	if ref.Path != "" {
		if ref.Lifetime == harness.InServer {
			return h.shared.transcriptPath(ref.Path)
		}
		return ref.Path
	}
	id := strings.TrimSpace(ref.ID)
	if id == "" || !h.Facts().Signals.StopsReadable {
		return ""
	}
	return strings.TrimSpace(transcript.FindCodexTranscriptForResume(id))
}

func (codexHarness) IDFromPath(string) string { return "" }
