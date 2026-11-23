package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

func init() {
	registerMigration(migration{
		version: 1795434433082663,
		desc:    "name models by their catalog id",
		apply:   applyMigration1795434433082663,
	})
}

func applyMigration1795434433082663(tx *sql.Tx) error {
	if err := rewriteJSONColumn(tx, `SELECT revision, config FROM delegation_preference_revisions`, `UPDATE delegation_preference_revisions SET config = ? WHERE revision = ?`, func(doc any) any {
		config, _ := doc.(map[string]any)
		roles, _ := config["roles"].([]any)
		for _, role := range roles {
			choices, _ := asObject(role)["choices"].([]any)
			for _, choice := range choices {
				nameModelByCatalogID(asObject(asObject(choice)["selection"]))
			}
		}
		nameModelByCatalogID(asObject(asObject(config["fallback"])["selection"]))
		return doc
	}); err != nil {
		return fmt.Errorf("delegation preference revisions: %w", err)
	}
	if err := rewriteJSONColumn(tx, `SELECT request_id, resolved_preferences FROM delegation_operations WHERE resolved_preferences != ''`, `UPDATE delegation_operations SET resolved_preferences = ? WHERE request_id = ?`, func(doc any) any {
		nameModelByCatalogID(asObject(asObject(doc)["selection"]))
		return doc
	}); err != nil {
		return fmt.Errorf("delegation operations: %w", err)
	}
	if err := rewriteJSONColumn(tx, `SELECT copied_from, value FROM settings WHERE key = 'model_tier_overrides' AND TRIM(COALESCE(value, '')) != ''`, `UPDATE settings SET value = ? WHERE key = 'model_tier_overrides' AND copied_from = ?`, func(doc any) any {
		entries, _ := doc.([]any)
		kept := []any{}
		seen := map[[2]string]bool{}
		for _, entry := range entries {
			override := asObject(entry)
			nameModelByCatalogID(override)
			harness, _ := override["harness"].(string)
			model, _ := override["model"].(string)
			if seen[[2]string{harness, model}] {
				continue
			}
			seen[[2]string{harness, model}] = true
			kept = append(kept, entry)
		}
		return kept
	}); err != nil {
		return fmt.Errorf("model_tier_overrides: %w", err)
	}
	return nil
}

func rewriteJSONColumn(tx *sql.Tx, query, update string, rewrite func(any) any) error {
	rows, err := tx.Query(query)
	if err != nil {
		return err
	}
	rewritten := map[string]string{}
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return err
		}
		var doc any
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			rows.Close()
			return fmt.Errorf("%s: %w", key, err)
		}
		out, err := json.Marshal(rewrite(doc))
		if err != nil {
			rows.Close()
			return err
		}
		rewritten[key] = string(out)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for key, value := range rewritten {
		if _, err := tx.Exec(update, value, key); err != nil {
			return err
		}
	}
	return nil
}

func asObject(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func nameModelByCatalogID(selection map[string]any) {
	if selection == nil {
		return
	}
	provider, _ := selection["provider"].(string)
	model, _ := selection["model"].(string)
	delete(selection, "provider")
	if provider != "" && model != "" && !strings.HasPrefix(model, provider+"/") {
		selection["model"] = provider + "/" + model
	}
}
