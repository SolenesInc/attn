package daemon

import (
	"context"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
)

type claudeHarness struct{ d *Daemon }

func init() {
	harnessSources = append(harnessSources, func(d *Daemon) []harness.Harness { return []harness.Harness{claudeHarness{d: d}} })
}

func (claudeHarness) driver() agentdriver.Driver { return agentdriver.MustGet("claude") }

func (h claudeHarness) Facts() harness.Facts { return builtinFacts(h.driver()) }

func (h claudeHarness) Health(context.Context) harness.Health {
	return h.d.executableHealth(h.driver(), true)
}
