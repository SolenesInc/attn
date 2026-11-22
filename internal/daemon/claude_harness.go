package daemon

import (
	"context"
	"path/filepath"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
)

type claudeHarness struct{ d *Daemon }

func init() {
	harnessSources = append(harnessSources, func(d *Daemon) []harness.Harness { return []harness.Harness{claudeHarness{d: d}} })
}

func (claudeHarness) driver() agentdriver.Driver { return agentdriver.MustGet("claude") }

func (h claudeHarness) Facts() harness.Facts {
	facts := builtinFacts(h.driver())
	facts.SkillDirs = func(_ func(string) string, homeDir string) ([]string, error) {
		return []string{filepath.Join(homeDir, ".claude", "skills")}, nil
	}
	facts.CacheTTL = time.Hour
	return facts
}

func (h claudeHarness) Health(context.Context) harness.Health {
	return h.d.executableHealth(h.Facts(), true)
}
