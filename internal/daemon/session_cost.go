package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) decorateSessionWithCost(session *protocol.Session) {
	if session == nil || d.store == nil {
		return
	}
	state, err := d.store.SessionCost(session.ID)
	if err != nil {
		return
	}
	cost := store.SessionCostUsage{UsageUnavailable: state.UsageUnavailable, MeasurementIncomplete: state.MeasurementIncomplete, Ledger: state.Ledger}
	if usage := sessionUsage(cost, d.store.GetAllSettings()); usage != nil {
		session.Usage = usage
	}
}

func (d *Daemon) decorateLedgerEntriesWithUsage(entries []protocol.SessionLedgerEntry) {
	ids := make([]protocol.SessionID, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	costs, err := d.store.SessionCostUsages(ids)
	if err != nil {
		d.logf("session ledger: read usage: %v", err)
		return
	}
	settings := d.store.GetAllSettings()
	for i := range entries {
		entries[i].Usage = sessionUsage(costs[entries[i].ID], settings)
	}
}

func (d *Daemon) decorateLedgerEntryWithUsage(entry *protocol.SessionLedgerEntry) {
	if entry == nil {
		return
	}
	costs, err := d.store.SessionCostUsages([]protocol.SessionID{entry.ID})
	if err != nil {
		d.logf("session ledger: read usage for %s: %v", entry.ID, err)
		return
	}
	entry.Usage = sessionUsage(costs[entry.ID], d.store.GetAllSettings())
}

func sessionUsage(state store.SessionCostUsage, settings map[string]string) *protocol.SessionUsage {
	if state.UsageUnavailable {
		return nil
	}
	summary := sessioncost.Summarize(state.Ledger, settings)
	if !summary.HasUsage || !summary.Valid {
		return nil
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
	return usage
}

func isSessionCostPriceSetting(key string) bool {
	return strings.HasPrefix(key, sessioncost.SessionCostPricePrefix) ||
		strings.HasPrefix(key, sessioncost.SessionCostBilledAsPrefix)
}

func (d *Daemon) publishSessionCostReprices() {
	for _, session := range d.store.List("") {
		state, err := d.store.SessionCost(session.ID)
		if err != nil {
			continue
		}
		summary := sessioncost.Summarize(state.Ledger, nil)
		if summary.HasUsage || state.UsageUnavailable {
			d.publishFact(FactSessionCostChanged, string(session.ID), nil)
		}
	}
}
