package daemon

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"sort"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

func (d *Daemon) handleAgents(w http.ResponseWriter, r *http.Request) {
	setNoStoreHeaders(w.Header())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"agents": d.agentCatalog()})
}

func (d *Daemon) agentCatalog() []agentdriver.Descriptor {
	agents := make([]agentdriver.Descriptor, 0, len(agentdriver.List()))
	for _, name := range agentdriver.List() {
		driver := agentdriver.Get(name)
		caps := agentdriver.EffectiveCapabilities(driver)
		entry := agentdriver.Descriptor{
			Name:       name,
			Executable: driver.ResolveExecutable(d.store.GetSetting(canonicalExecutableSettingKey(name))),
			ModelPin:   caps.HasModelPin,
			EffortPin:  caps.HasEffortPin,
		}
		if path, err := exec.LookPath(entry.Executable); err != nil {
			entry.Health, entry.Detail = agentdriver.HealthUnhealthy, err.Error()
		} else {
			entry.Health, entry.Detail = agentdriver.HealthHealthy, path
		}
		agents = append(agents, entry)
	}
	registry := d.ensurePluginRegistry()
	for _, driver := range registry.registeredDrivers() {
		plugin := registry.get(driver.PluginName)
		if plugin == nil {
			continue
		}
		health, detail, _ := plugin.healthSnapshot()
		agents = append(agents, agentdriver.Descriptor{
			Name:      driver.Agent,
			Plugin:    driver.PluginName,
			Health:    health,
			Detail:    detail,
			ModelPin:  driver.Capabilities["model_pin"],
			EffortPin: driver.Capabilities["effort_pin"],
		})
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	return agents
}
