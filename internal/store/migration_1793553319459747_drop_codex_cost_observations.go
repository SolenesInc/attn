package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

func init() {
	registerMigration(migration{
		version: 1793553319459747,
		desc:    "drop codex cost observations",
		apply:   applyMigration1793553319459747,
	})
}

func applyMigration1793553319459747(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT session_id, session_cost_json FROM session_runtime WHERE session_cost_json LIKE '%codex:%'`)
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
		kept, changed, err := withoutCodexCostObservations(record.raw)
		if err != nil {
			return fmt.Errorf("session %s cost: %w", record.id, err)
		}
		if !changed {
			continue
		}
		if _, err := tx.Exec(`UPDATE session_runtime SET session_cost_json = ? WHERE session_id = ?`, kept, record.id); err != nil {
			return err
		}
	}
	return nil
}

func withoutCodexCostObservations(raw string) (string, bool, error) {
	var state map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return "", false, err
	}
	var sources map[string]map[string]json.RawMessage
	if encoded, ok := state["sources"]; ok {
		if err := json.Unmarshal(encoded, &sources); err != nil {
			return "", false, err
		}
	}
	changed := false
	for _, source := range sources {
		if encoded, ok := source["observations"]; ok {
			var observations map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &observations); err != nil {
				return "", false, err
			}
			for id := range observations {
				if codexCostObservation(id) {
					delete(observations, id)
					changed = true
				}
			}
			if err := setCostField(source, "observations", observations, len(observations)); err != nil {
				return "", false, err
			}
		}
		if encoded, ok := source["finalized"]; ok {
			var finalized []string
			if err := json.Unmarshal(encoded, &finalized); err != nil {
				return "", false, err
			}
			kept := finalized[:0]
			for _, id := range finalized {
				if codexCostObservation(id) {
					changed = true
					continue
				}
				kept = append(kept, id)
			}
			if err := setCostField(source, "finalized", kept, len(kept)); err != nil {
				return "", false, err
			}
		}
	}
	if !changed {
		return raw, false, nil
	}
	if err := setCostField(state, "sources", sources, 1); err != nil {
		return "", false, err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return "", false, err
	}
	return string(encoded), true, nil
}

func setCostField(object map[string]json.RawMessage, field string, value any, count int) error {
	if count == 0 {
		delete(object, field)
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	object[field] = encoded
	return nil
}

func codexCostObservation(id string) bool {
	if native, ok := strings.CutPrefix(id, "native:"); ok {
		_, id, _ = strings.Cut(native, ":")
	}
	return strings.HasPrefix(id, "codex:")
}
