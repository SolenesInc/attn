package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func init() {
	registerMigration(migration{
		version: 1793536610325282,
		desc:    "fold pending delegation stopping points",
		apply:   applyMigration1793536610325282,
	})
}

func applyMigration1793536610325282(tx *sql.Tx) error {
	if err := foldDelegationOperationStoppingPoints(tx); err != nil {
		return err
	}
	return supersedePreFoldDelegationPreferences(tx)
}

func foldDelegationOperationStoppingPoints(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT request_id, resolved_preferences FROM delegation_operations WHERE resolved_preferences != ''`)
	if err != nil {
		return err
	}
	folded := map[string]string{}
	for rows.Next() {
		var requestID, raw string
		if err := rows.Scan(&requestID, &raw); err != nil {
			rows.Close()
			return err
		}
		resolved, changed, err := foldResolvedStoppingPoint(raw)
		if err != nil {
			rows.Close()
			return fmt.Errorf("delegation operation %s: %w", requestID, err)
		}
		if changed {
			folded[requestID] = resolved
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for requestID, resolved := range folded {
		if _, err := tx.Exec(`UPDATE delegation_operations SET resolved_preferences = ? WHERE request_id = ?`, resolved, requestID); err != nil {
			return err
		}
	}
	return nil
}

func foldResolvedStoppingPoint(raw string) (string, bool, error) {
	var resolved map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &resolved); err != nil {
		return "", false, err
	}
	stoppingRaw, ok := resolved["stopping_point"]
	if !ok {
		return raw, false, nil
	}
	delete(resolved, "stopping_point")
	var stoppingPoint, instructions string
	if err := json.Unmarshal(stoppingRaw, &stoppingPoint); err != nil {
		return "", false, err
	}
	if strings.TrimSpace(stoppingPoint) != "" {
		if instructionsRaw, ok := resolved["instructions"]; ok {
			if err := json.Unmarshal(instructionsRaw, &instructions); err != nil {
				return "", false, err
			}
		}
		instructions = strings.TrimSpace(strings.TrimSpace(instructions) + "\n\nStopping point: " + strings.TrimSpace(stoppingPoint))
		resolved["instructions"], _ = json.Marshal(instructions)
	}
	out, err := json.Marshal(resolved)
	return string(out), true, err
}

func supersedePreFoldDelegationPreferences(tx *sql.Tx) error {
	var revision int
	var config string
	err := tx.QueryRow(`SELECT revision, config FROM delegation_preference_revisions ORDER BY revision DESC LIMIT 1`).Scan(&revision, &config)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO delegation_preference_revisions (revision, parent, config, message, created_at) VALUES (?, ?, ?, ?, ?)`,
		revision+1, revision, config, "Stopping points are now part of each role's instructions", time.Now().UTC().Format(sortableTimeFormat))
	return err
}
