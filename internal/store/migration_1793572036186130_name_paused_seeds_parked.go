package store

import (
	"database/sql"

	"github.com/victorarias/attn/internal/docstore"
)

func init() {
	registerMigration(migration{
		version: 1793572036186130,
		desc:    "name paused seeds parked",
		apply:   applyMigration1793572036186130,
	})
}

func applyMigration1793572036186130(tx *sql.Tx) error {
	var id int64
	err := tx.QueryRow(`SELECT id FROM document_collections WHERE namespace = 'core/garden' AND collection = 'seeds'`).Scan(&id)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE ` + docstore.TableName(id) + ` SET body = json_set(body, '$.status', 'parked'), rev = rev + 1
		WHERE json_extract(body, '$.status') = 'dormant'`)
	return err
}
