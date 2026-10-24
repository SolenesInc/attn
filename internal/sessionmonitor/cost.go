package sessionmonitor

import (
	"maps"
	"reflect"
	"strings"

	"github.com/victorarias/attn/internal/sessioncost"
)

type costOwner struct {
	Source    string
	Finalized bool
}

func (m *Monitor) loadCostLocked(state sessioncost.Checkpoint) {
	m.costInitialized, m.cost = state.Initialized, state.Sources
	m.costOwners = make(map[string]costOwner)
	for source, state := range m.cost {
		for id := range state.Observations {
			m.costOwners[id] = costOwner{Source: source}
		}
		for _, id := range state.Finalized {
			m.costOwners[id] = costOwner{Source: source, Finalized: true}
		}
	}
}

func (m *Monitor) costCheckpoint() sessioncost.Checkpoint {
	m.mu.Lock()
	defer m.mu.Unlock()
	return sessioncost.Checkpoint{Initialized: m.costInitialized, Sources: m.cost}
}

func (m *Monitor) initializeCostSources(cursors map[string]string, consumeLegacy bool) bool {
	m.mu.Lock()
	changed := !m.costInitialized
	sources := maps.Clone(m.cost)
	if sources == nil {
		sources = make(map[string]*sessioncost.Source)
	}
	for id, cursor := range cursors {
		if sources[id] != nil && sources[id].Initialized {
			continue
		}
		source := sources[id].Clone()
		source.Cursor, source.Initialized = cursor, true
		sources[id] = source
		changed = true
	}
	if consumeLegacy && sources[sessioncost.LegacySource] != nil && sources[sessioncost.LegacySource].Cursor != "" {
		source := sources[sessioncost.LegacySource].Clone()
		source.Cursor = ""
		sources[sessioncost.LegacySource] = source
		changed = true
	}
	if changed {
		m.costInitialized, m.cost = true, sources
	}
	m.mu.Unlock()
	return changed
}

func (m *Monitor) applyCostObservations(sourceID, cursor string, observations []sessioncost.Observation) bool {
	m.mu.Lock()
	sources := maps.Clone(m.cost)
	if sources == nil {
		sources = make(map[string]*sessioncost.Source)
	}
	edited := make(map[string]*sessioncost.Source)
	edit := func(id string) *sessioncost.Source {
		if source := edited[id]; source != nil {
			return source
		}
		source := sources[id].Clone()
		sources[id], edited[id] = source, source
		return source
	}
	observed := edit(sourceID)
	changed := !m.costInitialized || !observed.Initialized || observed.Cursor != cursor
	observed.Cursor, observed.Initialized = cursor, true
	if m.costOwners == nil {
		m.costOwners = make(map[string]costOwner)
	}
	for _, observation := range observations {
		observation.ObservationID = strings.TrimSpace(observation.ObservationID)
		observation.Model = strings.TrimSpace(observation.Model)
		observation.Purpose = sessioncost.NewLedgerKey(observation.Model, observation.Purpose).Purpose
		if observation.ObservationID == "" || observation.Model == "" || !observation.Usage.HasUsage() {
			continue
		}
		owner, exists := m.costOwners[observation.ObservationID]
		if owner.Finalized {
			continue
		}
		if !exists {
			owner = costOwner{Source: sourceID}
			m.costOwners[observation.ObservationID] = owner
		}
		source := sources[owner.Source]
		prior, seen := source.Observations[observation.ObservationID]
		if seen && reflect.DeepEqual(prior, observation) {
			continue
		}
		source = edit(owner.Source)
		if source.Ledger == nil {
			source.Ledger = make(sessioncost.Ledger)
		}
		if source.Observations == nil {
			source.Observations = make(map[string]sessioncost.Observation)
		}
		if seen {
			key := sessioncost.RequestLedgerKey(prior.Model, prior.Purpose, prior.Usage)
			key.FastMode = prior.FastMode
			source.Ledger[key] = source.Ledger[key].Subtract(prior.Usage)
		}
		key := sessioncost.RequestLedgerKey(observation.Model, observation.Purpose, observation.Usage)
		key.FastMode = observation.FastMode
		source.Ledger[key] = source.Ledger[key].Add(observation.Usage)
		source.Observations[observation.ObservationID] = observation
		changed = true
	}
	if changed {
		m.costInitialized, m.cost = true, sources
	}
	m.mu.Unlock()
	return changed
}

func (m *Monitor) markCostSource(sourceID string, incomplete, unavailable bool, cursor *string) bool {
	m.mu.Lock()
	source := m.cost[sourceID].Clone()
	source.MeasurementIncomplete = source.MeasurementIncomplete || incomplete
	source.UsageUnavailable = source.UsageUnavailable || unavailable
	if cursor != nil {
		source.Cursor, source.Initialized = *cursor, true
	}
	if unavailable {
		source.Initialized = true
	}
	if m.costInitialized && reflect.DeepEqual(m.cost[sourceID], source) {
		m.mu.Unlock()
		return false
	}
	sources := maps.Clone(m.cost)
	if sources == nil {
		sources = make(map[string]*sessioncost.Source)
	}
	sources[sourceID] = source
	m.costInitialized = m.costInitialized || unavailable || cursor != nil
	m.cost = sources
	m.mu.Unlock()
	return true
}

func (m *Monitor) publishCost() bool {
	m.mu.Lock()
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
}

func CostChange(previous, view View) (changed, durable, usageChanged bool) {
	if previous.CostInitialized.V != view.CostInitialized.V {
		changed, durable = true, true
	}
	for id, source := range view.Cost.Entries {
		prior := previous.Cost.Entries[id]
		if prior == source {
			continue
		}
		changed = true
		if prior == nil || prior.Initialized != source.Initialized || prior.UsageUnavailable != source.UsageUnavailable || prior.MeasurementIncomplete != source.MeasurementIncomplete {
			durable = true
		}
		if prior == nil {
			usageChanged = usageChanged || len(source.Ledger) > 0 || source.UsageUnavailable || source.MeasurementIncomplete
		} else {
			usageChanged = usageChanged || prior.UsageUnavailable != source.UsageUnavailable || prior.MeasurementIncomplete != source.MeasurementIncomplete || !reflect.DeepEqual(prior.Ledger, source.Ledger)
		}
	}
	return
}

func (m *Monitor) StartCost() {
	if m == nil {
		return
	}
	if m.initializeCostSources(nil, false) {
		m.publishCost()
	}
}
