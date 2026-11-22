package store

import (
	"database/sql"
)

func init() {
	registerMigration(migration{
		version: 1795338125260106,
		desc:    "name the harness stored crew and the advisor run",
		apply:   applyMigration1795338125260106,
	})
}

func applyMigration1795338125260106(tx *sql.Tx) error {
	_, members, found, err := readCollection(tx, "core/crew", "members")
	if err != nil {
		return err
	}
	if found {
		if _, err := tx.Exec(`UPDATE ` + members + ` SET body = json_set(body, '$.agent', 'claude'), rev = rev + 1
			WHERE TRIM(COALESCE(json_extract(body, '$.agent'), '')) = ''
			AND id NOT IN (SELECT chief_member FROM profiles WHERE chief_member != '')`); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO settings (key, value) SELECT 'garden.advisor', '{"agent":"codex"}'
		WHERE EXISTS (SELECT 1 FROM document_collections)
		AND NOT EXISTS (SELECT 1 FROM settings WHERE key = 'garden.advisor' AND (copied_from != '' OR TRIM(COALESCE(value, '')) != ''))
		ON CONFLICT (key, copied_from) DO UPDATE SET value = excluded.value`)
	return err
}
