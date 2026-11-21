package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestOpenAndCalloutReachNothingInAnotherProfile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	home := w.Spawn(app, fakeagent.Claude, w.Path("home"))
	work := w.AppOn(createProfile(app, "Work").ID)
	stranger := w.Spawn(work, fakeagent.Claude, w.Path("work"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(home))}
	theirs := agentToolsCaller{w: w, bearer: terminalCredential(t, w, work.Terminal(stranger))}
	foreignSeed := narrowed[protocol.SeedPlantResult](t, theirs.mustCall(t, "seed_plant", map[string]any{"title": "Work's own"}).StructuredContent).ID
	if foreignSeed == "" {
		t.Fatal("the other profile's agent planted no seed")
	}
	pointAtStranger := map[string]any{"steps": []any{map[string]any{"anchor": "agent:" + stranger, "text": "Over there"}}}
	openForeignSeed := map[string]any{"seed": foreignSeed}

	refusedBy := func(surface, command, text, want string) {
		t.Helper()
		if !strings.Contains(text, want) {
			t.Errorf("%s %s reaching into another profile answered %q, want a refusal containing %q", surface, command, text, want)
		}
	}
	for _, tc := range []struct {
		tool, command string
		args          map[string]any
		want          string
	}{
		{"callout_show", "callout show", pointAtStranger, "is not an agent in your profile"},
		{"open", "open", openForeignSeed, "belongs to profile"},
	} {
		result := agent.mustCall(t, tc.tool, tc.args)
		if !result.IsError {
			t.Errorf("/mcp %s reached into another profile: %+v", tc.tool, result.StructuredContent)
		}
		refusedBy("/mcp", tc.tool, resultText(result), tc.want)

		var ignored map[string]any
		err := w.Client().RunCommand(tc.command, protocol.SessionID(home), tc.args, &ignored)
		if err == nil {
			t.Errorf("run_command %s reached into another profile: %+v", tc.command, ignored)
			continue
		}
		refusedBy("run_command", tc.command, err.Error(), tc.want)
	}
}
