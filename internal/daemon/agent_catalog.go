package daemon

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/victorarias/attn/internal/harness"
)

func (d *Daemon) handleAgents(w http.ResponseWriter, r *http.Request) {
	setNoStoreHeaders(w.Header())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"agents": d.agentCatalog()})
}

var descriptorHealth = map[harness.Installation]string{
	harness.Installed: harness.HealthHealthy,
	harness.Missing:   harness.HealthUnhealthy,
	harness.Unknown:   harness.HealthUnknown,
}

func (d *Daemon) agentCatalog() []harness.Descriptor {
	providers := map[string]string{}
	for _, driver := range d.ensurePluginRegistry().registeredDrivers() {
		providers[driver.Agent] = driver.PluginName
	}
	all := d.harnesses()
	agents := make([]harness.Descriptor, 0, len(all))
	for _, h := range all {
		health := h.Health(d.life.Context())
		if !health.Offered {
			continue
		}
		facts := h.Facts()
		entry := harness.Descriptor{
			Name:        facts.ID,
			DisplayName: facts.DisplayName,
			Plugin:      providers[facts.ID],
			Executable:  health.Executable,
			Health:      descriptorHealth[health.Installation],
			Detail:      health.Reason,
			ModelPin:    facts.Options.ModelPin,
			EffortPin:   facts.Options.EffortPin,
		}
		if health.Installation == harness.Installed && entry.Detail == "" {
			entry.Detail = health.Executable
		}
		agents = append(agents, entry)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	return agents
}
