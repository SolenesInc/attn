package store

import (
	"encoding/json"
	"github.com/victorarias/attn/internal/sessioncost"
	"strings"
)

type SessionCostObservation struct {
	ObservationID string            `json:"observation_id"`
	Model         string            `json:"model"`
	Purpose       string            `json:"purpose,omitempty"`
	FastMode      bool              `json:"fast_mode,omitempty"`
	Usage         sessioncost.Usage `json:"usage"`
}

type SessionCostState struct {
	Initialized           bool                              `json:"initialized,omitempty"`
	Cursor                string                            `json:"cursor,omitempty"`
	UsageUnavailable      bool                              `json:"usage_unavailable,omitempty"`
	MeasurementIncomplete bool                              `json:"measurement_incomplete,omitempty"`
	Sources               map[string]SessionCostSourceState `json:"sources,omitempty"`
	Ledger                sessioncost.Ledger                `json:"ledger,omitempty"`
	Observations          map[string]SessionCostObservation `json:"observations,omitempty"`
	Finalized             []string                          `json:"finalized,omitempty"`
}

type SessionCostSourceState struct {
	Cursor string `json:"cursor,omitempty"`
}

func decodeSessionCostState(raw string) (SessionCostState, error) {
	state := SessionCostState{}
	if strings.TrimSpace(raw) == "" {
		return state, nil
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return SessionCostState{}, err
	}
	return state, nil
}
