package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnOutpostRefusesAPluginLaunchWhenAnotherPluginRegistersTheAgentThere(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Plug))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Plug))
	outpost.NamePlugPlugin("other-plug")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	awaitAgentAvailable(pair.App, fakeagent.Plug)
	awaitAgentAvailable(outpost.App(), fakeagent.Plug)

	result, _, _ := home.RequestSpawn(pair.App, fakeagent.Plug, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	if want := `agent "plug" here is provided by plugin "other-plug", not "attn-plug"`; result.Success || !strings.Contains(protocol.Deref(result.Error), want) {
		t.Fatalf("spawn onto the outpost = success %v error %q, want refused with %q", result.Success, protocol.Deref(result.Error), want)
	}
	t.Logf("the home planned plug with attn-plug; the outpost, where other-plug registers plug, refused: %s", protocol.Deref(result.Error))
}
