package store

import (
	"database/sql"
	"errors"

	"github.com/victorarias/attn/internal/docstore"
)

func init() {
	registerMigration(migration{
		version: 1793138901999843,
		desc:    "crew memory session",
		apply:   applyMigration1793138901999843,
	})
}

func applyMigration1793138901999843(tx *sql.Tx) error {
	var collectionID int64
	err := tx.QueryRow(`SELECT id FROM document_collections WHERE namespace='core/crew' AND collection='members'`).Scan(&collectionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE ` + docstore.TableName(collectionID) + `
		SET body=json_remove(json_set(body, '$.letter_summary', json_extract(body, '$.letter_day')), '$.letter_day')
		WHERE json_type(body, '$.letter_day') IS NOT NULL`)
	return err
}
