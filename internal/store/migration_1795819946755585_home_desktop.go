package store

import (
	"database/sql"
	"time"
)

func init() {
	registerMigration(migration{
		version: 1795819946755585,
		desc:    "home desktop",
		apply:   applyMigration1795819946755585,
	})
}

func applyMigration1795819946755585(tx *sql.Tx) error {
	if _, err := tx.Exec(`ALTER TABLE desktops ADD COLUMN kind TEXT NOT NULL DEFAULT 'desktop' CHECK (kind IN ('desktop', 'home'))`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX desktops_one_home ON desktops(profile_id) WHERE kind = 'home'`); err != nil {
		return err
	}
	ids, err := queryColumn[string](tx, `SELECT id FROM profiles WHERE deleted_at = '' ORDER BY id`)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(sortableTimeFormat)
	for _, id := range ids {
		if err := insertHomeDesktop(tx, now, id); err != nil {
			return err
		}
	}
	return nil
}
