package sessioncost

import (
	"maps"
	"slices"
)

type Observation struct {
	ObservationID string `json:"observation_id"`
	Model         string `json:"model"`
	Purpose       string `json:"purpose,omitempty"`
	FastMode      bool   `json:"fast_mode,omitempty"`
	Usage         Usage  `json:"usage"`
}

const LegacySource = "_legacy"

type Checkpoint struct {
	Initialized bool               `json:"initialized,omitempty"`
	Sources     map[string]*Source `json:"sources,omitempty"`
}

type Source struct {
	Initialized           bool                   `json:"initialized,omitempty"`
	Cursor                string                 `json:"cursor,omitempty"`
	UsageUnavailable      bool                   `json:"usage_unavailable,omitempty"`
	MeasurementIncomplete bool                   `json:"measurement_incomplete,omitempty"`
	Ledger                Ledger                 `json:"ledger,omitempty"`
	Observations          map[string]Observation `json:"observations,omitempty"`
	Finalized             []string               `json:"finalized,omitempty"`
}

type SourceTotals struct {
	Initialized           bool   `json:"initialized,omitempty"`
	UsageUnavailable      bool   `json:"usage_unavailable,omitempty"`
	MeasurementIncomplete bool   `json:"measurement_incomplete,omitempty"`
	Ledger                Ledger `json:"ledger,omitempty"`
}

func (state Checkpoint) SourceTotals() map[string]SourceTotals {
	totals := make(map[string]SourceTotals, len(state.Sources))
	for id, source := range state.Sources {
		totals[id] = SourceTotals{Initialized: source.Initialized, UsageUnavailable: source.UsageUnavailable, MeasurementIncomplete: source.MeasurementIncomplete, Ledger: source.Ledger}
	}
	return totals
}

func FromSourceTotals(initialized bool, totals map[string]SourceTotals) Checkpoint {
	state := Checkpoint{Initialized: initialized, Sources: make(map[string]*Source, len(totals))}
	for id, source := range totals {
		state.Sources[id] = &Source{Initialized: source.Initialized, UsageUnavailable: source.UsageUnavailable, MeasurementIncomplete: source.MeasurementIncomplete, Ledger: source.Ledger}
	}
	return state
}

func (source *Source) Clone() *Source {
	if source == nil {
		return &Source{}
	}
	clone := *source
	clone.Ledger = maps.Clone(source.Ledger)
	clone.Observations = maps.Clone(source.Observations)
	clone.Finalized = slices.Clone(source.Finalized)
	return &clone
}

func (state Checkpoint) Clone() Checkpoint {
	clone := Checkpoint{Initialized: state.Initialized, Sources: make(map[string]*Source, len(state.Sources))}
	for id, source := range state.Sources {
		clone.Sources[id] = source.Clone()
	}
	return clone
}

type Totals struct {
	UsageUnavailable      bool
	MeasurementIncomplete bool
	Ledger                Ledger
}

func (state Checkpoint) Usage() Totals {
	usage := Totals{Ledger: make(Ledger)}
	for _, source := range state.Sources {
		usage.UsageUnavailable = usage.UsageUnavailable || source.UsageUnavailable
		usage.MeasurementIncomplete = usage.MeasurementIncomplete || source.MeasurementIncomplete
		for key, value := range source.Ledger {
			usage.Ledger[key] = usage.Ledger[key].Add(value)
		}
	}
	return usage
}
