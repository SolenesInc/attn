package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
)

func (d *Daemon) decorateSessionWithCost(session *protocol.Session) {
	if session == nil || d.store == nil {
		return
	}
	state, err := d.store.SessionCost(session.ID)
	if err != nil {
		return
	}
	if state.UsageUnavailable {
		return
	}
	summary := sessioncost.Summarize(state.Ledger, d.store.GetAllSettings())
	if !summary.HasUsage || !summary.Valid {
		return
	}
	usage := &protocol.SessionUsage{
		TotalTokens:      int(summary.TotalTokens),
		HasUnpricedUsage: summary.HasUnpricedUsage,
		Models:           make([]protocol.SessionUsageModel, 0, len(summary.Models)),
	}
	if state.MeasurementIncomplete {
		usage.MeasurementIncomplete = protocol.Ptr(true)
	}
	if summary.CostUSD != nil {
		usage.CostUsd = protocol.Ptr(*summary.CostUSD)
	}
	for _, row := range summary.Models {
		model := protocol.SessionUsageModel{
			Model:                        row.Model,
			Purpose:                      row.Purpose,
			InputTokens:                  int(row.Usage.InputTokens),
			OutputTokens:                 int(row.Usage.OutputTokens),
			CacheReadTokens:              int(row.Usage.CacheReadInputTokens),
			CacheWrite5MTokens:           int(row.Usage.CacheWrite5mInputTokens),
			CacheWrite1HTokens:           int(row.Usage.CacheWrite1hInputTokens),
			CacheWriteUnclassifiedTokens: int(row.Usage.UnclassifiedCacheWriteTokens),
			TotalTokens:                  int(row.TotalTokens),
			HasUnpricedUsage:             row.HasUnpricedUsage,
		}
		if row.CostUSD != nil {
			model.CostUsd = protocol.Ptr(*row.CostUSD)
		}
		if row.UnpricedReason != "" {
			model.UnpricedReason = protocol.Ptr(row.UnpricedReason)
		}
		usage.Models = append(usage.Models, model)
	}
	session.Usage = usage
}

func isSessionCostPriceSetting(key string) bool {
	return strings.HasPrefix(key, sessioncost.SessionCostPricePrefix)
}

func (d *Daemon) publishSessionCostReprices() {
	for _, session := range d.store.List("") {
		state, err := d.store.SessionCost(session.ID)
		if err != nil {
			continue
		}
		summary := sessioncost.Summarize(state.Ledger, nil)
		if summary.HasUsage || state.UsageUnavailable {
			d.publishFact(FactSessionCostChanged, session.ID, nil)
		}
	}
}
