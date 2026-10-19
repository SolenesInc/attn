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

func (e *Entry) loadCostLocked(state sessioncost.Checkpoint) {
	e.costInitialized, e.cost = state.Initialized, state.Sources
	e.costOwners = make(map[string]costOwner)
	for source, state := range e.cost {
		for id := range state.Observations {
			e.costOwners[id] = costOwner{Source: source}
		}
		for _, id := range state.Finalized {
			e.costOwners[id] = costOwner{Source: source, Finalized: true}
		}
	}
}

func (e *Entry) costCheckpoint() sessioncost.Checkpoint {
	e.mu.Lock()
	defer e.mu.Unlock()
	return sessioncost.Checkpoint{Initialized: e.costInitialized, Sources: e.cost}
}

func (e *Entry) initializeCostSources(cursors map[string]string, consumeLegacy bool) bool {
	e.mu.Lock()
	changed := !e.costInitialized
	sources := maps.Clone(e.cost)
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
	if !changed {
		e.mu.Unlock()
		return true
	}
	e.costInitialized, e.cost = true, sources
	view := e.viewLocked()
	e.mu.Unlock()
	return e.home.Applied(view)
}

func (e *Entry) applyCostObservations(sourceID, cursor string, observations []sessioncost.Observation) bool {
	e.mu.Lock()
	sources := maps.Clone(e.cost)
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
	observed.Cursor, observed.Initialized = cursor, true
	if e.costOwners == nil {
		e.costOwners = make(map[string]costOwner)
	}
	for _, observation := range observations {
		observation.ObservationID = strings.TrimSpace(observation.ObservationID)
		observation.Model = strings.TrimSpace(observation.Model)
		observation.Purpose = sessioncost.NewLedgerKey(observation.Model, observation.Purpose).Purpose
		if observation.ObservationID == "" || observation.Model == "" || !observation.Usage.HasUsage() {
			continue
		}
		owner, exists := e.costOwners[observation.ObservationID]
		if owner.Finalized {
			continue
		}
		if !exists {
			owner = costOwner{Source: sourceID}
			e.costOwners[observation.ObservationID] = owner
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
	}
	e.costInitialized, e.cost = true, sources
	view := e.viewLocked()
	e.mu.Unlock()
	return e.home.Applied(view)
}

func (e *Entry) markCostSource(sourceID string, incomplete, unavailable bool, cursor *string) bool {
	e.mu.Lock()
	source := e.cost[sourceID].Clone()
	source.MeasurementIncomplete = source.MeasurementIncomplete || incomplete
	source.UsageUnavailable = source.UsageUnavailable || unavailable
	if cursor != nil {
		source.Cursor, source.Initialized = *cursor, true
	}
	if unavailable {
		source.Initialized = true
	}
	if e.costInitialized && reflect.DeepEqual(e.cost[sourceID], source) {
		e.mu.Unlock()
		return true
	}
	sources := maps.Clone(e.cost)
	if sources == nil {
		sources = make(map[string]*sessioncost.Source)
	}
	sources[sourceID] = source
	e.costInitialized = e.costInitialized || unavailable || cursor != nil
	e.cost = sources
	view := e.viewLocked()
	e.mu.Unlock()
	return e.home.Applied(view)
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

func (e *Entry) StartCost() {
	if e == nil {
		return
	}
	if e != nil {
		e.initializeCostSources(nil, false)
	}
}
