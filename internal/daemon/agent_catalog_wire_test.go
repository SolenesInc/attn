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
	w := newWorld(t, fakeagent.Pi)
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
			if plugin.Name == "attn-pi" && protocol.Deref(plugin.HealthStatus) == agentdriver.HealthHealthy {
				return true
			}
		}
		return false
	})

	catalog := agentCatalog(t, w)
	codex := catalog["codex"]
	if codex.Executable != nightly || codex.Health != agentdriver.HealthHealthy || codex.Plugin != "" || !codex.ModelPin || !codex.EffortPin {
		t.Errorf("codex = %+v, want the configured executable, healthy, built in, with pins", codex)
	}
	if _, ok := catalog["claude"]; !ok {
		t.Errorf("the catalog is missing the built-in claude: %+v", catalog)
	}
	pi, ok := catalog["pi"]
	var piHealth string
	for _, plugin := range healthy.Plugins {
		if plugin.Name == "attn-pi" {
			piHealth = protocol.Deref(plugin.HealthMessage)
		}
	}
	if !ok || pi.Plugin != "attn-pi" || pi.Health != agentdriver.HealthHealthy || pi.Detail != piHealth || pi.ModelPin || pi.EffortPin {
		t.Errorf("pi = %+v, want offered by attn-pi, healthy with %q, without the pins the plugin did not declare", pi, piHealth)
	}

	uninstalled := testworld.Request(app, protocol.UninstallPluginMessage{Cmd: protocol.CmdUninstallPlugin, Name: "attn-pi"},
		protocol.EventPluginActionResult, func(m protocol.PluginActionResultMessage) bool { return protocol.Deref(m.Name) == "attn-pi" })
	if !uninstalled.Success {
		t.Fatalf("uninstall attn-pi: %s", protocol.Deref(uninstalled.Error))
	}
	if pi, ok := agentCatalog(t, w)["pi"]; ok {
		t.Errorf("the catalog still offers pi after its plugin went away: %+v", pi)
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
