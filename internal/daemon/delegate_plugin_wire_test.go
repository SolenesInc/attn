package daemon_test

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAPluginDelegateGivenOnlyAModelRunsAtTheDefaultEffortItsDriverCanPin(t *testing.T) {
	for _, row := range []struct {
		capabilities, wantEffort string
	}{
		{capabilities: "model_pin,effort_pin", wantEffort: "medium"},
		{capabilities: "model_pin", wantEffort: ""},
	} {
		t.Run(row.capabilities, func(t *testing.T) {
			t.Setenv(fakeagent.PlugCapabilitiesEnv, row.capabilities)
			w := newWorld(t, fakeagent.Plug)
			pluginDriverSettings(w.App(), "plug")
			cwd := w.Path("glm")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			request := brief(cwd, "Use the selected variant")
			request.Agent = protocol.Ptr("plug")
			request.Model = protocol.Ptr("spotify-glm/zai-org/GLM-5.2-FP8")
			result, err := w.Client().Delegate(request)
			if err != nil {
				t.Fatal(err)
			}
			argv := w.Launched(string(result.SessionID)).Argv
			model, _ := flagValue(argv, "--model")
			effort, _ := flagValue(argv, "--effort")
			if model != "spotify-glm/zai-org/GLM-5.2-FP8" || effort != row.wantEffort {
				t.Errorf("plug launched on %q at effort %q, want the chosen model at %q (argv %q)", model, effort, row.wantEffort, argv)
			}
		})
	}
}
