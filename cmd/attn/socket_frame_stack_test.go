package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestLargeSeedEditsAndEscapedVentsCrossTheOutpostBridge(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	turnOnAnalytics(t, pair.App)
	source := home.Spawn(pair.App, fakeagent.Claude, outpost.Path("source"), func(m *protocol.SpawnSessionMessage) {
		m.Host = &pair.Host
		m.InitialPrompt = protocol.Ptr("check large requests")
	})
	agent := outpost.LaunchedCarrying(pair.App.Terminal(source))
	agent.Prompted()
	terminal := pair.App.Terminal(source)
	var seed protocol.Seed
	created := outpost.Run(testworld.Invocation{Terminal: terminal, Args: []string{"seed", "plant", "large remote body", "-m", "Original body.", "--json"}})
	if created.Code != 0 {
		t.Fatalf("plant from outpost: %s", created.Stderr)
	}
	created.JSON(t, &seed)
	body := strings.Repeat("body\n", 64*1024)
	exported := outpost.Run(testworld.Invocation{Terminal: terminal, Args: []string{"seed", "export", seed.ID, "--out", "-"}})
	if exported.Code != 0 {
		t.Fatalf("export from outpost: %s", exported.Stderr)
	}
	stamp, _, _ := strings.Cut(exported.Stdout, "\n")
	edited := outpost.Run(testworld.Invocation{Terminal: terminal, Args: []string{"seed", "edit", seed.ID, "-m", "-", "--json"}, Stdin: stamp + "\n" + body})
	if edited.Code != 0 {
		t.Fatalf("edit %d body bytes from outpost: %s", len(body), edited.Stderr)
	}
	var result protocol.SeedEditResult
	edited.JSON(t, &result)
	seed = result.Seed
	shown := showSeed(t, home, source, seed.ID)
	if seed.Body != body || shown.Seed.Body != body {
		t.Fatal("the forwarded edit did not reach home intact")
	}
	t.Logf("outpost seed_edit round-tripped %d body bytes to home", len(body))
	vent := strings.Repeat("<", analytics.VentTextLimit)
	recorded := outpost.Run(testworld.Invocation{Terminal: terminal, Args: []string{"vent", "--about", "attn"}, Stdin: vent})
	if recorded.Code != 0 || !ventRecorded.MatchString(recorded.Stdout) {
		t.Fatalf("vent %d escaped bytes from outpost: %+v", len(vent)*6, recorded)
	}
	updated := testworld.Await(pair.App, protocol.EventVentsUpdated, func(m protocol.VentsUpdatedMessage) bool { return len(m.Vents) == 1 })
	if updated.Vents[0].Text != vent {
		t.Fatal("the forwarded vent did not reach home intact")
	}
	t.Logf("outpost vent round-tripped %d text bytes (%d escaped bytes)", len(vent), len(vent)*6)
}
