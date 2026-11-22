package daemon

import (
	"context"
	"path/filepath"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/modeltiers"
)

type codexHarness struct{ d *Daemon }

func init() {
	harnessSources = append(harnessSources, func(d *Daemon) []harness.Harness { return []harness.Harness{codexHarness{d: d}} })
}

func (codexHarness) driver() agentdriver.Driver { return agentdriver.MustGet("codex") }

func (h codexHarness) Facts() harness.Facts {
	facts := builtinFacts(h.driver())
	facts.SkillDirs = func(_ func(string) string, homeDir string) ([]string, error) {
		return []string{filepath.Join(homeDir, ".agents", "skills")}, nil
	}
	facts.CacheTTL = 30 * time.Minute
	facts.Tiers = map[modeltiers.Tier]string{modeltiers.Light: "gpt-6-luna", modeltiers.Standard: "gpt-5.6-terra", modeltiers.Deep: "gpt-6.1-sol"}
	facts.Advisor = harness.ModelChoice{Model: "gpt-5.6-luna", Effort: "xhigh"}
	facts.Signals.Heartbeat, facts.Signals.ReviewerDwell = 500*time.Millisecond, time.Minute
	facts.Signals.ConfirmsFirstTurn = true
	return facts
}

func (h codexHarness) Terminal(t harness.TerminalID, e harness.TerminalEvent) {
	r := h.d.codexShared()
	switch e := e.(type) {
	case harness.Exited:
		r.terminalExited(t)
	case harness.Dropped:
		r.terminalDropped(t)
	case harness.Handed:
		r.terminalMoved(t, e.Profile)
	case harness.Working:
		r.checkWorkingSoon(e.Session)
	case harness.Restarting:
		r.retireConnections(t)
	}
}

func (h codexHarness) End(_ context.Context, e harness.Ending) {
	h.d.codexShared().sessionClosed(e.Session, e.Profile, e.Released)
}

func (h codexHarness) Health(context.Context) harness.Health {
	return h.d.executableHealth(h.Facts(), true)
}
