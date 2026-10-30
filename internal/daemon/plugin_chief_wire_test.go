package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAPluginAgentThatCannotResumeIsRefusedAsChief(t *testing.T) {
	t.Setenv(fakeagent.PiCapabilitiesEnv, "launch_instructions,resume=false")
	w := newWorld(t, fakeagent.Pi)
	app := w.App()
	pluginDriverSettings(app, "pi")
	refused := refuseSpawnLikeTheApp(w, app, fakeagent.Pi, w.Path("chief"), func(m *protocol.SpawnSessionMessage) {
		m.ChiefOfStaff = protocol.Ptr(true)
	})
	if !strings.Contains(protocol.Deref(refused.Error), "resume capability") {
		t.Errorf("the chief spawn was refused with %q, want it naming the missing resume capability", protocol.Deref(refused.Error))
	}
}

func TestMarkingAPluginAgentChiefNeedsLaunchInstructionsAndResume(t *testing.T) {
	for _, row := range []struct{ name, capabilities, missing string }{
		{name: "without launch instructions", capabilities: "", missing: "launch_instructions"},
		{name: "without resume", capabilities: "launch_instructions,resume=false", missing: "resume capability"},
	} {
		t.Run(row.name, func(t *testing.T) {
			if row.capabilities != "" {
				t.Setenv(fakeagent.PiCapabilitiesEnv, row.capabilities)
			}
			w := newWorld(t, fakeagent.Pi)
			app := w.App()
			pluginDriverSettings(app, "pi")
			pi := w.Spawn(app, fakeagent.Pi, w.Path("pi"))
			w.Launched(pi)

			refused := setChiefOfStaff(app, pi, true)
			if refused.Success || !strings.Contains(protocol.Deref(refused.Error), row.missing) {
				t.Errorf("marking pi the chief = %+v, want a refusal naming %s", refused, row.missing)
			}
			if chiefs := chiefsOf(w); len(chiefs) != 0 {
				t.Errorf("the chiefs are %v after the refusal, want none", chiefs)
			}
		})
	}
}
