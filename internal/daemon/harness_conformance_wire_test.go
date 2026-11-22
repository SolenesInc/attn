package daemon_test

import (
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
	t.Setenv(fakeagent.PlugCapabilitiesEnv, "resume,model_pin,effort_pin,yolo,launch_instructions")
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

	terminalOptions := harness.Options{Resume: true, Yolo: true, AutoApprove: true, InitialPrompt: true, LaunchInstructions: true, ModelPin: true, EffortPin: true, ContextCap: true, Picker: true}
	sigmaOptions := terminalOptions
	sigmaOptions.ContextCap, sigmaOptions.Provider = false, true
	home := conformance.Home
	conformance.Run(t, w.daemon.Harnesses(), []conformance.Row{
		{
			Facts:     harness.Facts{ID: "claude", DisplayName: "Claude Code", Options: terminalOptions, Executable: harness.Executable{Default: "claude", Env: "ATTN_CLAUDE_EXECUTABLE"}, CacheTTL: time.Hour, Tiers: map[modeltiers.Tier]string{modeltiers.Light: "haiku", modeltiers.Standard: "sonnet", modeltiers.Deep: "opus"}, CrewModel: "fable"},
			SkillDirs: []string{home + "/.claude/skills"},
			Offered:   true,
		},
		{
			Facts:     harness.Facts{ID: "codex", DisplayName: "Codex", Options: terminalOptions, Executable: harness.Executable{Default: "codex", Env: "ATTN_CODEX_EXECUTABLE"}, CacheTTL: 30 * time.Minute, Tiers: map[modeltiers.Tier]string{modeltiers.Light: "gpt-6-luna", modeltiers.Standard: "gpt-5.6-terra", modeltiers.Deep: "gpt-6.1-sol"}},
			SkillDirs: []string{home + "/.agents/skills"},
			Offered:   true,
		},
		{
			Facts:     harness.Facts{ID: "sigma", DisplayName: "Sigma", Options: sigmaOptions, Executable: harness.Executable{Default: "sigma", Env: "ATTN_SIGMA_EXECUTABLE"}, CacheTTL: time.Hour},
			SkillDirs: []string{home + "/.sigma/skills", home + "/.claude/skills"},
			Offered:   true,
		},
		{
			Facts:     harness.Facts{ID: "house", DisplayName: "House", Options: harness.Options{Resume: true, Yolo: true, InitialPrompt: true, LaunchInstructions: true, ModelPin: true, EffortPin: true, Provider: true}, CacheTTL: time.Hour},
			SkillDirs: []string{home + "/.house/skills"},
			Offered:   true,
		},
	})
}
