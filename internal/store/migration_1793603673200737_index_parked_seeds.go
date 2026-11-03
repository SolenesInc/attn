package store

import (
	"database/sql"

	"github.com/victorarias/attn/internal/docstore"
)

func init() {
	registerMigration(migration{
		version: 1793603673200737,
		desc:    "index parked seeds",
		apply:   applyMigration1793603673200737,
	})
}

func applyMigration1793603673200737(tx *sql.Tx) error {
	var id int64
	err := tx.QueryRow(`SELECT id FROM document_collections WHERE namespace = 'core/garden' AND collection = 'seeds'`).Scan(&id)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	index := docstore.TableName(id) + "_text"
	var indexed int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = ?`, index).Scan(&indexed); err != nil || indexed == 0 {
		return err
	}
	_, err = tx.Exec(`UPDATE ` + index + ` SET fields_json = json_set(fields_json, '$.status', 'parked')
		WHERE json_extract(fields_json, '$.status') = 'dormant'`)
	return err
}
