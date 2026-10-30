package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

func init() {
	registerMigration(migration{
		version: 1793388869817139,
		desc:    "answer entry kind",
		apply:   applyMigration1793388869817139,
	})
}

func applyMigration1793388869817139(tx *sql.Tx) error {
	var collection int64
	err := tx.QueryRow(`SELECT id FROM document_collections WHERE namespace = 'core/garden' AND collection = 'notes'`).Scan(&collection)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	table := docstore.TableName(collection)
	answers := `json_extract(body, '$.kind') = 'note'
		AND COALESCE(json_extract(body, '$.author_session'), '') = ''
		AND COALESCE(json_extract(body, '$.author_member'), '') = ''
		AND json_extract(body, '$.body') GLOB '🙋 The user answered*'`
	var indexed int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = ?`, table+"_text").Scan(&indexed); err != nil {
		return err
	}
	if indexed > 0 {
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s_text SET fields_json = json_set(fields_json, '$.kind', 'answer')
			WHERE id IN (SELECT id FROM %s WHERE %s)`, table, table, answers)); err != nil {
			return err
		}
	}
	stamp := time.Now().UTC().Format(docstore.TimeFormat)
	_, err = tx.Exec(fmt.Sprintf(`UPDATE %s SET body = json_set(body, '$.kind', 'answer'), rev = rev + 1, updated_at = ?
		WHERE %s`, table, answers), stamp)
	return err
}
