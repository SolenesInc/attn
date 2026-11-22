package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
)

func init() {
	registerMigration(migration{
		version: 1795345861187375,
		desc:    "backfill codex adapter terminals",
		apply:   applyMigration1795345861187375,
	})
}

func applyMigration1795345861187375(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT DISTINCT profile_id FROM sessions WHERE profile_id != ''`)
	if err != nil {
		return err
	}
	var profiles []string
	for rows.Next() {
		var profile string
		if err := rows.Scan(&profile); err != nil {
			rows.Close()
			return err
		}
		profiles = append(profiles, profile)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		sum := sha256.Sum256([]byte(profile))
		terminal := "codex-server-" + hex.EncodeToString(sum[:4])
		if _, err := tx.Exec(`INSERT OR IGNORE INTO adapter_terminals (terminal_id, owner) VALUES (?, 'codex-shared')`, terminal); err != nil {
			return err
		}
	}
	return nil
}
