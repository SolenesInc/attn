package daemon

import (
	"context"
	"path/filepath"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

type codexHarness struct{ d *Daemon }

func init() {
	harnessSources = append(harnessSources, func(d *Daemon) []harness.Harness { return []harness.Harness{codexHarness{d: d}} })
}

func (codexHarness) driver() agentdriver.Driver { return agentdriver.MustGet("codex") }

func (h codexHarness) Facts() harness.Facts {
	facts := builtinFacts(h.driver())
	facts.SkillDirs = func(_ func(string) string, homeDir string) ([]string, error) {
		return []string{filepath.Join(homeDir, ".agents", "skills")}, nil
	}
	facts.CacheTTL = 30 * time.Minute
	facts.Tiers = map[modeltiers.Tier]harness.ModelChoice{modeltiers.Light: {Model: "gpt-6-luna", Effort: "low"}, modeltiers.Standard: {Model: "gpt-5.6-terra"}, modeltiers.Deep: {Model: "gpt-6.1-sol"}}
	facts.Advisor = harness.ModelChoice{Model: "gpt-5.6-luna", Effort: "xhigh"}
	facts.Signals.Heartbeat, facts.Signals.ReviewerDwell = 500*time.Millisecond, time.Minute
	facts.Signals.ConfirmsFirstTurn = true
	facts.ModelDiscovery = true
	facts.Activity = true
	facts.ServerTerminal = codexServerTerminal
	return facts
}

func (h codexHarness) Terminal(t harness.TerminalID, e harness.TerminalEvent) {
	r := h.d.codexShared()
	switch e := e.(type) {
	case harness.Exited:
		r.terminalExited(t)
	case harness.Dropped:
		r.terminalDropped(t)
	case harness.Handed:
		r.terminalMoved(t, e.Profile)
	case harness.Working:
		r.checkWorkingSoon(e.Session)
	case harness.Restarting:
		r.retireConnections(t)
	}
}

func (h codexHarness) Launch(_ context.Context, req harness.LaunchRequest) (harness.Command, error) {
	opts := h.d.launchSpawnOptions(req, h.Facts().ID)
	if err := h.d.refuseUnknownCodexFolder(&opts, req.Profile); err != nil {
		return harness.Command{}, err
	}
	if l := h.d.linkFor(opts.Agent, req.Lifetime); l != nil {
		if err := h.d.launchInLink(l, &opts, req); err != nil {
			return harness.Command{}, err
		}
	}
	command, err := h.d.launchWithDriver(req, &opts, false, nil)
	command.TitleSignals = h.Facts().Signals.Titles
	return command, err
}

func (h codexHarness) TrustFolder(dir string) error { return h.d.trustCodexFolder(dir) }

func (h codexHarness) Plan(req harness.LaunchRequest) (harness.Planned, error) {
	return planOn(h.d.codexShared(), req), nil
}

func (h codexHarness) Start(context.Context) error {
	h.d.registerHomeCron(homeCron{kind: codexUpdateCheckKind, interval: codexUpdateCheckInterval, handler: h.d.codexUpdateCheckHandler, runOnStart: true})
	return nil
}

func (h codexHarness) Recover(ctx context.Context, point harness.RecoverPoint) {
	switch point {
	case harness.RecoverProcesses:
		h.d.codexShared().recoverProcesses(ctx)
	case harness.RecoverViews:
		h.d.codexShared().recoverViews(ctx)
	}
}

func (h codexHarness) SettingChanged(key string) { h.d.codexShared().settingChanged(key) }

func (h codexHarness) Released(profile string) { h.d.codexShared().profileReleased(profile) }

func (codexHarness) Stop() {}

func (h codexHarness) Conversations() harness.ConversationStore { return h }

func (h codexHarness) Admit(ref harness.ConversationRef) error {
	if ref.Lifetime != harness.InServer {
		return nil
	}
	return h.d.codexShared().admit(ref.Profile, ref.ID)
}

func (h codexHarness) Check(_ context.Context, refs []harness.ConversationRef) map[harness.ConversationRef]harness.Availability {
	return h.d.checkStoredConversations(h.driver(), refs)
}

func (h codexHarness) Prepare(_ context.Context, ref harness.ConversationRef) (bool, error) {
	if ref.Lifetime == harness.InServer {
		if err := h.d.codexShared().restore(ref.Profile, ref.ID); err != nil {
			return false, err
		}
	}
	return h.d.conversationReady(h.driver(), ref.ID), nil
}

func (h codexHarness) End(_ context.Context, e harness.Ending) {
	h.d.codexShared().sessionClosed(e.Session, e.Profile, e.Released)
}

func (h codexHarness) Health(context.Context) harness.Health {
	return h.d.executableHealth(h.Facts(), true)
}

func (h codexHarness) Page(ctx context.Context, ref harness.ConversationRef, cursor string, limit int) (transcript.EventPage, error) {
	return fileConversations(h).Page(ctx, ref, cursor, limit)
}

func (h codexHarness) LastReply(ctx context.Context, ref harness.ConversationRef, max int) (string, error) {
	return fileConversations(h).LastReply(ctx, ref, max)
}

func (h codexHarness) Window(ctx context.Context, ref harness.ConversationRef) (harness.AssistantWindow, bool) {
	return fileConversations(h).Window(ctx, ref)
}

func (h codexHarness) Activity(ctx context.Context, ref harness.ConversationRef, kind, cursor string) (protocol.TranscriptActivityResult, error) {
	return fileConversations(h).Activity(ctx, ref, kind, cursor)
}
