// Package sigma is the Sigma harness: attn's adapter for Sigma and its app server.
package sigma

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/appserver"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/launchenv"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/transcript"
)

const id = "sigma"

//go:embed logo.svg
var logo string

// Harness is Sigma, always linked to its app server.
type Harness struct {
	host harness.Host
	link *appserver.Link
}

// New binds Sigma's server on servers and returns its harness.
func New(host harness.Host, servers *appserver.Client) *Harness {
	h := &Harness{host: host}
	declaration := driver().Server()
	binding := appserver.Binding{Agent: id, Declaration: &declaration, DisplayName: func() string { return driver().DisplayName() }, Enabled: h.enabled}
	if err := servers.Bind(binding, nil); err != nil {
		host.Logf("link the %s server: %v", id, err)
	}
	h.link = servers.Link(id)
	return h
}

func driver() *agentdriver.Sigma { return agentdriver.MustGet(id).(*agentdriver.Sigma) }

func (h *Harness) enabled() bool { return setting.ParseBool(h.host.Setting(setting.SigmaEnabled)) }

func (h *Harness) Facts() harness.Facts {
	facts := agentdriver.Facts(driver())
	facts.Logo = harness.Logo{View: 24, Body: logo}
	facts.SkillDirs = skillDirs
	facts.CacheTTL = time.Hour
	facts.Signals.Heartbeat = 500 * time.Millisecond
	facts.Recovery = harness.ByServer
	facts.ModelDiscovery = true
	facts.Activity = true
	facts.ServerTerminal = h.link.ServerTerminal
	return facts
}

func (*Harness) Terminal(harness.TerminalID, harness.TerminalEvent) {}

func (h *Harness) Conversation(facts harness.SessionFacts) harness.Conversation {
	if facts.Lifetime == harness.InServer {
		return h.link.Conversation(facts)
	}
	return harness.TypedConversation(harness.NoCompaction(id))
}

func (h *Harness) Plan(req harness.LaunchRequest) (harness.Planned, error) {
	planned := h.link.Plan("", req)
	planned.Conversation = harness.ResumedOrTerminal(req)
	return planned, nil
}

func (h *Harness) Launch(_ context.Context, req harness.LaunchRequest) (harness.Command, error) {
	if err := h.link.Launch(req); err != nil {
		return harness.Command{}, err
	}
	onServer := req.Lifetime == harness.InServer
	configured, executable := "", explicitExecutable(req)
	if executable == "" {
		configured = strings.TrimSpace(h.host.Setting(setting.Executable.For(id)))
		executable = configured
	}
	var tools *agentdriver.AgentTools
	var shared *agentdriver.SharedAgentTools
	if onServer {
		if served := h.link.SharedTools(); served != nil {
			shared = &agentdriver.SharedAgentTools{Name: served.Name, Description: served.Description, Instructions: served.Instructions}
		}
	} else if served, ok := h.host.TerminalTools(req.Terminal); ok {
		tools = &agentdriver.AgentTools{URL: served.URL, Credential: served.Credential}
	}
	var command harness.Command
	if req.Direct {
		command = directCommand(req, executable, tools, shared)
	} else {
		command = launcherCommand(req, executable, tools, shared)
	}
	command.Executable = configured
	if onServer {
		command.Env = append(command.Env, h.link.HomeEnv())
	}
	return command, nil
}

func explicitExecutable(req harness.LaunchRequest) string {
	if !req.Unattended.IsZero() {
		return req.Unattended.Executable
	}
	return req.Executable
}

func launcherCommand(req harness.LaunchRequest, executable string, tools *agentdriver.AgentTools, shared *agentdriver.SharedAgentTools) harness.Command {
	guidance := req.Guidance
	var env []string
	if guidance.Crew != "" {
		env = append(env, "ATTN_CREW_MEMBER=1")
	}
	env = append(env, tools.LauncherEnv()...)
	env = append(env, shared.LauncherEnv()...)
	env = append(env, fmt.Sprintf("ATTN_VENT_ENABLED=%t", guidance.Vent), fmt.Sprintf("ATTN_TOOL_VENTS_ENABLED=%t", guidance.ToolVents), fmt.Sprintf("ATTN_CODE_REVIEW_ENABLED=%t", guidance.CodeReview))
	autoApprove, model, effort := req.Options.AutoApprove, req.Options.Model, req.Options.Effort
	if unattended := req.Unattended; !unattended.IsZero() {
		autoApprove, model, effort = true, unattended.Model, unattended.Effort
	}
	return harness.LauncherCommand(harness.LauncherLaunch{
		Label: req.Label, Conversation: req.Conversation, Picker: req.Picker, Yolo: req.Options.Yolo, InitialPrompt: req.InitialPrompt,
		ExecutableEnv: driver().ExecutableEnvVar(), Executable: executable,
		AutoApprove: autoApprove, Model: model, Effort: effort, Target: req.Options.Target, ContextCap: req.Options.ContextCap,
		Env: env,
	})
}

func directCommand(req harness.LaunchRequest, explicit string, tools *agentdriver.AgentTools, shared *agentdriver.SharedAgentTools) harness.Command {
	sigma := driver()
	executable := explicit
	if executable == "" {
		executable = sigma.DefaultExecutable()
	}
	guidance := req.Guidance
	spawn := agentdriver.SpawnOpts{
		TerminalID: protocol.TerminalID(req.Terminal), CWD: req.Dir, Label: req.Label,
		Cols: req.Cols, Rows: req.Rows, InitialPrompt: req.InitialPrompt,
		ResumeSessionID: req.Conversation, ResumePicker: req.Picker,
		YoloMode: req.Options.Yolo, AutoApprove: req.Options.AutoApprove,
		Model: req.Options.Model, Effort: req.Options.Effort, ExecutionTarget: req.Options.Target, AutoCompactWindow: req.Options.ContextCap,
		Executable: executable, WrapperPath: launchenv.ActiveAttnExecutable(),
		Garden: guidance.Garden, Asks: guidance.Asks, Vent: guidance.Vent, ToolVents: guidance.ToolVents, CodeReview: guidance.CodeReview,
		CrewPriming: guidance.Crew, AwarenessDirs: req.AwarenessDirs,
		SelfReportPullRequests: guidance.SelfReportPullRequests,
		AgentTools:             tools,
		SharedAgentTools:       shared,
	}
	if unattended := req.Unattended; !unattended.IsZero() {
		spawn.AutoApprove = true
		spawn.Model, spawn.Effort = unattended.Model, unattended.Effort
	}
	if !agentdriver.EffectiveCapabilities(sigma).HasResume {
		spawn.ResumeSessionID, spawn.ResumePicker = "", false
	}
	return harness.DirectCommand(sigma.ExecutableEnvVar(), explicit, sigma.BuildCommand(spawn).Args, sigma.BuildEnv(spawn))
}

func (*Harness) Start(context.Context) error { return nil }

func (h *Harness) Recover(ctx context.Context, point harness.RecoverPoint) {
	h.link.Recover(ctx, point)
}

func (*Harness) SettingChanged(string) {}

func (h *Harness) Released(string) { h.link.Release() }

func (*Harness) Stop() {}

func (h *Harness) Health(context.Context) harness.Health {
	return h.host.ExecutableHealth(h.Facts(), h.enabled())
}

func (h *Harness) Conversations() harness.ConversationStore { return h }

func (h *Harness) Admit(ref harness.ConversationRef) error { return h.link.Admit(ref) }

func (h *Harness) Check(_ context.Context, refs []harness.ConversationRef) map[harness.ConversationRef]harness.Availability {
	return h.link.ConversationsResumable(refs)
}

func (*Harness) Prepare(_ context.Context, ref harness.ConversationRef) (bool, error) {
	return ref.ID != "", nil
}

func (h *Harness) End(_ context.Context, e harness.Ending) {
	h.link.SessionClosed(e.Session, e.Profile, e.Released)
}

func (h *Harness) Page(ctx context.Context, ref harness.ConversationRef, cursor string, limit int) (transcript.EventPage, error) {
	if ref.ID == "" {
		return transcript.EventPage{}, harness.ErrTranscriptUnavailable
	}
	return h.link.TranscriptPage(ctx, ref.ID, cursor, limit)
}

func (h *Harness) LastReply(ctx context.Context, ref harness.ConversationRef, max int) (string, error) {
	if ref.ID == "" {
		return "", nil
	}
	message, err := h.link.LastReply(ctx, ref.ID)
	if runes := []rune(message); len(runes) > max {
		message = string(runes[len(runes)-max:])
	}
	return message, err
}

func (h *Harness) Window(ctx context.Context, ref harness.ConversationRef) (harness.AssistantWindow, bool) {
	if ref.ID == "" {
		return harness.AssistantWindow{}, false
	}
	return h.link.AssistantWindow(ctx, ref.ID)
}

func (h *Harness) Activity(ctx context.Context, ref harness.ConversationRef, kind, cursor string) (protocol.TranscriptActivityResult, error) {
	if ref.ID == "" {
		return protocol.TranscriptActivityResult{}, nil
	}
	return h.link.Activity(ctx, ref.ID, kind, cursor)
}

func (h *Harness) Version(harness.ConversationRef) string { return h.link.Version() }

func (*Harness) Locate(ref harness.ConversationRef) string { return ref.Path }

func (*Harness) IDFromPath(string) string { return "" }

type provider struct {
	ID     string `json:"id"`
	Health struct {
		Status string `json:"status"`
	} `json:"health"`
	Ephemeral *bool `json:"ephemeral"`
}

func (h *Harness) Models(ctx context.Context) (harness.Models, error) {
	server := h.link
	models, err := server.Models(ctx, true)
	if err != nil {
		return harness.Models{}, err
	}
	var providers []provider
	if err := server.Call(ctx, "providers/list", appserver.RefreshParams(true), &providers); err != nil {
		return harness.Models{}, err
	}
	byID := map[string]provider{}
	for _, p := range providers {
		byID[p.ID] = p
	}
	for i := range models.List {
		model := &models.List[i]
		provider, listed := byID[model.Provider]
		if !listed {
			continue
		}
		if provider.Health.Status != "ready" {
			model.Access = protocol.ModelCapabilitySupportUnsupported
		}
		if provider.Ephemeral != nil {
			model.OneShot = supportOf(*provider.Ephemeral)
		}
	}
	return models, nil
}

func supportOf(supported bool) protocol.ModelCapabilitySupport {
	if supported {
		return protocol.ModelCapabilitySupportSupported
	}
	return protocol.ModelCapabilitySupportUnsupported
}

type ephemeralRun struct {
	Prompt   string          `json:"prompt"`
	System   string          `json:"system,omitempty"`
	Schema   json.RawMessage `json:"schema,omitempty"`
	Provider string          `json:"provider,omitempty"`
	Model    string          `json:"model,omitempty"`
	Effort   string          `json:"effort,omitempty"`
	MaxTurns int             `json:"maxTurns,omitempty"`
}

type ephemeralAnswer struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Text     string          `json:"text"`
	Output   json.RawMessage `json:"output"`
	Turns    int             `json:"turns"`
	Usage    struct {
		Input  int `json:"input"`
		Output int `json:"output"`
		Cost   struct {
			Total float64 `json:"total"`
		} `json:"cost"`
	} `json:"usage"`
}

func (h *Harness) Run(ctx context.Context, req harness.OneShotRequest) (harness.OneShotResult, error) {
	run := ephemeralRun{Prompt: req.Input, System: req.System, Schema: req.Schema, Effort: req.Effort, MaxTurns: req.Bound.MaxTurns}
	run.Provider, run.Model = agentdriver.SigmaModel(req.Model)
	var answer ephemeralAnswer
	if err := h.link.Call(ctx, "ephemeral/run", run, &answer); err != nil {
		return ephemeralFailure(err)
	}
	return harness.OneShotResult{
		Model:      qualified(answer.Provider, answer.Model),
		Text:       answer.Text,
		Structured: answer.Output,
		Usage:      harness.OneShotUsage{In: answer.Usage.Input, Out: answer.Usage.Output, CostUSD: answer.Usage.Cost.Total, Turns: answer.Turns},
	}, nil
}

func qualified(provider, model string) string {
	if provider == "" {
		return model
	}
	return provider + "/" + model
}

func ephemeralFailure(err error) (harness.OneShotResult, error) {
	var refused *effectrpc.Error
	if !errors.As(err, &refused) {
		return harness.OneShotResult{}, err
	}
	diagnostics := headless.ClassifyFailure(refused.Message)
	return harness.OneShotResult{Diagnostics: diagnostics, FailureOutput: refused.Message}, fmt.Errorf("%s: %s", diagnostics, refused.Message)
}
