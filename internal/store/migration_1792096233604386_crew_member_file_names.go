package store

import (
	"database/sql"
	"errors"
	"path"

	"github.com/victorarias/attn/internal/docstore"
)

func init() {
	registerMigration(migration{
		version: 1792096233604386,
		desc:    "crew member file names",
		apply:   applyMigration1792096233604386,
	})
}

func applyMigration1792096233604386(tx *sql.Tx) error {
	var collectionID int64
	err := tx.QueryRow(`SELECT id FROM document_collections WHERE namespace='core/crew' AND collection='members'`).Scan(&collectionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	table := docstore.TableName(collectionID)
	rows, err := tx.Query(`SELECT id, json_extract(body, '$.letter_path') FROM ` + table + ` WHERE coalesce(json_extract(body, '$.letter_path'), '') != ''`)
	if err != nil {
		return err
	}
	letters := make(map[string]string)
	for rows.Next() {
		var id, letterPath string
		if err := rows.Scan(&id, &letterPath); err != nil {
			rows.Close()
			return err
		}
		letters[id] = path.Base(letterPath)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for id, filename := range letters {
		if _, err := tx.Exec(`UPDATE `+table+` SET body=json_set(body, '$.letter_file', ?) WHERE id=?`, filename, id); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`UPDATE ` + table + ` SET body=json_remove(body, '$.home_dir', '$.charter_path', '$.letter_path')`)
	return err
}
