package store

import "database/sql"

func addDesktopFocusHistory(tx *sql.Tx) error {
	has, err := columnExists(tx, "desktops", "focus_history")
	if err != nil || has {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE desktops ADD COLUMN focus_history TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE desktops SET focus_history = json_array(active_pane_id) WHERE active_pane_id != ''`)
	return err
}
