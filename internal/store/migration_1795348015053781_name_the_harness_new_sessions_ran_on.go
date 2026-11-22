package store

import "database/sql"

func init() {
	registerMigration(migration{
		version: 1795348015053781,
		desc:    "name the harness new sessions ran on",
		apply:   applyMigration1795348015053781,
	})
}

func applyMigration1795348015053781(tx *sql.Tx) error {
	_, err := tx.Exec(`INSERT INTO settings (key, value) SELECT 'new_session_agent', 'claude'
		WHERE EXISTS (SELECT 1 FROM document_collections)
		AND NOT EXISTS (SELECT 1 FROM settings WHERE key = 'new_session_agent' AND (copied_from != '' OR TRIM(COALESCE(value, '')) != ''))
		ON CONFLICT (key, copied_from) DO UPDATE SET value = excluded.value`)
	return err
}
