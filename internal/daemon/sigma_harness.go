package daemon

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/appserver"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/transcript"
)

type sigmaHarness struct{ d *Daemon }

//go:embed sigma_logo.svg
var sigmaLogo string

func init() {
	harnessSources = append(harnessSources, func(d *Daemon) []harness.Harness { return []harness.Harness{sigmaHarness{d: d}} })
}

func (h sigmaHarness) link() *appserver.Link { return h.d.appServer.Link("sigma") }

func (sigmaHarness) driver() agentdriver.Driver { return agentdriver.MustGet("sigma") }

func (h sigmaHarness) Facts() harness.Facts {
	facts := builtinFacts(h.driver())
	facts.Logo = harness.Logo{View: 24, Body: sigmaLogo}
	facts.SkillDirs = h.driver().(*agentdriver.Sigma).SkillDirs
	facts.CacheTTL = time.Hour
	facts.Signals.Heartbeat = 500 * time.Millisecond
	facts.Recovery = harness.ByServer
	facts.ModelDiscovery = true
	facts.Activity = h.link().WatchesConversations()
	facts.ServerTerminal = serverTerminalOf(h.link())
	return facts
}

func (sigmaHarness) Terminal(harness.TerminalID, harness.TerminalEvent) {}

func (h sigmaHarness) Launch(_ context.Context, req harness.LaunchRequest) (harness.Command, error) {
	return h.d.launchOnAppServer(req, h.Facts().ID)
}

func (h sigmaHarness) Conversation(facts harness.SessionFacts) harness.Conversation {
	if l := h.link(); l != nil && facts.Lifetime == harness.InServer {
		return l.Conversation(facts)
	}
	return typedConversation{refusal: harness.NoCompaction(h.Facts().ID)}
}

func (h sigmaHarness) Plan(req harness.LaunchRequest) (harness.Planned, error) {
	planned := h.link().Plan("", req)
	planned.Conversation = resumedOrTerminal(req)
	return planned, nil
}

func (sigmaHarness) Start(context.Context) error { return nil }

func (h sigmaHarness) Recover(ctx context.Context, point harness.RecoverPoint) {
	h.link().Recover(ctx, point)
}

func (sigmaHarness) SettingChanged(string) {}

func (h sigmaHarness) Released(string) { h.link().Release() }

func (sigmaHarness) Stop() {}

func (h sigmaHarness) Conversations() harness.ConversationStore { return h }

func (h sigmaHarness) Admit(ref harness.ConversationRef) error {
	return h.link().Admit(ref)
}

func (h sigmaHarness) Check(_ context.Context, refs []harness.ConversationRef) map[harness.ConversationRef]harness.Availability {
	if l := h.link(); l.WatchesConversations() {
		return l.ConversationsResumable(refs)
	}
	return h.d.checkStoredConversations(h.driver(), refs)
}

func (sigmaHarness) Prepare(_ context.Context, ref harness.ConversationRef) (bool, error) {
	return ref.ID != "", nil
}

func (h sigmaHarness) End(_ context.Context, e harness.Ending) {
	if l := h.link(); l != nil {
		l.SessionClosed(e.Session, e.Profile, e.Released)
	}
}

type sigmaProvider struct {
	ID     string `json:"id"`
	Health struct {
		Status string `json:"status"`
	} `json:"health"`
	Ephemeral *bool `json:"ephemeral"`
}

func (h sigmaHarness) Models(ctx context.Context) (harness.Models, error) {
	server := h.link()
	if server == nil {
		return harness.NoModelDiscovery, nil
	}
	models, err := server.Models(ctx, true)
	if err != nil {
		return harness.Models{}, err
	}
	var providers []sigmaProvider
	if err := server.Call(ctx, "providers/list", appserver.RefreshParams(true), &providers); err != nil {
		return harness.Models{}, err
	}
	byID := map[string]sigmaProvider{}
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

func (h sigmaHarness) Health(context.Context) harness.Health {
	return h.d.executableHealth(h.Facts(), h.d.on(setting.SigmaEnabled))
}

func (h sigmaHarness) Page(ctx context.Context, ref harness.ConversationRef, cursor string, limit int) (transcript.EventPage, error) {
	return linkedConversations(h).Page(ctx, ref, cursor, limit)
}

func (h sigmaHarness) LastReply(ctx context.Context, ref harness.ConversationRef, max int) (string, error) {
	return linkedConversations(h).LastReply(ctx, ref, max)
}

func (h sigmaHarness) Window(ctx context.Context, ref harness.ConversationRef) (harness.AssistantWindow, bool) {
	return linkedConversations(h).Window(ctx, ref)
}

func (h sigmaHarness) Activity(ctx context.Context, ref harness.ConversationRef, kind, cursor string) (protocol.TranscriptActivityResult, error) {
	return linkedConversations(h).Activity(ctx, ref, kind, cursor)
}

func (h sigmaHarness) Version(ref harness.ConversationRef) string {
	return linkedConversations(h).Version(ref)
}

func (h sigmaHarness) Locate(ref harness.ConversationRef) string {
	return h.d.locateTranscript(h.driver(), ref)
}

func (h sigmaHarness) IDFromPath(path string) string {
	return agentdriver.ResumeSessionIDFromTranscriptPath(h.driver(), path)
}

type sigmaEphemeralRun struct {
	Prompt   string          `json:"prompt"`
	System   string          `json:"system,omitempty"`
	Schema   json.RawMessage `json:"schema,omitempty"`
	Provider string          `json:"provider,omitempty"`
	Model    string          `json:"model,omitempty"`
	Effort   string          `json:"effort,omitempty"`
	MaxTurns int             `json:"maxTurns,omitempty"`
}

type sigmaEphemeralAnswer struct {
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

func (h sigmaHarness) Run(ctx context.Context, req harness.OneShotRequest) (harness.OneShotResult, error) {
	run := sigmaEphemeralRun{Prompt: req.Input, System: req.System, Schema: req.Schema, Effort: req.Effort, MaxTurns: req.Bound.MaxTurns}
	run.Provider, run.Model = agentdriver.SigmaModel(req.Model)
	var answer sigmaEphemeralAnswer
	if err := h.link().Call(ctx, "ephemeral/run", run, &answer); err != nil {
		return sigmaEphemeralFailure(err)
	}
	return harness.OneShotResult{
		Model:      sigmaQualified(answer.Provider, answer.Model),
		Text:       answer.Text,
		Structured: answer.Output,
		Usage:      harness.OneShotUsage{In: answer.Usage.Input, Out: answer.Usage.Output, CostUSD: answer.Usage.Cost.Total, Turns: answer.Turns},
	}, nil
}

func sigmaQualified(provider, model string) string {
	if provider == "" {
		return model
	}
	return provider + "/" + model
}

func sigmaEphemeralFailure(err error) (harness.OneShotResult, error) {
	var refused *effectrpc.Error
	if !errors.As(err, &refused) {
		return harness.OneShotResult{}, err
	}
	diagnostics := headless.ClassifyFailure(refused.Message)
	return harness.OneShotResult{Diagnostics: diagnostics, FailureOutput: refused.Message}, fmt.Errorf("%s: %s", diagnostics, refused.Message)
}
