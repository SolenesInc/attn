package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

func TestAgentCatalogListsBuiltInAndPluginAgents(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	plugin := &pluginConnection{name: "attn-pi"}
	if err := d.ensurePluginRegistry().register(plugin); err != nil {
		t.Fatal(err)
	}
	if err := d.ensurePluginRegistry().registerDriver(plugin, pluginDriverRegisterParams{
		Agent:        "pi",
		Capabilities: map[string]bool{"model_pin": true, "effort_pin": true, "state_reporting": true},
	}); err != nil {
		t.Fatal(err)
	}
	plugin.setHealth("healthy", "pi 0.84.2 is ready", time.Now())
	d.store.SetSetting(canonicalExecutableSettingKey("codex"), "/opt/tools/codex-nightly")

	recorder := httptest.NewRecorder()
	d.handleAgents(recorder, httptest.NewRequest(http.MethodGet, "/agents", nil))
	var payload struct {
		Agents []agentdriver.Descriptor `json:"agents"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %q: %v", recorder.Body.String(), err)
	}
	byName := map[string]agentdriver.Descriptor{}
	for _, agent := range payload.Agents {
		byName[agent.Name] = agent
	}

	codex := byName["codex"]
	if codex.Executable != "/opt/tools/codex-nightly" || codex.Health != agentdriver.HealthUnhealthy || codex.Plugin != "" || !codex.ModelPin || !codex.EffortPin {
		t.Fatalf("codex = %+v", codex)
	}
	pi := byName["pi"]
	if pi.Plugin != "attn-pi" || pi.Health != agentdriver.HealthHealthy || pi.Detail != "pi 0.84.2 is ready" || !pi.ModelPin || !pi.EffortPin {
		t.Fatalf("pi = %+v", pi)
	}
	if _, ok := byName["claude"]; !ok {
		t.Fatalf("catalog is missing the built-in agents: %+v", payload.Agents)
	}
}

func TestAgentCatalogMarksADisconnectedPluginAgentUnknown(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	plugin := &pluginConnection{name: "attn-pi"}
	if err := d.ensurePluginRegistry().register(plugin); err != nil {
		t.Fatal(err)
	}
	if err := d.ensurePluginRegistry().registerDriver(plugin, pluginDriverRegisterParams{Agent: "pi"}); err != nil {
		t.Fatal(err)
	}
	d.ensurePluginRegistry().unregister(plugin)
	for _, agent := range d.agentCatalog() {
		if agent.Name == "pi" {
			t.Fatalf("a disconnected plugin still offers pi: %+v", agent)
		}
	}
}
