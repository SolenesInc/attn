package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/conformance"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEveryHarnessAnswersTheContractWithItsFake(t *testing.T) {
	t.Setenv(fakeagent.PlugAgentEnv, "house")
	t.Setenv("ATTN_FAKE_PLUG_DISPLAY_NAME", "House")
	t.Setenv(fakeagent.PlugCapabilitiesEnv, "resume,model_pin,effort_pin,yolo,launch_instructions")
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
	conformance.Run(t, w.daemon.Harnesses(), []conformance.Row{
		{Facts: harness.Facts{ID: "claude", DisplayName: "Claude Code", Options: terminalOptions}, Offered: true},
		{Facts: harness.Facts{ID: "codex", DisplayName: "Codex", Options: terminalOptions}, Offered: true},
		{Facts: harness.Facts{ID: "sigma", DisplayName: "Sigma", Options: sigmaOptions}, Offered: true},
		{Facts: harness.Facts{ID: "house", DisplayName: "House", Options: harness.Options{Resume: true, Yolo: true, InitialPrompt: true, LaunchInstructions: true, ModelPin: true, EffortPin: true, Provider: true}}, Offered: true},
	})
}
