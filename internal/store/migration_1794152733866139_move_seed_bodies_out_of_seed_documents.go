package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

func init() {
	registerMigration(migration{
		version: 1794152733866139,
		desc:    "move seed bodies out of seed documents",
		apply:   applyMigration1794152733866139,
	})
}

func applyMigration1794152733866139(tx *sql.Tx) error {
	seeds, seedTable, found, err := readCollectionWithText(tx, "core/garden", "seeds")
	if err != nil || !found {
		return err
	}
	type seedRow struct{ id, body, createdAt, updatedAt string }
	var moving []seedRow
	rows, err := tx.Query(`SELECT id, body, created_at, updated_at FROM ` + seedTable)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row seedRow
		if err := rows.Scan(&row.id, &row.body, &row.createdAt, &row.updatedAt); err != nil {
			rows.Close()
			return err
		}
		moving = append(moving, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	bodies := docstore.CollectionSchema{Namespace: "core/garden", Collection: "seed_bodies", Text: []string{"body"}}
	now := time.Now().UTC().Format(docstore.TimeFormat)
	declared, err := tx.Exec(`INSERT INTO document_collections (namespace, collection, fields_json, text_json, updated_at) VALUES (?, ?, 'null', '["body"]', ?)`,
		bodies.Namespace, bodies.Collection, now)
	if err != nil {
		return err
	}
	id, err := declared.LastInsertId()
	if err != nil {
		return err
	}
	bodyTable := docstore.TableName(id)
	if err := createCollectionTable(tx, bodyTable, nil); err != nil {
		return err
	}
	for _, row := range moving {
		var seed struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal([]byte(row.body), &seed); err != nil {
			return fmt.Errorf("seed %s: %w", row.id, err)
		}
		document, err := json.Marshal(seed)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO `+bodyTable+` (id, body, created_at, updated_at) VALUES (?, ?, ?, ?)`, row.id, string(document), row.createdAt, row.updatedAt); err != nil {
			return err
		}
	}
	if err := createTextIndex(tx, bodyTable, bodies); err != nil {
		return err
	}

	if _, err := tx.Exec(`UPDATE ` + seedTable + ` SET body = json_remove(body, '$.body')`); err != nil {
		return err
	}
	if len(seeds.Text) > 0 {
		if _, err := tx.Exec(`DROP TABLE ` + seedTable + `_text`); err != nil {
			return err
		}
	}
	seeds.Text = []string{"title"}
	if err := createTextIndex(tx, seedTable, seeds); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE document_collections SET text_json = '["title"]', updated_at = ? WHERE namespace = 'core/garden' AND collection = 'seeds'`, now)
	return err
}
