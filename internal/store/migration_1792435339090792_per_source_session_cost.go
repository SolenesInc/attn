package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/sessioncost"
)

func init() {
	registerMigration(migration{version: 1792435339090792, desc: "per source session cost", apply: applyMigration1792435339090792})
}

func applyMigration1792435339090792(tx *sql.Tx) error {
	type source struct {
		Initialized           bool                              `json:"initialized,omitempty"`
		Cursor                string                            `json:"cursor,omitempty"`
		UsageUnavailable      bool                              `json:"usage_unavailable,omitempty"`
		MeasurementIncomplete bool                              `json:"measurement_incomplete,omitempty"`
		Ledger                sessioncost.Ledger                `json:"ledger,omitempty"`
		Observations          map[string]SessionCostObservation `json:"observations,omitempty"`
		Finalized             []string                          `json:"finalized,omitempty"`
	}
	type state struct {
		Initialized bool              `json:"initialized,omitempty"`
		Sources     map[string]source `json:"sources,omitempty"`
	}
	rows, err := tx.Query(`SELECT id,session_cost_json FROM sessions WHERE session_cost_json<>''`)
	if err != nil {
		return err
	}
	type record struct{ id, raw string }
	var records []record
	for rows.Next() {
		var record record
		if err := rows.Scan(&record.id, &record.raw); err != nil {
			rows.Close()
			return err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, record := range records {
		legacy, err := decodeSessionCostState(record.raw)
		if err != nil {
			return fmt.Errorf("session %s cost migration: %w", record.id, err)
		}
		converted := state{Initialized: legacy.Initialized, Sources: make(map[string]source)}
		for id, prior := range legacy.Sources {
			converted.Sources[id] = source{Initialized: true, Cursor: prior.Cursor}
		}
		legacyCursor := ""
		if legacy.Initialized && len(legacy.Sources) == 0 {
			legacyCursor = legacy.Cursor
		}
		if legacyCursor != "" || legacy.UsageUnavailable || legacy.MeasurementIncomplete || len(legacy.Ledger) > 0 || len(legacy.Observations) > 0 || len(legacy.Finalized) > 0 {
			converted.Sources["_legacy"] = source{Initialized: legacy.Initialized, Cursor: legacyCursor, UsageUnavailable: legacy.UsageUnavailable, MeasurementIncomplete: legacy.MeasurementIncomplete,
				Ledger: legacy.Ledger, Observations: legacy.Observations, Finalized: legacy.Finalized}
		}
		encoded, err := json.Marshal(converted)
		if err != nil {
			return fmt.Errorf("encode migrated session %s cost: %w", record.id, err)
		}
		if _, err := tx.Exec(`UPDATE sessions SET session_cost_json=? WHERE id=?`, string(encoded), record.id); err != nil {
			return err
		}
	}
	return nil
}
