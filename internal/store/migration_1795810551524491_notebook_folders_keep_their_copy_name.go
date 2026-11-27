package store

import (
	"database/sql"

	"github.com/victorarias/attn/internal/notebook"
)

func init() {
	registerMigration(migration{
		version: 1795810551524491,
		desc:    "notebook folders keep their copy name",
		apply:   applyMigration1795810551524491,
	})
}

func applyMigration1795810551524491(tx *sql.Tx) error {
	if _, err := tx.Exec(`ALTER TABLE notebook_folders ADD COLUMN copy_name TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id, root_path FROM notebook_folders`)
	if err != nil {
		return err
	}
	roots := map[string]string{}
	for rows.Next() {
		var id, root string
		if err := rows.Scan(&id, &root); err != nil {
			rows.Close()
			return err
		}
		roots[id] = root
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for id, root := range roots {
		if _, err := tx.Exec(`UPDATE notebook_folders SET copy_name = ? WHERE id = ?`, notebook.CopyName(root), id); err != nil {
			return err
		}
	}
	return nil
}
