package store

import (
	"database/sql"
	"path/filepath"
	"regexp"
	"strings"
)

func init() {
	registerMigration(migration{version: 1792371295625260, desc: "split session labels", apply: applyMigration1792371295625260})
}

func applyMigration1792371295625260(tx *sql.Tx) error {
	if _, err := tx.Exec(`ALTER TABLE sessions RENAME COLUMN label TO user_label;
 ALTER TABLE sessions ADD COLUMN harness_title TEXT NOT NULL DEFAULT '';`); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id,user_label,directory FROM sessions WHERE user_label<>'' AND agent<>'shell'`)
	if err != nil {
		return err
	}
	type labelRow struct{ id, label, directory string }
	var labels []labelRow
	for rows.Next() {
		var row labelRow
		if err := rows.Scan(&row.id, &row.label, &row.directory); err != nil {
			rows.Close()
			return err
		}
		labels = append(labels, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	idShape := regexp.MustCompile(`^(?:s-)?[a-z0-9]{6}$|^[0-9a-f]{6,}$|^[A-Z][A-Z0-9]*-[0-9]+$|^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f-]{4,}$`)
	word := regexp.MustCompile(`^(?:s-)?[a-z]{6}$`)
	for _, row := range labels {
		label := strings.TrimSpace(row.label)
		def := filepath.Base(row.directory)
		if def == "" || def == "." || def == string(filepath.Separator) {
			def = row.id
		}
		truncated := def
		if runes := []rune(def); len(runes) > 48 {
			truncated = strings.TrimRight(string(runes[:48]), "-_. \t")
		}
		if label != def && label != truncated && (!idShape.MatchString(label) || word.MatchString(label)) {
			continue
		}
		if _, err := tx.Exec(`UPDATE sessions SET user_label='',harness_title=? WHERE id=?`, row.label, row.id); err != nil {
			return err
		}
	}
	return nil
}
