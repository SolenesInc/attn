package store

import (
	"database/sql"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"maps"
)

func writeDesktopArrangement(tx *sql.Tx, now string, desktop *profiles.Desktop) error {
	return writeArrivingArrangement(tx, now, desktop, nil)
}

func writeArrivingArrangement(tx *sql.Tx, now string, desktop *profiles.Desktop, arrivingCreatedAt map[string]string) error {
	if err := layouttree.Validate(desktop.Tree); err != nil {
		return profiles.Errorf(profiles.CodeInvalid, "desktop %s: %v", desktop.ID, err)
	}
	previous, err := scanDesktopRow(tx.QueryRow(`SELECT `+desktopColumns+` FROM desktops WHERE id = ?`, desktop.ID))
	if err != nil {
		return err
	}
	desktop.FocusHistory = profiles.Focus(previous, previous.ActivePaneID).FocusHistory
	*desktop = settleForWrite(*desktop, previous.Tree)
	if err := profiles.CheckDesktop(*desktop); err != nil {
		return err
	}
	if err := checkPaneMembership(tx, *desktop, arrivingCreatedAt); err != nil {
		return err
	}
	createdAt, err := paneCreationTimes(tx, desktop.ID)
	if err != nil {
		return err
	}
	maps.Copy(createdAt, arrivingCreatedAt)
	if _, err := tx.Exec(`DELETE FROM desktop_panes WHERE desktop_id = ?`, desktop.ID); err != nil {
		return err
	}
	if err := insertDesktopPanes(tx, now, *desktop, createdAt); err != nil {
		return err
	}
	return saveDesktop(tx, now, desktop)
}

func insertDesktopPanes(tx *sql.Tx, now string, desktop profiles.Desktop, createdAt map[string]string) error {
	for _, pane := range desktop.Panes {
		at := createdAt[pane.PaneID]
		if at == "" {
			at = now
		}
		if _, err := tx.Exec(`
			INSERT INTO desktop_panes (pane_id, desktop_id, kind, session_id, runtime_id, title, status, error, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			pane.PaneID, desktop.ID, string(pane.Kind), pane.SessionID, pane.RuntimeID, pane.Title, string(pane.Status), pane.Error, at, now); err != nil {
			return err
		}
	}
	return nil
}
