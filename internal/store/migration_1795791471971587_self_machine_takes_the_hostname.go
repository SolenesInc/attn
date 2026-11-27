package store

import (
	"database/sql"
	"log"
	"os"
	"strings"
)

func init() {
	registerMigration(migration{
		version: 1795791471971587,
		desc:    "self machine takes the hostname",
		apply:   applyMigration1795791471971587,
	})
}

func applyMigration1795791471971587(tx *sql.Tx) error {
	name := shortHostname()
	var holders int
	if err := tx.QueryRow(`SELECT count(*) FROM machines WHERE transport != 'self' AND lower(name) = lower(?)`, name).Scan(&holders); err != nil {
		return err
	}
	if holders > 0 || strings.EqualFold(name, "home") {
		log.Printf("[store] this machine's own row stays nameless: another machine is named %q; home and its id still name it", name)
		return nil
	}
	_, err := tx.Exec(`UPDATE machines SET name = ? WHERE transport = 'self' AND name = ''`, name)
	return err
}

// shortHostname names this machine's own row: the hostname up to its first dot.
func shortHostname() string {
	host, _ := os.Hostname()
	short, _, _ := strings.Cut(host, ".")
	return short
}
