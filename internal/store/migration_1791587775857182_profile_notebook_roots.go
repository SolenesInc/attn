package store

import (
	"database/sql"
	"errors"
	"github.com/victorarias/attn/internal/config"
	"strings"
)

func init() {
	registerMigration(migration{
		version: 1791587775857182,
		desc:    "profile notebook roots",
		apply:   applyMigration1791587775857182,
	})
}

func applyMigration1791587775857182(tx *sql.Tx) error {
	var root string
	err := tx.QueryRow(`SELECT value FROM settings WHERE key = 'notebook.root'`).Scan(&root)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if strings.TrimSpace(root) == "" {
		root = config.HarnessNotebookRoot()
		if root == "" {
			root = "~/attn-notebook"
			instance := strings.ToLower(strings.TrimSpace(config.Instance()))
			if instance != "" && instance != "default" {
				root += "-" + instance
			}
		}
	}
	if _, err := tx.Exec(`INSERT INTO profile_settings (profile_id, key, value) SELECT id, 'notebook.root', ? FROM profiles`, root); err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM settings WHERE key = 'notebook.root'`)
	return err
}
