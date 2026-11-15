package daemon_test

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheAgentCatalogListsBuiltInAgentsAndThoseAConnectedPluginOffers(t *testing.T) {
	t.Setenv("ATTN_FAKE_PLUG_DISPLAY_NAME", "Example Agent")
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	t.Setenv("ATTN_CODEX_EXECUTABLE", "")
	nightly := w.Path("tools", "codex-nightly")
	if err := os.MkdirAll(w.Path("tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nightly, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	setSetting(t, app, "codex_executable", nightly)
	healthy := testworld.Request(app, protocol.ListPluginsMessage{Cmd: protocol.CmdListPlugins}, protocol.EventPluginsUpdated, func(m protocol.PluginsUpdatedMessage) bool {
		for _, plugin := range m.Plugins {
			if plugin.Name == "attn-plug" && protocol.Deref(plugin.HealthStatus) == agentdriver.HealthHealthy {
				return true
			}
		}
		return false
	})

	if got := pluginDriverSettings(app, "plug")["plug_display_name"]; got != "Example Agent" {
		t.Fatalf("settings display name = %q", got)
	}
	catalog := agentCatalog(t, w)
	codex := catalog["codex"]
	if codex.Executable != nightly || codex.Health != agentdriver.HealthHealthy || codex.Plugin != "" || !codex.ModelPin || !codex.EffortPin {
		t.Errorf("codex = %+v, want the configured executable, healthy, built in, with pins", codex)
	}
	if _, ok := catalog["claude"]; !ok {
		t.Errorf("the catalog is missing the built-in claude: %+v", catalog)
	}
	plug, ok := catalog["plug"]
	var plugHealth string
	for _, plugin := range healthy.Plugins {
		if plugin.Name == "attn-plug" {
			plugHealth = protocol.Deref(plugin.HealthMessage)
		}
	}
	if !ok || plug.DisplayName != "Example Agent" || plug.Plugin != "attn-plug" || plug.Health != agentdriver.HealthHealthy || plug.Detail != plugHealth || plug.ModelPin || plug.EffortPin {
		t.Errorf("plug = %+v, want offered by attn-plug, healthy with %q, without the pins the plugin did not declare", plug, plugHealth)
	}

	prefs := testworld.Request(app, protocol.DelegationPreferencesGetMessage{Cmd: protocol.CmdDelegationPreferencesGet, RequestID: "display-name"}, protocol.EventDelegationPreferencesResult, func(m protocol.DelegationPreferencesResultMessage) bool { return m.RequestID == "display-name" })
	found := false
	for _, harness := range prefs.Harnesses {
		if harness.ID == "plug" && harness.Name == "Example Agent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("picker catalog lost display name: %+v", prefs.Harnesses)
	}

	uninstalled := testworld.Request(app, protocol.UninstallPluginMessage{Cmd: protocol.CmdUninstallPlugin, Name: "attn-plug"},
		protocol.EventPluginActionResult, func(m protocol.PluginActionResultMessage) bool { return protocol.Deref(m.Name) == "attn-plug" })
	if !uninstalled.Success {
		t.Fatalf("uninstall attn-plug: %s", protocol.Deref(uninstalled.Error))
	}
	if plug, ok := agentCatalog(t, w)["plug"]; ok {
		t.Errorf("the catalog still offers plug after its plugin went away: %+v", plug)
	}
}

func agentCatalog(t *testing.T, w *world) map[string]agentdriver.Descriptor {
	t.Helper()
	response, err := http.Get("http://" + w.WSAddr + "/agents")
	if err != nil {
		t.Fatalf("GET /agents: %v", err)
	}
	defer response.Body.Close()
	var payload struct {
		Agents []agentdriver.Descriptor `json:"agents"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode /agents: %v", err)
	}
	byName := make(map[string]agentdriver.Descriptor, len(payload.Agents))
	for _, agent := range payload.Agents {
		byName[agent.Name] = agent
	}
	return byName
}
