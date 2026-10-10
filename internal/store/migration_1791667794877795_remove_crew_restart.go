package store

import (
	"database/sql"
	"errors"

	"github.com/victorarias/attn/internal/docstore"
)

func init() {
	registerMigration(migration{
		version: 1791667794877795,
		desc:    "remove crew restart",
		apply:   applyMigration1791667794877795,
	})
}

func applyMigration1791667794877795(tx *sql.Tx) error {
	var id int64
	err := tx.QueryRow(`SELECT id FROM document_collections WHERE namespace='core/crew' AND collection='restart_requests'`).Scan(&id)
	if err == nil {
		if _, err := tx.Exec(`DROP TABLE ` + docstore.TableName(id)); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM document_collections WHERE id=?`, id); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	err = tx.QueryRow(`SELECT id FROM document_collections WHERE namespace='core/crew' AND collection='members'`).Scan(&id)
	if err == nil {
		if _, err := tx.Exec(`UPDATE ` + docstore.TableName(id) + ` SET body=json_remove(body, '$.restart') WHERE json_type(body, '$.restart') IS NOT NULL`); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(`DELETE FROM inbox_items WHERE kind='notice' AND coalesce_key GLOB 'crew-restart-*'`)
	return err
}
