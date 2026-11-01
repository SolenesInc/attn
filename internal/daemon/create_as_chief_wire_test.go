package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAPluginAgentWithoutLaunchInstructionsCannotBeCreatedAsChief(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Plug)
	app := w.App()
	pluginDriverSettings(app, "plug")
	chief := w.Spawn(app, fakeagent.Claude, w.Path("chief"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
	w.Launched(chief)

	refused := refuseSpawnLikeTheApp(w, app, fakeagent.Plug, w.Path("plugin-chief"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
	if reason := protocol.Deref(refused.Error); !strings.Contains(reason, "launch_instructions") {
		t.Errorf("creating plug as chief was refused with %q, want it to name the missing launch_instructions capability", reason)
	}
	if chiefs := chiefsOf(w); !slices.Equal(chiefs, []string{chief}) {
		t.Fatalf("the chiefs are %v after the refused spawn, want %s to keep the role", chiefs, chief)
	}
}
