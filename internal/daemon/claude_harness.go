package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

type claudeHarness struct{ d *Daemon }

func init() {
	harnessSources = append(harnessSources, func(d *Daemon) []harness.Harness { return []harness.Harness{claudeHarness{d: d}} })
}

func (claudeHarness) driver() agentdriver.Driver { return agentdriver.MustGet("claude") }

func (h claudeHarness) Facts() harness.Facts {
	facts := builtinFacts(h.driver())
	facts.SkillDirs = func(_ func(string) string, homeDir string) ([]string, error) {
		return []string{filepath.Join(homeDir, ".claude", "skills")}, nil
	}
	facts.CacheTTL = time.Hour
	facts.Tiers = map[modeltiers.Tier]harness.ModelChoice{modeltiers.Light: {Model: "haiku"}, modeltiers.Standard: {Model: "sonnet"}, modeltiers.Deep: {Model: "opus"}}
	facts.Advisor = harness.ModelChoice{Model: "sonnet", Effort: "medium"}
	facts.CrewModel = "fable"
	facts.ModelDiscovery = true
	facts.Activity = true
	facts.Signals.Heartbeat = 1500 * time.Millisecond
	return facts
}

func (h claudeHarness) Reach(_ context.Context, s harness.SessionFacts) harness.Reach {
	return harness.Reach{Up: h.d.claudeModReaches(s.Session)}
}

func (claudeHarness) Terminal(harness.TerminalID, harness.TerminalEvent) {}

func (claudeHarness) End(context.Context, harness.Ending) {}

func (h claudeHarness) Launch(_ context.Context, req harness.LaunchRequest) (harness.Command, error) {
	opts := h.d.launchSpawnOptions(req, h.Facts().ID)
	if err := h.d.settleClaudeFolderTrust(&opts, req.Trust); err != nil {
		return harness.Command{}, err
	}
	command, err := h.d.launchWithDriver(req, &opts, false, nil)
	command.TitleSignals = h.Facts().Signals.Titles
	return command, err
}

func (claudeHarness) TrustFolder(dir string) error { return trustClaudeFolder(claudeConfigPath(), dir) }

func (claudeHarness) Plan(harness.LaunchRequest) (harness.Planned, error) {
	return harness.Planned{Lifetime: harness.InTerminal}, nil
}

func (h claudeHarness) Start(context.Context) error {
	if h.d.noAgentSockets {
		return nil
	}
	if err := h.d.claudeMod().serve(); err != nil {
		return fmt.Errorf("claude mod: live sessions are typed into: %w", err)
	}
	return nil
}

func (h claudeHarness) Recover(_ context.Context, point harness.RecoverPoint) {
	if point == harness.RecoverMonitors {
		h.d.claudeMod().expectModsToReconnect()
	}
}

func (claudeHarness) SettingChanged(string) {}

func (claudeHarness) Released(string) {}

func (h claudeHarness) Stop() { h.d.claudeMod().close() }

func (h claudeHarness) Conversations() harness.ConversationStore { return h }

func (claudeHarness) Admit(harness.ConversationRef) error { return nil }

func (h claudeHarness) Check(_ context.Context, refs []harness.ConversationRef) map[harness.ConversationRef]harness.Availability {
	return h.d.checkStoredConversations(h.driver(), refs)
}

func (h claudeHarness) Prepare(_ context.Context, ref harness.ConversationRef) (bool, error) {
	return h.d.conversationReady(h.driver(), ref.ID), nil
}

func (h claudeHarness) Health(context.Context) harness.Health {
	return h.d.executableHealth(h.Facts(), true)
}

func (h claudeHarness) Page(ctx context.Context, ref harness.ConversationRef, cursor string, limit int) (transcript.EventPage, error) {
	return fileConversations(h).Page(ctx, ref, cursor, limit)
}

func (h claudeHarness) LastReply(ctx context.Context, ref harness.ConversationRef, max int) (string, error) {
	return fileConversations(h).LastReply(ctx, ref, max)
}

func (h claudeHarness) Window(ctx context.Context, ref harness.ConversationRef) (harness.AssistantWindow, bool) {
	return fileConversations(h).Window(ctx, ref)
}

func (h claudeHarness) Activity(ctx context.Context, ref harness.ConversationRef, kind, cursor string) (protocol.TranscriptActivityResult, error) {
	return fileConversations(h).Activity(ctx, ref, kind, cursor)
}

func (h claudeHarness) Version(ref harness.ConversationRef) string {
	return fileConversations(h).version(ref, transcript.ClaudeVersion)
}

func (h claudeHarness) Locate(ref harness.ConversationRef) string {
	return h.d.locateTranscript(h.driver(), ref)
}

func (h claudeHarness) IDFromPath(path string) string {
	return agentdriver.ResumeSessionIDFromTranscriptPath(h.driver(), path)
}
