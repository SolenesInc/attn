package daemon

import (
	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

func (h monitorHost) RunEnded(m *sessionmonitor.Monitor, run sessionmonitor.Run) {
	d := h.daemon
	if !d.analyticsEnabled() {
		return
	}
	session := m.Session()
	if err := d.appendAnalyticsAt(analytics.StreamRun, "run", string(session), run.EndedAt, d.runProps(session, run)); err != nil {
		d.logf("analytics: %v", err)
	}
}

type runTokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

type runProps struct {
	StartedBy  sessionmonitor.RunStart  `json:"started_by"`
	DurationMs int64                    `json:"duration_ms"`
	Ended      protocol.SessionState    `json:"ended"`
	OpenedTurn bool                     `json:"opened_turn"`
	Approvals  int                      `json:"approvals"`
	Usage      sessionmonitor.RunUsage  `json:"usage"`
	Tokens     *runTokens               `json:"tokens,omitempty"`
	USD        *float64                 `json:"usd,omitempty"`
	Session    *analytics.SessionTraits `json:"session,omitempty"`
}

func (d *Daemon) runProps(session protocol.SessionID, run sessionmonitor.Run) runProps {
	props := runProps{
		StartedBy:  run.StartedBy,
		DurationMs: run.EndedAt.Sub(run.StartedAt).Milliseconds(),
		Ended:      run.Ended,
		OpenedTurn: run.OpenedTurn,
		Approvals:  run.Approvals,
		Usage:      run.Usage,
	}
	if traits := d.sessionTraits(session); traits != nil {
		traits.State = ""
		props.Session = traits
	}
	if run.Usage != sessionmonitor.RunUsageKnown {
		return props
	}
	var tokens runTokens
	for _, usage := range run.Tokens {
		tokens.Input += usage.InputTokens
		tokens.Output += usage.OutputTokens
		tokens.CacheRead += usage.CacheReadInputTokens
		tokens.CacheWrite += usage.CacheWrite5mInputTokens + usage.CacheWrite1hInputTokens + usage.UnclassifiedCacheWriteTokens
	}
	props.Tokens = &tokens
	if usd, known, _ := sessioncost.Price(run.Tokens, d.settingValues()); known {
		props.USD = &usd
	}
	return props
}
