package daemon

import (
	"context"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
)

func init() {
	harnessSources = append(harnessSources, func(d *Daemon) []harness.Harness {
		registered := d.ensurePluginRegistry().registeredDrivers()
		all := make([]harness.Harness, 0, len(registered))
		for _, reg := range registered {
			all = append(all, pluginHarness{d: d, reg: reg})
		}
		return all
	})
}

type pluginHarness struct {
	d   *Daemon
	reg pluginDriverRegistration
}

func (h pluginHarness) Facts() harness.Facts {
	declared := h.reg.Capabilities
	return harness.Facts{
		ID:          h.reg.Agent,
		DisplayName: h.reg.displayName(),
		Options: harness.Options{
			Resume:             declared["resume"],
			Yolo:               declared["yolo"],
			InitialPrompt:      declared["initial_prompt"],
			LaunchInstructions: declared["launch_instructions"],
			ModelPin:           declared["model_pin"],
			EffortPin:          declared["effort_pin"],
		},
	}
}

func (h pluginHarness) Health(context.Context) harness.Health {
	plugin := h.d.ensurePluginRegistry().get(h.reg.PluginName)
	if plugin == nil {
		return harness.Health{Installation: harness.Missing, Offered: true, Reason: "plugin " + h.reg.PluginName + " is not connected"}
	}
	health, detail, _ := plugin.healthSnapshot()
	installation := harness.Unknown
	switch health {
	case agentdriver.HealthHealthy:
		installation = harness.Installed
	case agentdriver.HealthUnhealthy:
		installation = harness.Missing
	}
	return harness.Health{Installation: installation, Offered: true, Reason: detail}
}
