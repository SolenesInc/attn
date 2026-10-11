package store

import (
	"database/sql"
	"errors"

	"github.com/victorarias/attn/internal/docstore"
)

func init() {
	registerMigration(migration{
		version: 1791676859283008,
		desc:    "remove outposts",
		apply:   applyMigration1791676859283008,
	})
}

func applyMigration1791676859283008(tx *sql.Tx) error {
	if _, err := tx.Exec(`DROP TABLE endpoints`); err != nil {
		return err
	}
	var id int64
	err := tx.QueryRow(`SELECT id FROM document_collections WHERE namespace='core/garden' AND collection='dispatches'`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	dispatches := docstore.TableName(id)
	if _, err := tx.Exec(`UPDATE ` + dispatches + ` SET body=json_remove(body, '$.cwd', '$.repository_root', '$.repository_subdir', '$.branch', '$.resume') WHERE json_extract(body, '$.host_kind')='remote'`); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE ` + dispatches + ` SET body=json_remove(body, '$.host_kind', '$.endpoint_id') WHERE json_type(body, '$.host_kind') IS NOT NULL OR json_type(body, '$.endpoint_id') IS NOT NULL`)
	return err
}
