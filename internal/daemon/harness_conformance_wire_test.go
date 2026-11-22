package daemon_test

import (
	"os"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/conformance"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEveryHarnessAnswersTheContractWithItsFake(t *testing.T) {
	t.Setenv(fakeagent.PlugAgentEnv, "house")
	t.Setenv("ATTN_FAKE_PLUG_DISPLAY_NAME", "House")
	t.Setenv(fakeagent.PlugCapabilitiesEnv, "resume,model_pin,effort_pin,yolo,launch_instructions,state_reporting,pull_request_reporting")
	t.Setenv(fakeagent.PlugSkillDirsEnv, `[{"home_env":"HOUSE_HOME","default_home":".house"}]`)
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex, fakeagent.Sigma, fakeagent.Plug)
	app := w.App()
	setSetting(t, app, "sigma_enabled", "true")
	testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		for _, id := range []string{"claude", "codex", "sigma", "house"} {
			if m.Settings[id+"_available"] != "true" {
				return false
			}
		}
		return true
	})

	terminalOptions := harness.Options{Resume: true, Yolo: true, AutoApprove: true, AutomationExecutable: true, InitialPrompt: true, LaunchInstructions: true, ModelPin: true, EffortPin: true, ContextCap: true, Picker: true}
	codexOptions := terminalOptions
	codexOptions.AutomationExecutable = false
	sigmaOptions := terminalOptions
	sigmaOptions.ContextCap, sigmaOptions.Provider = false, true
	home := conformance.Home
	conformance.Run(t, w.daemon.Harnesses(), []conformance.Row{
		{
			Facts: harness.Facts{ID: "claude", DisplayName: "Claude Code", Logo: adapterLogo(t, 1200, "../harness/internal/claude/logo.svg"), Options: terminalOptions, Executable: harness.Executable{Default: "claude", Env: "ATTN_CLAUDE_EXECUTABLE"}, CacheTTL: time.Hour, Tiers: map[modeltiers.Tier]harness.ModelChoice{modeltiers.Light: {Model: "haiku"}, modeltiers.Standard: {Model: "sonnet"}, modeltiers.Deep: {Model: "opus"}}, Advisor: harness.ModelChoice{Model: "sonnet", Effort: "medium"}, CrewModel: "fable", ModelDiscovery: true, Activity: true, ConversationIsSession: true,
				Signals: harness.Signals{TurnStarts: harness.TurnStartsReported, ReportsTools: true, Heartbeat: 1500 * time.Millisecond, Titles: harness.TitlesClaude, StopsReadable: true, StopsClassified: true}},
			SkillDirs: []string{home + "/.claude/skills"},
			Offered:   true,
			OneShot:   true,
		},
		{
			Facts: harness.Facts{ID: "codex", DisplayName: "Codex", Logo: adapterLogo(t, 24, "../harness/internal/codex/logo.svg"), Options: codexOptions, Executable: harness.Executable{Default: "codex", Env: "ATTN_CODEX_EXECUTABLE"}, CacheTTL: 30 * time.Minute, Tiers: map[modeltiers.Tier]harness.ModelChoice{modeltiers.Light: {Model: "gpt-6-luna", Effort: "low"}, modeltiers.Standard: {Model: "gpt-5.6-terra"}, modeltiers.Deep: {Model: "gpt-6.1-sol"}}, Advisor: harness.ModelChoice{Model: "gpt-5.6-luna", Effort: "xhigh"}, ModelDiscovery: true, Activity: true, ConversationIsSession: true,
				Caller:  harness.CallerEnv{Scope: "ATTN_CODEX_SHARED_PROFILE", Conversation: "CODEX_THREAD_ID"},
				Signals: harness.Signals{TurnStarts: harness.TurnStartsReported, ReportsTools: true, Heartbeat: 500 * time.Millisecond, ReviewerDwell: time.Minute, ConfirmsFirstTurn: true, Titles: harness.TitlesCodex, StopsReadable: true, StopsClassified: true, ForgetsRecoveredClaim: true}},
			SkillDirs:      []string{home + "/.agents/skills"},
			ServerTerminal: "codex-server-6c1d6ced",
			Offered:        true,
			OneShot:        true,
		},
		{
			Facts:          harness.Facts{ID: "sigma", DisplayName: "Sigma", Logo: adapterLogo(t, 24, "../harness/internal/sigma/logo.svg"), Options: sigmaOptions, Executable: harness.Executable{Default: "sigma", Env: "ATTN_SIGMA_EXECUTABLE"}, CacheTTL: time.Hour, Signals: harness.Signals{Heartbeat: 500 * time.Millisecond}, Recovery: harness.ByServer, ModelDiscovery: true, Activity: true},
			SkillDirs:      []string{home + "/.sigma/skills", home + "/.claude/skills"},
			ServerTerminal: "sigma-server",
			Offered:        true,
			OneShot:        true,
		},
		{
			Facts: harness.Facts{ID: "house", DisplayName: "House", Options: harness.Options{Resume: true, Yolo: true, InitialPrompt: true, LaunchInstructions: true, ModelPin: true, EffortPin: true, Provider: true}, CacheTTL: time.Hour,
				Signals: harness.Signals{StateAuthority: harness.StateByHarness, ReportsTools: true, Heartbeat: 500 * time.Millisecond, ReviewerDwell: time.Minute, StopsReadable: true, StopsClassified: true}, Owner: "attn-plug"},
			SkillDirs: []string{home + "/.house/skills"},
			Offered:   true,
		},
	})
}

func adapterLogo(t *testing.T, view float64, file string) harness.Logo {
	t.Helper()
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return harness.Logo{View: view, Body: string(body)}
}
