package store

import (
	"database/sql"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/setting"
	"maps"
	"strings"
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
	if *desktop, err = settleForWrite(tx, *desktop, previous.Tree); err != nil {
		return err
	}
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

func shelfLayoutValue(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true")
}

func shelfLayoutOn(tx *sql.Tx) (bool, error) {
	var value string
	if _, err := rowFound(tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, setting.ShelfLayoutEnabled.Key), &value); err != nil {
		return false, err
	}
	return shelfLayoutValue(value), nil
}

func settleForWrite(tx *sql.Tx, desktop profiles.Desktop, previousTree layouttree.Node) (profiles.Desktop, error) {
	shelf, err := shelfLayoutOn(tx)
	if err != nil {
		return desktop, err
	}
	closeFocus := profiles.RecentThenLeft
	if shelf {
		closeFocus = layouttree.NextInPosition
	}
	desktop = profiles.SettleAfter(desktop, previousTree, closeFocus)
	for i := range desktop.Panes {
		desktop.Panes[i].DesktopID = desktop.ID
		if desktop.Panes[i].Status == "" {
			desktop.Panes[i].Status = profiles.PaneStatusReady
		}
	}
	return desktop, nil
}
