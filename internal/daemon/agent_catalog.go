package daemon

import (
	"encoding/json"
	"net/http"
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
		if !d.agentOffered(name) {
			continue
		}
		driver := agentdriver.Get(name)
		caps := agentdriver.EffectiveCapabilities(driver)
		if d.harnessAvailability(driver) == harnessChecking {
			agents = append(agents, agentdriver.Descriptor{
				Name:       name,
				Executable: driver.DefaultExecutable(),
				ModelPin:   caps.HasModelPin,
				EffortPin:  caps.HasEffortPin,
				Health:     agentdriver.HealthUnknown,
				Detail:     "checking the login environment for " + driver.DisplayName(),
			})
			continue
		}
		executable, err := d.machineAgentExecutable(driver, "")
		entry := agentdriver.Descriptor{
			Name:       name,
			Executable: executable,
			ModelPin:   caps.HasModelPin,
			EffortPin:  caps.HasEffortPin,
		}
		if err != nil {
			entry.Health, entry.Detail = agentdriver.HealthUnhealthy, err.Error()
		} else {
			entry.Health, entry.Detail = agentdriver.HealthHealthy, executable
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
	for _, l := range d.appServerLinks() {
		if l.callerConversationEnv() == "" {
			continue
		}
		found := false
		for i := range agents {
			if agents[i].Name == l.b.agent {
				agents[i].CallerConversationEnv = l.callerConversationEnv()
				found = true
				break
			}
		}
		if !found && (l.b.plugin != "" || len(l.sessions()) > 0) {
			agents = append(agents, agentdriver.Descriptor{Name: l.b.agent, Plugin: l.b.plugin, Health: agentdriver.HealthUnhealthy, Detail: "unavailable for new views", CallerConversationEnv: l.callerConversationEnv()})
		}
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	return agents
}
