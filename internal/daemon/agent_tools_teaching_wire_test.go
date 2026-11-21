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
	awaitDriverAvailable(app, "snipe")
	withoutTools := connectDriver(t, w, "plain-plugin", "plain", map[string]bool{"launch_instructions": true, "resume": true})
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

func TestASharedServerViewTakesTheServersTeachingOnlyWhileTheServerHoldsTheTools(t *testing.T) {
	w := newWorld(t, fakeagent.SampleServer)
	app := w.App()
	driver := registerToolsServerDriver(t, w, "launch_instructions")
	awaitDriverAvailable(app, serverPluginAgent)
	spawnToolsServerView(w, app, driver)
	server := w.AppServer(fakeagent.SampleServer)
	setSetting(t, app, "agent_tools_enabled", "true")
	server.AwaitMCPRegistration("attn", true)

	taught := func(view toolsServerView) bool {
		return strings.Contains(view.instructions, hooks.GardenGuidance()) && strings.Contains(view.instructions, seedCLITeaching)
	}
	if held := spawnToolsServerView(w, app, driver); held.tools == nil || taught(held) {
		t.Errorf("a view of a server holding attn's MCP server launched with agent_tools %+v and the garden's CLI teaching %t, want the tools and no teaching", held.tools, taught(held))
	}

	setSetting(t, app, "agent_tools_enabled", "false")
	server.AwaitMCPRegistration("attn", false)
	server.RefuseMCPRegistration("McpServerUnreachable")
	setSetting(t, app, "agent_tools_enabled", "true")
	if refused := spawnToolsServerView(w, app, driver); refused.tools != nil || !taught(refused) {
		t.Errorf("a view of a server that refused attn's MCP server launched with agent_tools %+v and the garden's CLI teaching %t, want no tools and the teaching:\n%s", refused.tools, taught(refused), refused.instructions)
	}
}
