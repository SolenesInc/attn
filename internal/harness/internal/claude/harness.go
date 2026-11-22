package claude

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

type claudeHarness struct {
	host harness.Host
	mod  *claudeMod
}

//go:embed logo.svg
var claudeLogo string

// New is the Claude Code harness, reaching attn through host.
func New(host harness.Host) harness.Harness { return newClaudeHarness(host) }

func newClaudeHarness(host harness.Host) claudeHarness {
	return claudeHarness{host: host, mod: newClaudeMod(host)}
}

func claudeDriver() agentdriver.Driver { return agentdriver.MustGet("claude") }

func (claudeHarness) driver() agentdriver.Driver { return claudeDriver() }

func (claudeHarness) Facts() harness.Facts { return claudeFacts() }

func claudeFacts() harness.Facts {
	facts := agentdriver.Facts(claudeDriver())
	facts.Logo = harness.Logo{View: 1200, Body: claudeLogo}
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
	return harness.Reach{Up: h.mod.reachable(s.Terminal)}
}

func (claudeHarness) Terminal(harness.TerminalID, harness.TerminalEvent) {}

func (claudeHarness) End(context.Context, harness.Ending) {}

func (claudeHarness) TrustFolder(dir string) error { return trustClaudeFolder(claudeConfigPath(), dir) }

func (claudeHarness) Plan(req harness.LaunchRequest) (harness.Planned, error) {
	conversation := harness.ResumedOrTerminal(req)
	if req.Conversation == "" && req.Picker {
		conversation = harness.ConversationCleared
	}
	return harness.Planned{Lifetime: harness.InTerminal, Conversation: conversation}, nil
}

func (h claudeHarness) Start(context.Context) error {
	if !h.host.AgentSockets() {
		return nil
	}
	if err := h.mod.serve(); err != nil {
		return fmt.Errorf("claude mod: live sessions are typed into: %w", err)
	}
	return nil
}

func (h claudeHarness) Recover(_ context.Context, point harness.RecoverPoint) {
	if point == harness.RecoverMonitors {
		h.mod.expectModsToReconnect()
	}
}

func (claudeHarness) SettingChanged(string) {}

func (claudeHarness) Released(string) {}

func (h claudeHarness) Stop() { h.mod.close() }

func (h claudeHarness) Conversations() harness.ConversationStore { return h }

func (claudeHarness) Admit(harness.ConversationRef) error { return nil }

func (h claudeHarness) resumable(id string) bool { return agentdriver.ResumeAvailable(h.driver(), id) }

func (h claudeHarness) Check(_ context.Context, refs []harness.ConversationRef) map[harness.ConversationRef]harness.Availability {
	return h.host.Files().Check(refs, h.resumable)
}

func (h claudeHarness) Prepare(_ context.Context, ref harness.ConversationRef) (bool, error) {
	return h.host.Files().Ready(ref, h.resumable), nil
}

func (h claudeHarness) Health(context.Context) harness.Health {
	return h.host.ExecutableHealth(h.Facts(), true)
}

func (h claudeHarness) Page(ctx context.Context, ref harness.ConversationRef, cursor string, limit int) (transcript.EventPage, error) {
	return h.host.Files().Page(ctx, ref, cursor, limit)
}

func (h claudeHarness) LastReply(ctx context.Context, ref harness.ConversationRef, max int) (string, error) {
	return h.host.Files().LastReply(ctx, ref, max)
}

func (h claudeHarness) Window(ctx context.Context, ref harness.ConversationRef) (harness.AssistantWindow, bool) {
	return h.host.Files().Window(ctx, ref)
}

func (h claudeHarness) Activity(ctx context.Context, ref harness.ConversationRef, kind, cursor string) (protocol.TranscriptActivityResult, error) {
	return h.host.Files().Activity(ctx, ref, kind, cursor)
}

func (h claudeHarness) Version(ref harness.ConversationRef) string {
	path := h.host.Files().Path(ref)
	if path == "" {
		return ""
	}
	return transcript.ClaudeVersion(path)
}

func (h claudeHarness) Locate(ref harness.ConversationRef) string {
	if ref.Path != "" {
		return ref.Path
	}
	id := strings.TrimSpace(ref.ID)
	finder, ok := agentdriver.GetTranscriptFinder(h.driver())
	if id == "" || !ok {
		return ""
	}
	return strings.TrimSpace(finder.FindTranscriptForResume(id))
}

func (h claudeHarness) IDFromPath(path string) string {
	return agentdriver.ResumeSessionIDFromTranscriptPath(h.driver(), path)
}
