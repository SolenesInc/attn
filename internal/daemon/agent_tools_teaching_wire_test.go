package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/hooks"
)

const seedCLITeaching = "`attn seed standby`"

func TestALaunchThatCarriesTheToolsLeavesTheirFamiliesCLITeachingToTheServer(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app := w.App()
	both := map[string]bool{"launch_instructions": true, "agent_tools": true, "resume": true}
	withTools := connectDriver(t, w, "snipe-plugin", "snipe", both)
	withoutTools := connectDriver(t, w, "plain-plugin", "plain", map[string]bool{"launch_instructions": true, "resume": true})
	awaitDriverAvailable(app, "snipe")
	awaitDriverAvailable(app, "plain")

	guidance := func() map[string]string {
		_, snipe := spawnDriven(w, app, withTools, w.Path("shop"))
		_, plain := spawnDriven(w, app, withoutTools, w.Path("shop"))
		return map[string]string{
			"claude":                      launchGuidance(t, w, w.Launched(w.Spawn(app, fakeagent.Claude, w.Path("shop")))),
			"shared codex":                launchGuidance(t, w, w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("shop")))),
			"a plugin that takes tools":   snipe.Instructions.Content,
			"a plugin that takes no tool": plain.Instructions.Content,
		}
	}

	for launch, text := range guidance() {
		if !strings.Contains(text, hooks.GardenGuidance()) || !strings.Contains(text, seedCLITeaching) {
			t.Errorf("with agent_tools_enabled off, %s launched without the garden's CLI teaching:\n%s", launch, text)
		}
	}

	setSetting(t, app, "agent_tools_enabled", "true")
	for launch, text := range guidance() {
		taught := launch != "a plugin that takes no tool"
		if dropped := !strings.Contains(text, hooks.GardenGuidance()) && !strings.Contains(text, seedCLITeaching); dropped != taught {
			t.Errorf("%s left out the seed family's CLI teaching: %t, want %t, because its launch carries the tools: %t:\n%s", launch, dropped, taught, taught, text)
		}
		for _, kept := range []string{agentGuidanceLead, "`attn agent msg`", "## What attn can do"} {
			if !strings.Contains(text, kept) {
				t.Errorf("%s launched without %q, which no tool teaches yet", launch, kept)
			}
		}
	}
}
