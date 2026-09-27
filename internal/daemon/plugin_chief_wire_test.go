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
