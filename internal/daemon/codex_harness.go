package daemon

import (
	"context"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
)

type codexHarness struct{ d *Daemon }

func init() {
	harnessSources = append(harnessSources, func(d *Daemon) []harness.Harness { return []harness.Harness{codexHarness{d: d}} })
}

func (codexHarness) driver() agentdriver.Driver { return agentdriver.MustGet("codex") }

func (h codexHarness) Facts() harness.Facts { return builtinFacts(h.driver()) }

func (h codexHarness) Health(context.Context) harness.Health {
	return h.d.executableHealth(h.driver(), true)
}
