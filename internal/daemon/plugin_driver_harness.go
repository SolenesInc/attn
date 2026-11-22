package daemon

import (
	"context"
	"time"

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
			Provider:           true,
		},
		SkillDirs: func(lookupEnv func(string) string, homeDir string) ([]string, error) {
			return resolvePluginSkillsDirs(h.reg.SkillDirs, lookupEnv, homeDir), nil
		},
		CacheTTL: crewCacheTTLDefault * time.Second,
		Signals:  pluginSignals(declared),
	}
}

func (h pluginHarness) RunEnded(e harness.RunEnd) {
	h.d.notifyPluginDriverSessionClosed(e.Owner, e.Terminal, e.Run, string(e.Reason), e.ExitCode, e.Signal)
}

func (h pluginHarness) Route(facts harness.SessionFacts) (string, bool) {
	reg, conn := h.d.ensurePluginRegistry().driverConnection(facts.Harness, facts.RunOwner)
	if conn == nil {
		return "", false
	}
	return pluginDriverRoute(reg), true
}

func (h pluginHarness) End(_ context.Context, e harness.Ending) {
	if l := h.d.serverLinkFor(h.reg.Agent); l != nil {
		l.sessionClosed(e.Session, e.Profile, e.Released)
	}
}

func pluginSignals(declared map[string]bool) harness.Signals {
	signals := harness.Signals{Heartbeat: 500 * time.Millisecond, ReviewerDwell: time.Minute, ReportsTools: declared["pull_request_reporting"]}
	if declared["state_reporting"] {
		signals.StateAuthority = harness.StateByHarness
	}
	return signals
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
