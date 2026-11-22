package daemon

import (
	"context"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/setting"
)

type sigmaHarness struct{ d *Daemon }

func init() {
	harnessSources = append(harnessSources, func(d *Daemon) []harness.Harness { return []harness.Harness{sigmaHarness{d: d}} })
}

func (sigmaHarness) driver() agentdriver.Driver { return agentdriver.MustGet("sigma") }

func (h sigmaHarness) Facts() harness.Facts { return builtinFacts(h.driver()) }

func (h sigmaHarness) Health(context.Context) harness.Health {
	return h.d.executableHealth(h.driver(), h.d.on(setting.SigmaEnabled))
}
