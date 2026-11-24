package store

import (
	"database/sql"
)

func init() {
	registerMigration(migration{
		version: 1795485089532374,
		desc:    "backfill seed automation id",
		apply:   applyMigration1795485089532374,
	})
}

func applyMigration1795485089532374(tx *sql.Tx) error {
	_, seeds, found, err := readCollection(tx, "core/garden", "seeds")
	if err != nil || !found {
		return err
	}
	_, err = tx.Exec(`UPDATE ` + seeds + `
		SET body = json_set(body, '$.automation_id', COALESCE(
			(SELECT r.definition_id FROM automation_runs r WHERE r.seed_id = ` + seeds + `.id AND r.seed_id != '' ORDER BY r.created_at DESC, r.id DESC LIMIT 1),
			(SELECT b.definition_id FROM automation_continuity_bindings b WHERE b.seed_id = ` + seeds + `.id AND b.seed_id != '' ORDER BY b.created_at DESC, b.id DESC LIMIT 1)
		)),
		rev = rev + 1
		WHERE (json_extract(body, '$.automation_id') IS NULL OR json_extract(body, '$.automation_id') = 0)
		  AND (
			EXISTS (SELECT 1 FROM automation_runs r WHERE r.seed_id = ` + seeds + `.id AND r.seed_id != '')
			OR EXISTS (SELECT 1 FROM automation_continuity_bindings b WHERE b.seed_id = ` + seeds + `.id AND b.seed_id != '')
		  )`)
	return err
}
