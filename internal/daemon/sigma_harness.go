package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/transcript"
)

type sigmaHarness struct{ d *Daemon }

func init() {
	harnessSources = append(harnessSources, func(d *Daemon) []harness.Harness { return []harness.Harness{sigmaHarness{d: d}} })
}

func (sigmaHarness) driver() agentdriver.Driver { return agentdriver.MustGet("sigma") }

func (h sigmaHarness) Facts() harness.Facts {
	facts := builtinFacts(h.driver())
	facts.SkillDirs = h.driver().(*agentdriver.Sigma).SkillDirs
	facts.CacheTTL = time.Hour
	facts.Signals.Heartbeat = 500 * time.Millisecond
	facts.Recovery = harness.ByServer
	facts.ModelDiscovery = true
	facts.Tiers = map[modeltiers.Tier]harness.ModelChoice{modeltiers.Light: {Model: "openai/gpt-6-luna", Effort: "low"}, modeltiers.Standard: {Model: "openai/gpt-5.6-terra"}, modeltiers.Deep: {Model: "openai/gpt-6.1-sol"}}
	facts.Advisor = harness.ModelChoice{Model: "openai/gpt-5.6-luna", Effort: "xhigh"}
	facts.Activity = h.d.serverLinkFor(facts.ID).watchesConversations()
	facts.ServerTerminal = h.d.serverTerminalOf(facts.ID)
	return facts
}

func (h sigmaHarness) OwnsCredential(subject string) bool {
	return h.d.appServer().OwnsCredential(subject)
}

func (h sigmaHarness) Authenticate(subject string, header http.Header) (harness.SessionID, string, bool, error) {
	return h.d.appServer().Authenticate(subject, header)
}

func (h sigmaHarness) IdentifyCall(subject string, meta json.RawMessage) (harness.SessionID, string, *harness.Refusal) {
	return h.d.appServer().IdentifyCall(subject, meta)
}

func (h sigmaHarness) ResolveCaller(caller string) (harness.SessionID, harness.TerminalID, string, bool) {
	return h.d.appServer().ResolveCaller(caller)
}

func (h sigmaHarness) Terminal(t harness.TerminalID, e harness.TerminalEvent) {
	h.d.appServer().terminal(t, e)
}

func (h sigmaHarness) Launch(_ context.Context, req harness.LaunchRequest) (harness.Command, error) {
	return h.d.launchOnServerOrTerminal(req, h.Facts().ID)
}

func (h sigmaHarness) Plan(req harness.LaunchRequest) (harness.Planned, error) {
	planned := h.d.planOnServer(h.Facts().ID, "", req)
	planned.Conversation = resumedOrTerminal(req)
	return planned, nil
}

func (sigmaHarness) Start(context.Context) error { return nil }

func (h sigmaHarness) Recover(ctx context.Context, point harness.RecoverPoint) {
	h.d.recoverServer(ctx, h.Facts().ID, point)
}

func (h sigmaHarness) SettingChanged(key string) {
	if key == setting.AgentToolsEnabled.Key {
		h.d.life.Go("reconcileAgentTools", h.d.reconcileAgentTools)
	}
}

func (h sigmaHarness) Released(string) { h.d.releaseServer(h.Facts().ID) }

func (sigmaHarness) Stop() {}

func (h sigmaHarness) Conversations() harness.ConversationStore { return h }

func (h sigmaHarness) Admit(ref harness.ConversationRef) error {
	return h.d.admitToServer(h.Facts().ID, ref)
}

func (h sigmaHarness) Check(_ context.Context, refs []harness.ConversationRef) map[harness.ConversationRef]harness.Availability {
	if l := h.d.serverLinkFor(h.Facts().ID); l.watchesConversations() {
		return l.conversationsResumable(refs)
	}
	return h.d.checkStoredConversations(h.driver(), refs)
}

func (sigmaHarness) Prepare(_ context.Context, ref harness.ConversationRef) (bool, error) {
	return ref.ID != "", nil
}

func (h sigmaHarness) End(_ context.Context, e harness.Ending) {
	if l := h.d.serverLinkFor(h.Facts().ID); l != nil {
		l.sessionClosed(e.Session, e.Profile, e.Released)
	}
}

func (h sigmaHarness) Models(ctx context.Context) (harness.Models, error) {
	if server := h.d.serverLinkFor(h.Facts().ID); server != nil {
		return server.harnessModels(ctx)
	}
	return noModelDiscovery, nil
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
	Text   string          `json:"text"`
	Output json.RawMessage `json:"output"`
	Turns  int             `json:"turns"`
	Usage  struct {
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
	link := h.d.serverLinkFor(h.driver().Name())
	connection, err := link.connection()
	if err != nil {
		return harness.OneShotResult{}, err
	}
	client, release, err := connection.borrow(ctx, link, true)
	if err != nil {
		return harness.OneShotResult{}, err
	}
	defer release()
	var answer sigmaEphemeralAnswer
	if err := client.Call(ctx, "ephemeral/run", run, &answer); err != nil {
		return sigmaEphemeralFailure(err)
	}
	return harness.OneShotResult{
		Text:       answer.Text,
		Structured: answer.Output,
		Usage:      harness.OneShotUsage{In: answer.Usage.Input, Out: answer.Usage.Output, CostUSD: answer.Usage.Cost.Total, Turns: answer.Turns},
	}, nil
}

func sigmaEphemeralFailure(err error) (harness.OneShotResult, error) {
	var refused *effectrpc.Error
	if !errors.As(err, &refused) {
		return harness.OneShotResult{}, err
	}
	diagnostics := headless.ClassifyFailure(refused.Message)
	return harness.OneShotResult{Diagnostics: diagnostics, FailureOutput: refused.Message}, fmt.Errorf("%s: %s", diagnostics, refused.Message)
}
