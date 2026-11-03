package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

func init() {
	registerMigration(migration{
		version: 1793534250941634,
		desc:    "fold role stopping points",
		apply:   applyMigration1793534250941634,
	})
}

func applyMigration1793534250941634(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT revision, config FROM delegation_preference_revisions`)
	if err != nil {
		return err
	}
	folded := map[int]string{}
	for rows.Next() {
		var revision int
		var raw string
		if err := rows.Scan(&revision, &raw); err != nil {
			rows.Close()
			return err
		}
		config, changed, err := foldRoleStoppingPoints(raw)
		if err != nil {
			rows.Close()
			return fmt.Errorf("delegation preferences revision %d: %w", revision, err)
		}
		if changed {
			folded[revision] = config
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for revision, config := range folded {
		if _, err := tx.Exec(`UPDATE delegation_preference_revisions SET config = ? WHERE revision = ?`, config, revision); err != nil {
			return err
		}
	}
	return nil
}

func foldRoleStoppingPoints(raw string) (string, bool, error) {
	var config map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return "", false, err
	}
	var roles []map[string]json.RawMessage
	if rolesRaw, ok := config["roles"]; ok {
		if err := json.Unmarshal(rolesRaw, &roles); err != nil {
			return "", false, err
		}
	}
	changed := false
	for _, role := range roles {
		stoppingRaw, ok := role["stopping_point"]
		if !ok {
			continue
		}
		changed = true
		delete(role, "stopping_point")
		var stoppingPoint, instructions string
		if err := json.Unmarshal(stoppingRaw, &stoppingPoint); err != nil {
			return "", false, err
		}
		if strings.TrimSpace(stoppingPoint) == "" {
			continue
		}
		if instructionsRaw, ok := role["instructions"]; ok {
			if err := json.Unmarshal(instructionsRaw, &instructions); err != nil {
				return "", false, err
			}
		}
		instructions = strings.TrimSpace(strings.TrimSpace(instructions) + "\n\nStopping point: " + strings.TrimSpace(stoppingPoint))
		role["instructions"], _ = json.Marshal(instructions)
	}
	if !changed {
		return raw, false, nil
	}
	config["roles"], _ = json.Marshal(roles)
	out, err := json.Marshal(config)
	return string(out), true, err
}
