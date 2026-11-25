package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/hooks"
)

const seedCLITeaching = "`attn seed standby`"

var gardenRulesNoToolTeaches = []string{
	"Never make the user decode an id.",
	"When the user uses one of those Jira words",
	"not in markdown TODO lists or your harness's todo tool",
	"Never plant an unverified suspicion:",
	"`--discovered-from <seed>`",
}

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
		for _, kept := range append([]string{agentGuidanceLead, "## What attn can do"}, gardenRulesNoToolTeaches...) {
			if !strings.Contains(text, kept) {
				t.Errorf("%s launched without %q, which no tool teaches yet", launch, kept)
			}
		}
		if header := strings.Contains(text, "| When you need to | Run | Reference |"); header == taught {
			t.Errorf("%s launched with the attn map's table header %t, want it only while a row of the table is left:\n%s", launch, header, text)
		}
		if viaTool := strings.Contains(text, "call `skill` with `reference` delegation"); viaTool != taught || strings.Contains(text, "attn skill --reference delegation") == taught {
			t.Errorf("%s points at the delegation reference through the tool: %t, want %t, and through the CLI only without the tools:\n%s", launch, viaTool, taught, text)
		}
		if dropped := !strings.Contains(text, "Use `attn agent msg` to reach sessions attn runs."); dropped != taught {
			t.Errorf("%s left out the agent and session families' CLI teaching: %t, want %t:\n%s", launch, dropped, taught, text)
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

const (
	askVisualsTeaching = "draw a compact `mermaid` code block"
	askInlineTeaching  = "take inline markdown only"
	askCLITeaching     = "Pipe the ask as markdown to `attn ask`"
)

func askSection(launch string) string {
	_, section, found := strings.Cut(launch, "## Asking the user")
	if !found {
		return ""
	}
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

func TestEveryLaunchWithAsksTeachesTheirRulesAndVisualsAndLeavesTheCLIToLaunchesWithoutTheTools(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex, fakeagent.Sigma)
	app := w.App()
	setSetting(t, app, "asks_enabled", "true")
	offerAppServer(t, w, app, fakeagent.Sigma)
	withTools := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"launch_instructions": true, "agent_tools": true, "resume": true})
	awaitDriverAvailable(app, "snipe")
	withoutTools := connectDriver(t, w, "plain-plugin", "plain", map[string]bool{"launch_instructions": true, "resume": true})
	awaitDriverAvailable(app, "plain")

	launches := func() map[string]string {
		_, snipe := spawnDriven(w, app, withTools, w.Path("shop"))
		_, plain := spawnDriven(w, app, withoutTools, w.Path("shop"))
		sigmaRun := w.Launched(w.Spawn(app, fakeagent.Sigma, w.Path("shop")))
		return map[string]string{
			"claude":                      launchGuidance(t, w, w.Launched(w.Spawn(app, fakeagent.Claude, w.Path("shop")))),
			"shared codex":                launchGuidance(t, w, w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("shop")))),
			"sigma":                       w.AppServer(fakeagent.Sigma).Started(sigmaRun.ConversationID).Instructions,
			"a plugin that takes tools":   snipe.Instructions.Content,
			"a plugin that takes no tool": plain.Instructions.Content,
		}
	}
	check := func(toolsOn bool) {
		t.Helper()
		for launch, text := range launches() {
			section := askSection(text)
			for _, rule := range []string{askGuidanceLead, askVisualsTeaching, askInlineTeaching} {
				if !strings.Contains(section, rule) {
					t.Errorf("with agent_tools_enabled %t, %s launched without the ask rule %q:\n%s", toolsOn, launch, rule, section)
				}
			}
			carried := toolsOn && launch != "a plugin that takes no tool"
			if taught := strings.Contains(section, askCLITeaching); taught == carried {
				t.Errorf("with agent_tools_enabled %t, %s launched with the ask CLI teaching %t, want it only without attn's tools:\n%s", toolsOn, launch, taught, section)
			}
			if !carried {
				continue
			}
			for _, cliWord := range []string{"attn ask", "--re", "--seed", "--help"} {
				if strings.Contains(section, cliWord) {
					t.Errorf("%s carries attn's tools, yet its ask guidance says the CLI word %q:\n%s", launch, cliWord, section)
				}
			}
		}
	}

	check(false)
	setSetting(t, app, "agent_tools_enabled", "true")
	w.AppServer(fakeagent.Sigma).AwaitMCPRegistration("attn", true)
	check(true)
}
