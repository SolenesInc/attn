package daemon_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestEverySettingsToolAnswersWithTheShapeItsOutputSchemaDescribes(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(w.Spawn(app, fakeagent.Claude, w.Path("shop"))))}
	tools := &schemaCheckedTools{t: t, agent: agent, tools: listOfferedTools(t, agent), called: map[string]bool{}}

	if set := tools.call("settings_set", map[string]any{"key": "theme", "value": "dark"}); set["key"] != "theme" || set["value"] != "dark" {
		t.Errorf("settings_set answered %v, want theme set to dark", set)
	}
	if got := tools.call("settings_get", map[string]any{"key": "theme"}); got["value"] != "dark" {
		t.Errorf("settings_get answered %v, want dark", got)
	}
	listed := tools.call("settings_list", map[string]any{"all": true})
	if !strings.Contains(fmt.Sprint(listed["entries"]), "pty_backend_mode") {
		t.Errorf("settings_list with all answered %v, want the read-only values too", listed)
	}
	tools.report("settings")
}

func TestASettingsCallerReadsAndChangesNothingOfAnotherProfile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	const key = "garden.tender_resume"
	if _, err := w.Client().SetSetting(client.SettingsCaller{Profile: "Default"}, key, "watches"); err != nil {
		t.Fatal(err)
	}
	sideApp := w.AppOn(createProfile(app, "Side").ID)
	side := w.Spawn(sideApp, fakeagent.Claude, w.Path("side"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, sideApp.Terminal(side))}

	surfaces := map[string]func(command string, args map[string]any) (map[string]any, error){
		"/mcp": func(command string, args map[string]any) (map[string]any, error) {
			result := agent.mustCall(t, strings.ReplaceAll(command, " ", "_"), args)
			if result.IsError {
				return nil, fmt.Errorf("%s", resultText(result))
			}
			answer, _ := result.StructuredContent.(map[string]any)
			return answer, nil
		},
		"run_command": func(command string, args map[string]any) (map[string]any, error) {
			var answer map[string]any
			err := w.Client().RunCommand(command, protocol.SessionID(side), args, &answer)
			return answer, err
		},
	}
	for surface, call := range surfaces {
		if got, err := call("settings get", map[string]any{"key": key}); err != nil || got["value"] != "" {
			t.Errorf("%s settings get %s from Side answered %v, %v, want Side's own empty value, not Default's", surface, key, got, err)
		}
		if _, err := call("settings set", map[string]any{"key": key, "value": "off"}); err != nil {
			t.Fatalf("%s settings set from Side: %v", surface, err)
		}
		listed, err := call("settings list", map[string]any{})
		if err != nil {
			t.Fatalf("%s settings list from Side: %v", surface, err)
		}
		if listed["profile"] != "Side" || !strings.Contains(fmt.Sprint(listed["entries"]), "key:"+key+" scope:profile value:off") {
			t.Errorf("%s settings list from Side answered %v, want Side's profile with %s off", surface, listed, key)
		}
		if _, err := call("settings set", map[string]any{"key": key, "value": ""}); err != nil {
			t.Fatalf("%s settings set from Side: %v", surface, err)
		}
	}
	defaults, err := w.Client().Settings(client.SettingsCaller{Profile: "Default"}, key, false)
	if err != nil || protocol.Deref(defaults.Entries[0].Value) != "watches" {
		t.Fatalf("Side's calls changed Default's %s: %+v, %v, want watches", key, defaults, err)
	}
}
