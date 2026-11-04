package store

import (
	"database/sql"

	"github.com/victorarias/attn/internal/docstore"
)

func init() {
	registerMigration(migration{
		version: 1793828999085389,
		desc:    "move seed reminders out of seed documents",
		apply:   applyMigration1793828999085389,
	})
}

func applyMigration1793828999085389(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TABLE seed_reminders (
		seed_id TEXT PRIMARY KEY,
		remind_at INTEGER NOT NULL,
		reminder TEXT NOT NULL
	)`); err != nil {
		return err
	}
	var id int64
	err := tx.QueryRow(`SELECT id FROM document_collections WHERE namespace = 'core/garden' AND collection = 'seeds'`).Scan(&id)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	seeds := docstore.TableName(id)
	if _, err := tx.Exec(`INSERT INTO seed_reminders (seed_id, remind_at, reminder)
		SELECT id, json_extract(body, '$.remind_at'), json_extract(body, '$.reminder') FROM ` + seeds + `
		WHERE json_extract(body, '$.reminder') IS NOT NULL`); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE ` + seeds + ` SET body = json_remove(body, '$.reminder', '$.remind_at'), rev = rev + 1
		WHERE json_extract(body, '$.reminder') IS NOT NULL`)
	return err
}
