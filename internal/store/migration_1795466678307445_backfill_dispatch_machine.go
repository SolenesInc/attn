package store

import (
	"database/sql"
)

func init() {
	registerMigration(migration{
		version: 1795466678307445,
		desc:    "backfill dispatch machine",
		apply:   applyMigration1795466678307445,
	})
}

func applyMigration1795466678307445(tx *sql.Tx) error {
	_, dispatches, found, err := readCollection(tx, "core/garden", "dispatches")
	if err != nil || !found {
		return err
	}
	_, err = tx.Exec(`UPDATE ` + dispatches + `
		SET body = json_set(body, '$.machine', (
			SELECT CASE WHEN s.copied_from != '' THEN '' ELSE s.host END
			FROM sessions s
			WHERE s.id = ` + dispatches + `.id
		)),
		rev = rev + 1
		WHERE json_extract(body, '$.machine') IS NULL
		  AND EXISTS (SELECT 1 FROM sessions s WHERE s.id = ` + dispatches + `.id)`)
	return err
}
