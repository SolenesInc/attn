package store

import (
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/rankkey"
	"github.com/victorarias/attn/internal/setups"
)

type SetupDeletion struct {
	Deleted         setups.Setup
	Destination     setups.Setup
	MovedSessionIDs []string
}

type DesktopDeletion struct {
	Setup             setups.Setup
	Deleted           setups.Desktop
	UnplacedSessionID []string
}

type LeafMove struct {
	Source      setups.Desktop
	Target      setups.Desktop
	FinalLeafID string
}

type LeafMoveRequest struct {
	SourceDesktopID        string
	TargetDesktopID        string
	LeafID                 string
	AnchorID               string
	Direction              layouttree.Direction
	Before                 bool
	LeafShare              float64
	ExpectedSourceRevision int64
	ExpectedTargetRevision int64
}

type SessionPlacementRequest struct {
	DesktopID        string
	ExpectedRevision int64
	SessionID        string
	AnchorPaneID     string
	Direction        layouttree.Direction
	NewPaneShare     float64
	Title            string
	Status           setups.PaneStatus
}

type SessionSetupMove struct {
	SessionID     string
	FromSetupID   string
	ToSetupID     string
	SourceDesktop *setups.Desktop
}

func firstChildRatio(leafShare float64, leafIsFirst bool) float64 {
	if !(leafShare > 0 && leafShare < 1) {
		return layouttree.DefaultSplitRatio
	}
	if leafIsFirst {
		return leafShare
	}
	return 1 - leafShare
}

func newSetupEntityID(prefix string) string {
	return prefix + "-" + uuid.NewString()
}

func (s *Store) setupsTx(fn func(tx *sql.Tx, now string) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return setups.Errorf(setups.CodeUnavailable, "setups need the SQLite store")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx, time.Now().UTC().Format(sortableTimeFormat)); err != nil {
		return err
	}
	return tx.Commit()
}

const setupColumns = `id, name, current_desktop_id, last_used_at, revision, deleted_at`

func scanSetup(row rowScanner) (setups.Setup, error) {
	var setup setups.Setup
	err := row.Scan(&setup.ID, &setup.Name, &setup.CurrentDesktopID, &setup.LastUsedAt, &setup.Revision, &setup.DeletedAt)
	return setup, err
}

func loadSetup(tx *sql.Tx, id string) (setups.Setup, error) {
	setup, err := scanSetup(tx.QueryRow(`SELECT `+setupColumns+` FROM setups WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return setups.Setup{}, setups.Errorf(setups.CodeNotFound, "setup %q does not exist", id)
	}
	return setup, err
}

func loadLiveSetup(tx *sql.Tx, id string) (setups.Setup, error) {
	setup, err := loadSetup(tx, id)
	if err != nil {
		return setups.Setup{}, err
	}
	if setup.Deleted() {
		return setups.Setup{}, setups.Errorf(setups.CodeSetupDeleted, "setup %q (%s) was deleted at %s", setup.Name, setup.ID, setup.DeletedAt)
	}
	return setup, nil
}

func requireRevision(entity, id string, expected, current int64) error {
	if expected != current {
		return setups.Stale(entity, id, expected, current)
	}
	return nil
}

func ensureLiveSetupNameFree(tx *sql.Tx, name, exceptID string) error {
	var holder string
	err := tx.QueryRow(`SELECT id FROM setups WHERE name = ? AND deleted_at = '' AND id != ?`, name, exceptID).Scan(&holder)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return setups.Errorf(setups.CodeNameTaken, "setup name %q is already used by %s", name, holder)
}

const desktopColumns = `id, setup_id, name, COALESCE(shortcut_slot, 0), order_key, tree_json, active_pane_id, revision`

func scanDesktopRow(row rowScanner) (setups.Desktop, error) {
	var desktop setups.Desktop
	var treeJSON string
	if err := row.Scan(&desktop.ID, &desktop.SetupID, &desktop.Name, &desktop.ShortcutSlot, &desktop.OrderKey, &treeJSON, &desktop.ActivePaneID, &desktop.Revision); err != nil {
		return setups.Desktop{}, err
	}
	tree, err := layouttree.DecodeLayout(treeJSON)
	if err != nil {
		return setups.Desktop{}, setups.Errorf(setups.CodeInvalid, "desktop %s has a stored tree that does not decode: %v", desktop.ID, err)
	}
	desktop.Tree = tree
	return desktop, nil
}

type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func loadDesktopPanes(q queryer, desktopID string) ([]setups.Pane, error) {
	rows, err := q.Query(`
		SELECT pane_id, desktop_id, kind, session_id, title, status, error
		FROM desktop_panes WHERE desktop_id = ? ORDER BY created_at, pane_id`, desktopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var panes []setups.Pane
	for rows.Next() {
		var pane setups.Pane
		if err := rows.Scan(&pane.PaneID, &pane.DesktopID, &pane.Kind, &pane.SessionID, &pane.Title, &pane.Status, &pane.Error); err != nil {
			return nil, err
		}
		panes = append(panes, pane)
	}
	return panes, rows.Err()
}

func loadDesktop(q queryer, id string) (setups.Desktop, error) {
	desktop, err := scanDesktopRow(q.QueryRow(`SELECT `+desktopColumns+` FROM desktops WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return setups.Desktop{}, setups.Errorf(setups.CodeNotFound, "desktop %q does not exist", id)
	}
	if err != nil {
		return setups.Desktop{}, err
	}
	desktop.Panes, err = loadDesktopPanes(q, id)
	return desktop, err
}

func listDesktops(q queryer, setupID string) ([]setups.Desktop, error) {
	rows, err := q.Query(`SELECT `+desktopColumns+` FROM desktops WHERE setup_id = ? ORDER BY order_key, id`, setupID)
	if err != nil {
		return nil, err
	}
	var desktops []setups.Desktop
	for rows.Next() {
		desktop, err := scanDesktopRow(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		desktops = append(desktops, desktop)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range desktops {
		if desktops[i].Panes, err = loadDesktopPanes(q, desktops[i].ID); err != nil {
			return nil, err
		}
	}
	return desktops, nil
}

func insertDesktop(tx *sql.Tx, now, setupID, name string, slot int) (setups.Desktop, error) {
	if err := setups.ValidateShortcutSlot(slot); err != nil {
		return setups.Desktop{}, err
	}
	var lastKey string
	if err := tx.QueryRow(`SELECT COALESCE(MAX(order_key), '') FROM desktops WHERE setup_id = ?`, setupID).Scan(&lastKey); err != nil {
		return setups.Desktop{}, err
	}
	if slot != 0 {
		var holder string
		err := tx.QueryRow(`SELECT id FROM desktops WHERE setup_id = ? AND shortcut_slot = ?`, setupID, slot).Scan(&holder)
		if err == nil {
			return setups.Desktop{}, setups.Errorf(setups.CodeSlotTaken, "shortcut slot %d of setup %s is held by desktop %s", slot, setupID, holder)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return setups.Desktop{}, err
		}
	}
	desktop := setups.Desktop{
		ID:           newSetupEntityID("desktop"),
		SetupID:      setupID,
		Name:         strings.TrimSpace(name),
		ShortcutSlot: slot,
		OrderKey:     rankkey.After(lastKey),
		Revision:     1,
	}
	_, err := tx.Exec(`
		INSERT INTO desktops (id, setup_id, name, shortcut_slot, order_key, tree_json, active_pane_id, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, '', '', 1, ?, ?)`,
		desktop.ID, setupID, desktop.Name, slotValue(slot), desktop.OrderKey, now, now)
	return desktop, err
}

func slotValue(slot int) any {
	if slot == 0 {
		return nil
	}
	return slot
}

func lowestFreeShortcutSlot(tx *sql.Tx, setupID string) (int, error) {
	rows, err := tx.Query(`SELECT shortcut_slot FROM desktops WHERE setup_id = ? AND shortcut_slot IS NOT NULL`, setupID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	taken := make(map[int]bool)
	for rows.Next() {
		var slot int
		if err := rows.Scan(&slot); err != nil {
			return 0, err
		}
		taken[slot] = true
	}
	for slot := setups.FirstShortcutSlot; slot <= setups.LastShortcutSlot; slot++ {
		if !taken[slot] {
			return slot, rows.Err()
		}
	}
	return 0, rows.Err()
}

func bumpSetup(tx *sql.Tx, setup *setups.Setup) error {
	setup.Revision++
	_, err := tx.Exec(`UPDATE setups SET name = ?, current_desktop_id = ?, revision = ? WHERE id = ?`,
		setup.Name, setup.CurrentDesktopID, setup.Revision, setup.ID)
	return err
}

func touchSetupUse(tx *sql.Tx, setup *setups.Setup, now string) error {
	setup.LastUsedAt = now
	_, err := tx.Exec(`UPDATE setups SET last_used_at = ? WHERE id = ?`, now, setup.ID)
	return err
}

func (s *Store) CreateSetup(name string) (setups.Setup, setups.Desktop, error) {
	var setup setups.Setup
	var desktop setups.Desktop
	err := s.setupsTx(func(tx *sql.Tx, now string) error {
		trimmed, err := setups.ValidateName("setup", name)
		if err != nil {
			return err
		}
		if err := ensureLiveSetupNameFree(tx, trimmed, ""); err != nil {
			return err
		}
		setup = setups.Setup{ID: newSetupEntityID("setup"), Name: trimmed, Revision: 1}
		if desktop, err = insertDesktop(tx, now, setup.ID, "", setups.FirstShortcutSlot); err != nil {
			return err
		}
		setup.CurrentDesktopID = desktop.ID
		_, err = tx.Exec(`
			INSERT INTO setups (id, name, current_desktop_id, last_used_at, revision, created_at, deleted_at)
			VALUES (?, ?, ?, '', 1, ?, '')`, setup.ID, setup.Name, setup.CurrentDesktopID, now)
		return err
	})
	return setup, desktop, err
}

func (s *Store) GetSetup(id string) (setups.Setup, error) {
	var setup setups.Setup
	err := s.setupsTx(func(tx *sql.Tx, _ string) error {
		var err error
		setup, err = loadSetup(tx, id)
		return err
	})
	return setup, err
}

func (s *Store) ListSetups(includeDeleted bool) ([]setups.Setup, error) {
	var out []setups.Setup
	err := s.setupsTx(func(tx *sql.Tx, _ string) error {
		query := `SELECT ` + setupColumns + ` FROM setups`
		if !includeDeleted {
			query += ` WHERE deleted_at = ''`
		}
		rows, err := tx.Query(query + ` ORDER BY created_at, id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			setup, err := scanSetup(rows)
			if err != nil {
				return err
			}
			out = append(out, setup)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) MostRecentlyUsedSetup() (setups.Setup, error) {
	var setup setups.Setup
	err := s.setupsTx(func(tx *sql.Tx, _ string) error {
		var err error
		setup, err = scanSetup(tx.QueryRow(`
			SELECT ` + setupColumns + ` FROM setups WHERE deleted_at = ''
			ORDER BY last_used_at DESC, created_at, id LIMIT 1`))
		if errors.Is(err, sql.ErrNoRows) {
			return setups.Errorf(setups.CodeNotFound, "no setup exists")
		}
		return err
	})
	return setup, err
}

func (s *Store) RenameSetup(id, name string, expectedRevision int64) (setups.Setup, error) {
	var setup setups.Setup
	err := s.setupsTx(func(tx *sql.Tx, _ string) error {
		trimmed, err := setups.ValidateName("setup", name)
		if err != nil {
			return err
		}
		if setup, err = loadLiveSetup(tx, id); err != nil {
			return err
		}
		if err := requireRevision("setup", id, expectedRevision, setup.Revision); err != nil {
			return err
		}
		if err := ensureLiveSetupNameFree(tx, trimmed, id); err != nil {
			return err
		}
		setup.Name = trimmed
		return bumpSetup(tx, &setup)
	})
	return setup, err
}

func (s *Store) SelectSetup(id string) (setups.Setup, []setups.Desktop, error) {
	var setup setups.Setup
	var desktops []setups.Desktop
	err := s.setupsTx(func(tx *sql.Tx, now string) error {
		var err error
		if setup, err = loadLiveSetup(tx, id); err != nil {
			return err
		}
		if err := touchSetupUse(tx, &setup, now); err != nil {
			return err
		}
		desktops, err = listDesktops(tx, id)
		return err
	})
	return setup, desktops, err
}

func (s *Store) SetupArrangement(id string) (setups.Setup, []setups.Desktop, error) {
	var setup setups.Setup
	var desktops []setups.Desktop
	err := s.setupsTx(func(tx *sql.Tx, _ string) error {
		var err error
		if setup, err = loadSetup(tx, id); err != nil {
			return err
		}
		desktops, err = listDesktops(tx, id)
		return err
	})
	return setup, desktops, err
}

func (s *Store) DeleteSetup(id string, expectedRevision int64, destinationID string) (SetupDeletion, error) {
	var result SetupDeletion
	err := s.setupsTx(func(tx *sql.Tx, now string) error {
		setup, err := loadLiveSetup(tx, id)
		if err != nil {
			return err
		}
		if err := requireRevision("setup", id, expectedRevision, setup.Revision); err != nil {
			return err
		}
		var live int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM setups WHERE deleted_at = ''`).Scan(&live); err != nil {
			return err
		}
		if live <= 1 {
			return setups.Errorf(setups.CodeLastSetup, "setup %q (%s) is the last setup and cannot be deleted", setup.Name, setup.ID)
		}
		if strings.TrimSpace(destinationID) == "" {
			return setups.Errorf(setups.CodeInvalid, "deleting setup %q needs a destination setup for its agents", setup.Name)
		}
		if destinationID == id {
			return setups.Errorf(setups.CodeDestinationSame, "setup %s cannot be its own destination", id)
		}
		destination, err := loadLiveSetup(tx, destinationID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT id FROM sessions WHERE setup_id = ? AND closed_at = '' ORDER BY id`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var sessionID string
			if err := rows.Scan(&sessionID); err != nil {
				rows.Close()
				return err
			}
			result.MovedSessionIDs = append(result.MovedSessionIDs, sessionID)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE sessions SET setup_id = ? WHERE setup_id = ? AND closed_at = ''`, destinationID, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM desktop_panes WHERE desktop_id IN (SELECT id FROM desktops WHERE setup_id = ?)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM desktops WHERE setup_id = ?`, id); err != nil {
			return err
		}
		setup.CurrentDesktopID = ""
		setup.DeletedAt = now
		setup.Revision++
		if _, err := tx.Exec(`UPDATE setups SET current_desktop_id = '', deleted_at = ?, revision = ? WHERE id = ?`, now, setup.Revision, id); err != nil {
			return err
		}
		result.Deleted = setup
		result.Destination = destination
		return nil
	})
	return result, err
}

func (s *Store) CreateDesktop(setupID, name string, shortcutSlot int, takeFreeSlot bool) (setups.Setup, setups.Desktop, error) {
	var setup setups.Setup
	var desktop setups.Desktop
	err := s.setupsTx(func(tx *sql.Tx, now string) error {
		var err error
		if setup, err = loadLiveSetup(tx, setupID); err != nil {
			return err
		}
		if shortcutSlot == 0 && takeFreeSlot {
			if shortcutSlot, err = lowestFreeShortcutSlot(tx, setupID); err != nil {
				return err
			}
		}
		if desktop, err = insertDesktop(tx, now, setupID, name, shortcutSlot); err != nil {
			return err
		}
		return bumpSetup(tx, &setup)
	})
	return setup, desktop, err
}

func (s *Store) GetDesktop(id string) (setups.Desktop, error) {
	var desktop setups.Desktop
	err := s.setupsTx(func(tx *sql.Tx, _ string) error {
		var err error
		desktop, err = loadDesktop(tx, id)
		return err
	})
	return desktop, err
}

func saveDesktop(tx *sql.Tx, now string, desktop *setups.Desktop) error {
	treeJSON := ""
	if !layouttree.LayoutEmpty(desktop.Tree) {
		encoded, err := layouttree.EncodeLayout(desktop.Tree)
		if err != nil {
			return err
		}
		treeJSON = encoded
	}
	desktop.Revision++
	_, err := tx.Exec(`
		UPDATE desktops SET name = ?, shortcut_slot = ?, order_key = ?, tree_json = ?, active_pane_id = ?, revision = ?, updated_at = ?
		WHERE id = ?`,
		desktop.Name, slotValue(desktop.ShortcutSlot), desktop.OrderKey, treeJSON, desktop.ActivePaneID, desktop.Revision, now, desktop.ID)
	return err
}

func (s *Store) editDesktopRow(id string, expectedRevision int64, edit func(tx *sql.Tx, desktop *setups.Desktop) error) (setups.Desktop, error) {
	var desktop setups.Desktop
	err := s.setupsTx(func(tx *sql.Tx, now string) error {
		var err error
		if desktop, err = loadDesktop(tx, id); err != nil {
			return err
		}
		if err := requireRevision("desktop", id, expectedRevision, desktop.Revision); err != nil {
			return err
		}
		if err := edit(tx, &desktop); err != nil {
			return err
		}
		return saveDesktop(tx, now, &desktop)
	})
	return desktop, err
}

func (s *Store) RenameDesktop(id, name string, expectedRevision int64) (setups.Desktop, error) {
	return s.editDesktopRow(id, expectedRevision, func(_ *sql.Tx, desktop *setups.Desktop) error {
		desktop.Name = strings.TrimSpace(name)
		return nil
	})
}

func (s *Store) SetDesktopShortcutSlot(id string, slot int, expectedRevision int64) (setups.Desktop, error) {
	return s.editDesktopRow(id, expectedRevision, func(tx *sql.Tx, desktop *setups.Desktop) error {
		if err := setups.ValidateShortcutSlot(slot); err != nil {
			return err
		}
		if slot != 0 {
			var holder string
			err := tx.QueryRow(`SELECT id FROM desktops WHERE setup_id = ? AND shortcut_slot = ? AND id != ?`, desktop.SetupID, slot, id).Scan(&holder)
			if err == nil {
				return setups.Errorf(setups.CodeSlotTaken, "shortcut slot %d of setup %s is held by desktop %s", slot, desktop.SetupID, holder)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		desktop.ShortcutSlot = slot
		return nil
	})
}

func (s *Store) ReorderDesktop(id, previousID, nextID string, expectedRevision int64) (setups.Desktop, error) {
	return s.editDesktopRow(id, expectedRevision, func(tx *sql.Tx, desktop *setups.Desktop) error {
		neighbourKey := func(neighbourID string) (string, error) {
			if neighbourID == "" {
				return "", nil
			}
			if neighbourID == id {
				return "", setups.Errorf(setups.CodeInvalid, "desktop %s cannot be ordered against itself", id)
			}
			neighbour, err := loadDesktop(tx, neighbourID)
			if err != nil {
				return "", err
			}
			if neighbour.SetupID != desktop.SetupID {
				return "", setups.Errorf(setups.CodeCrossSetup, "desktop %s belongs to setup %s, not %s", neighbourID, neighbour.SetupID, desktop.SetupID)
			}
			return neighbour.OrderKey, nil
		}
		previousKey, err := neighbourKey(previousID)
		if err != nil {
			return err
		}
		nextKey, err := neighbourKey(nextID)
		if err != nil {
			return err
		}
		key, err := rankkey.Between(previousKey, nextKey)
		if err != nil {
			return setups.Errorf(setups.CodeInvalid, "desktop %s cannot be ordered between %q and %q: %v", id, previousID, nextID, err)
		}
		desktop.OrderKey = key
		return nil
	})
}

func (s *Store) DeleteDesktop(id string, expectedRevision int64) (DesktopDeletion, error) {
	var result DesktopDeletion
	err := s.setupsTx(func(tx *sql.Tx, _ string) error {
		desktop, err := loadDesktop(tx, id)
		if err != nil {
			return err
		}
		if err := requireRevision("desktop", id, expectedRevision, desktop.Revision); err != nil {
			return err
		}
		setup, err := loadLiveSetup(tx, desktop.SetupID)
		if err != nil {
			return err
		}
		siblings, err := listDesktops(tx, desktop.SetupID)
		if err != nil {
			return err
		}
		if len(siblings) <= 1 {
			return setups.Errorf(setups.CodeLastDesktop, "desktop %s is the last desktop of setup %q and cannot be deleted", id, setup.Name)
		}
		if setup.CurrentDesktopID == id {
			for i, sibling := range siblings {
				if sibling.ID != id {
					continue
				}
				if i+1 < len(siblings) {
					setup.CurrentDesktopID = siblings[i+1].ID
				} else {
					setup.CurrentDesktopID = siblings[i-1].ID
				}
			}
		}
		for _, pane := range desktop.Panes {
			result.UnplacedSessionID = append(result.UnplacedSessionID, pane.SessionID)
		}
		if _, err := tx.Exec(`DELETE FROM desktop_panes WHERE desktop_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM desktops WHERE id = ?`, id); err != nil {
			return err
		}
		if err := bumpSetup(tx, &setup); err != nil {
			return err
		}
		result.Setup = setup
		result.Deleted = desktop
		return nil
	})
	return result, err
}

func (s *Store) SetCurrentDesktop(setupID, desktopID string) (setups.Setup, error) {
	var setup setups.Setup
	err := s.setupsTx(func(tx *sql.Tx, now string) error {
		var err error
		if setup, err = loadLiveSetup(tx, setupID); err != nil {
			return err
		}
		desktop, err := loadDesktop(tx, desktopID)
		if err != nil {
			return err
		}
		if desktop.SetupID != setupID {
			return setups.Errorf(setups.CodeCrossSetup, "desktop %s belongs to setup %s, not %s", desktopID, desktop.SetupID, setupID)
		}
		setup.CurrentDesktopID = desktopID
		setup.LastUsedAt = now
		_, err = tx.Exec(`UPDATE setups SET current_desktop_id = ?, last_used_at = ? WHERE id = ?`, desktopID, now, setupID)
		return err
	})
	return setup, err
}

func (s *Store) SetActivePane(desktopID, paneID string) (setups.Setup, setups.Desktop, error) {
	var setup setups.Setup
	var desktop setups.Desktop
	err := s.setupsTx(func(tx *sql.Tx, now string) error {
		var err error
		if desktop, err = loadDesktop(tx, desktopID); err != nil {
			return err
		}
		if !layouttree.HasPane(desktop.Tree, paneID) {
			return setups.Errorf(setups.CodeNotFound, "pane %q does not belong to desktop %s", paneID, desktopID)
		}
		if setup, err = loadLiveSetup(tx, desktop.SetupID); err != nil {
			return err
		}
		desktop.ActivePaneID = paneID
		if _, err := tx.Exec(`UPDATE desktops SET active_pane_id = ?, updated_at = ? WHERE id = ?`, paneID, now, desktopID); err != nil {
			return err
		}
		return touchSetupUse(tx, &setup, now)
	})
	return setup, desktop, err
}

func checkPaneMembership(tx *sql.Tx, desktop setups.Desktop) error {
	for _, pane := range desktop.Panes {
		var persisted int
		if err := tx.QueryRow(`SELECT count(*) FROM desktop_panes WHERE pane_id = ? AND session_id = ? AND desktop_id = ?`,
			pane.PaneID, pane.SessionID, desktop.ID).Scan(&persisted); err != nil {
			return err
		}
		var setupID, closedAt string
		err := tx.QueryRow(`SELECT setup_id, closed_at FROM sessions WHERE id = ?`, pane.SessionID).Scan(&setupID, &closedAt)
		if errors.Is(err, sql.ErrNoRows) {
			if persisted == 1 {
				continue
			}
			return setups.Errorf(setups.CodeNotFound, "pane %s names session %s, which does not exist", pane.PaneID, pane.SessionID)
		}
		if err != nil {
			return err
		}
		if closedAt != "" && persisted == 0 {
			return setups.Errorf(setups.CodeSessionClosed, "pane %s names session %s, which closed at %s", pane.PaneID, pane.SessionID, closedAt)
		}
		if setupID != desktop.SetupID {
			return setups.Errorf(setups.CodeCrossSetup, "session %s belongs to setup %q, desktop %s belongs to setup %q; a layout write cannot change membership", pane.SessionID, setupID, desktop.ID, desktop.SetupID)
		}
		var holderDesktop, holderPane string
		err = tx.QueryRow(`SELECT desktop_id, pane_id FROM desktop_panes WHERE session_id = ? AND pane_id != ?`, pane.SessionID, pane.PaneID).Scan(&holderDesktop, &holderPane)
		if err == nil && holderDesktop != desktop.ID {
			return setups.Errorf(setups.CodeAlreadyPlaced, "session %s is already placed in pane %s of desktop %s", pane.SessionID, holderPane, holderDesktop)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var paneHolder string
		err = tx.QueryRow(`SELECT desktop_id FROM desktop_panes WHERE pane_id = ?`, pane.PaneID).Scan(&paneHolder)
		if err == nil && paneHolder != desktop.ID {
			return setups.Errorf(setups.CodeInvalid, "pane id %s is already used on desktop %s", pane.PaneID, paneHolder)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func writeDesktopArrangement(tx *sql.Tx, now string, desktop *setups.Desktop) error {
	if err := layouttree.Validate(desktop.Tree); err != nil {
		return setups.Errorf(setups.CodeInvalid, "desktop %s: %v", desktop.ID, err)
	}
	*desktop = setups.Settle(*desktop)
	for i := range desktop.Panes {
		desktop.Panes[i].DesktopID = desktop.ID
		if desktop.Panes[i].Status == "" {
			desktop.Panes[i].Status = setups.PaneStatusReady
		}
	}
	if err := setups.CheckDesktop(*desktop); err != nil {
		return err
	}
	if err := checkPaneMembership(tx, *desktop); err != nil {
		return err
	}
	createdAt := make(map[string]string)
	rows, err := tx.Query(`SELECT pane_id, created_at FROM desktop_panes WHERE desktop_id = ?`, desktop.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var paneID, at string
		if err := rows.Scan(&paneID, &at); err != nil {
			rows.Close()
			return err
		}
		createdAt[paneID] = at
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM desktop_panes WHERE desktop_id = ?`, desktop.ID); err != nil {
		return err
	}
	for _, pane := range desktop.Panes {
		at := createdAt[pane.PaneID]
		if at == "" {
			at = now
		}
		if _, err := tx.Exec(`
			INSERT INTO desktop_panes (pane_id, desktop_id, kind, session_id, title, status, error, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			pane.PaneID, desktop.ID, string(pane.Kind), pane.SessionID, pane.Title, string(pane.Status), pane.Error, at, now); err != nil {
			return err
		}
	}
	return saveDesktop(tx, now, desktop)
}

func (s *Store) UpdateDesktopArrangement(id string, expectedRevision int64, edit func(desktop setups.Desktop) (setups.Desktop, error)) (setups.Desktop, error) {
	var desktop setups.Desktop
	err := s.setupsTx(func(tx *sql.Tx, now string) error {
		current, err := loadDesktop(tx, id)
		if err != nil {
			return err
		}
		if err := requireRevision("desktop", id, expectedRevision, current.Revision); err != nil {
			return err
		}
		if _, err := loadLiveSetup(tx, current.SetupID); err != nil {
			return err
		}
		edited, err := edit(current)
		if err != nil {
			return err
		}
		edited.ID, edited.SetupID, edited.Revision = current.ID, current.SetupID, current.Revision
		edited.Name, edited.ShortcutSlot, edited.OrderKey = current.Name, current.ShortcutSlot, current.OrderKey
		desktop = edited
		return writeDesktopArrangement(tx, now, &desktop)
	})
	return desktop, err
}

func (s *Store) PlaceSession(request SessionPlacementRequest) (setups.Desktop, string, error) {
	paneID := newSetupEntityID("pane")
	desktop, err := s.UpdateDesktopArrangement(request.DesktopID, request.ExpectedRevision, func(desktop setups.Desktop) (setups.Desktop, error) {
		anchor := strings.TrimSpace(request.AnchorPaneID)
		if anchor == "" {
			anchor = desktop.ActivePaneID
		}
		switch {
		case layouttree.LayoutEmpty(desktop.Tree):
			desktop.Tree = layouttree.DefaultLayout(paneID)
		case anchor == "":
			leaves := layouttree.TileIDs(desktop.Tree)
			next, ok := layouttree.MoveLeafBetweenLayouts(layouttree.DefaultLayout(paneID), desktop.Tree, paneID, "", newSetupEntityID("split"), request.Direction, false, firstChildRatio(request.NewPaneShare, false), "")
			if !ok {
				return desktop, setups.Errorf(setups.CodeInvalid, "desktop %s holds only tiles %v and the new pane could not dock beside them", desktop.ID, leaves)
			}
			desktop.Tree = next.TargetLayout
		default:
			splitID := newSetupEntityID("split")
			ratio := firstChildRatio(request.NewPaneShare, false)
			next, ok := layouttree.Split(desktop.Tree, anchor, paneID, splitID, request.Direction, ratio)
			if !ok {
				return desktop, setups.Errorf(setups.CodeNotFound, "anchor pane %q does not belong to desktop %s", anchor, desktop.ID)
			}
			if request.NewPaneShare > 0 && request.NewPaneShare < 1 {
				next, _ = layouttree.SetSplitRatio(next, splitID, ratio)
			}
			desktop.Tree = next
		}
		title := strings.TrimSpace(request.Title)
		desktop.Panes = append(desktop.Panes, setups.Pane{
			PaneID:    paneID,
			Kind:      setups.PaneKindAgent,
			SessionID: strings.TrimSpace(request.SessionID),
			Title:     title,
			Status:    request.Status,
		})
		desktop.ActivePaneID = paneID
		return desktop, nil
	})
	return desktop, paneID, err
}

func withoutPane(panes []setups.Pane, paneID string) []setups.Pane {
	kept := make([]setups.Pane, 0, len(panes))
	for _, pane := range panes {
		if pane.PaneID != paneID {
			kept = append(kept, pane)
		}
	}
	return kept
}

func (s *Store) RemoveLeaf(desktopID, leafID string, expectedRevision int64) (setups.Desktop, error) {
	return s.UpdateDesktopArrangement(desktopID, expectedRevision, func(desktop setups.Desktop) (setups.Desktop, error) {
		next, ok := layouttree.Remove(desktop.Tree, leafID)
		if !ok {
			return desktop, setups.Errorf(setups.CodeNotFound, "leaf %q does not belong to desktop %s", leafID, desktopID)
		}
		desktop.Tree = next
		desktop.Panes = withoutPane(desktop.Panes, leafID)
		return desktop, nil
	})
}

func (s *Store) SetDesktopSplitRatio(desktopID, splitID string, ratio float64, expectedRevision int64) (setups.Desktop, error) {
	return s.UpdateDesktopArrangement(desktopID, expectedRevision, func(desktop setups.Desktop) (setups.Desktop, error) {
		next, ok := layouttree.SetSplitRatio(desktop.Tree, splitID, ratio)
		if !ok {
			return desktop, setups.Errorf(setups.CodeNotFound, "split %q does not belong to desktop %s", splitID, desktopID)
		}
		desktop.Tree = next
		return desktop, nil
	})
}

func (s *Store) MoveLeaf(request LeafMoveRequest) (LeafMove, error) {
	var result LeafMove
	if request.SourceDesktopID == request.TargetDesktopID {
		desktop, err := s.UpdateDesktopArrangement(request.SourceDesktopID, request.ExpectedSourceRevision, func(desktop setups.Desktop) (setups.Desktop, error) {
			next, ok := layouttree.MoveLeaf(desktop.Tree, request.LeafID, request.AnchorID, newSetupEntityID("split"), request.Direction, request.Before, firstChildRatio(request.LeafShare, request.Before))
			if !ok {
				return desktop, setups.Errorf(setups.CodeInvalid, "leaf %q could not move beside %q on desktop %s", request.LeafID, request.AnchorID, desktop.ID)
			}
			desktop.Tree = next
			return desktop, nil
		})
		return LeafMove{Source: desktop, Target: desktop, FinalLeafID: request.LeafID}, err
	}
	err := s.setupsTx(func(tx *sql.Tx, now string) error {
		source, err := loadDesktop(tx, request.SourceDesktopID)
		if err != nil {
			return err
		}
		target, err := loadDesktop(tx, request.TargetDesktopID)
		if err != nil {
			return err
		}
		if err := requireRevision("desktop", source.ID, request.ExpectedSourceRevision, source.Revision); err != nil {
			return err
		}
		if err := requireRevision("desktop", target.ID, request.ExpectedTargetRevision, target.Revision); err != nil {
			return err
		}
		if source.SetupID != target.SetupID {
			return setups.Errorf(setups.CodeCrossSetup, "desktop %s belongs to setup %s and desktop %s to setup %s; a move between desktops cannot change membership", source.ID, source.SetupID, target.ID, target.SetupID)
		}
		if _, err := loadLiveSetup(tx, source.SetupID); err != nil {
			return err
		}
		moved, ok := layouttree.MoveLeafBetweenLayouts(source.Tree, target.Tree, request.LeafID, request.AnchorID, newSetupEntityID("split"), request.Direction, request.Before, firstChildRatio(request.LeafShare, request.Before), uuid.NewString())
		if !ok {
			return setups.Errorf(setups.CodeInvalid, "leaf %q could not move from desktop %s beside %q on desktop %s", request.LeafID, source.ID, request.AnchorID, target.ID)
		}
		source.Tree, target.Tree = moved.SourceLayout, moved.TargetLayout
		for _, pane := range source.Panes {
			if pane.PaneID == request.LeafID {
				pane.PaneID = moved.FinalLeafID
				target.Panes = append(target.Panes, pane)
				target.ActivePaneID = moved.FinalLeafID
			}
		}
		source.Panes = withoutPane(source.Panes, request.LeafID)
		if err := writeDesktopArrangement(tx, now, &source); err != nil {
			return err
		}
		if err := writeDesktopArrangement(tx, now, &target); err != nil {
			return err
		}
		result = LeafMove{Source: source, Target: target, FinalLeafID: moved.FinalLeafID}
		return nil
	})
	return result, err
}

func (s *Store) AssignSessionSetup(sessionID, setupID string) error {
	return s.setupsTx(func(tx *sql.Tx, _ string) error {
		if _, err := loadLiveSetup(tx, setupID); err != nil {
			return err
		}
		var current, closedAt string
		err := tx.QueryRow(`SELECT setup_id, closed_at FROM sessions WHERE id = ?`, sessionID).Scan(&current, &closedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return setups.Errorf(setups.CodeNotFound, "session %q does not exist", sessionID)
		}
		if err != nil {
			return err
		}
		if closedAt != "" {
			return setups.Errorf(setups.CodeSessionClosed, "session %s closed at %s; closed sessions keep their setup as history", sessionID, closedAt)
		}
		if current != "" && current != setupID {
			return setups.Errorf(setups.CodeCrossSetup, "session %s already belongs to setup %s; membership changes only through a move", sessionID, current)
		}
		_, err = tx.Exec(`UPDATE sessions SET setup_id = ? WHERE id = ? AND closed_at = ''`, setupID, sessionID)
		return err
	})
}

func (s *Store) SessionSetupID(sessionID string) (string, error) {
	var setupID string
	err := s.setupsTx(func(tx *sql.Tx, _ string) error {
		err := tx.QueryRow(`SELECT setup_id FROM sessions WHERE id = ?`, sessionID).Scan(&setupID)
		if errors.Is(err, sql.ErrNoRows) {
			return setups.Errorf(setups.CodeNotFound, "session %q does not exist", sessionID)
		}
		return err
	})
	return setupID, err
}

func (s *Store) SessionPlacement(sessionID string) (setups.Placement, bool, error) {
	var placement setups.Placement
	found := false
	err := s.setupsTx(func(tx *sql.Tx, _ string) error {
		err := tx.QueryRow(`
			SELECT d.setup_id, p.desktop_id, p.pane_id
			FROM desktop_panes p JOIN desktops d ON d.id = p.desktop_id
			WHERE p.session_id = ?`, sessionID).Scan(&placement.SetupID, &placement.DesktopID, &placement.PaneID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		found = err == nil
		return err
	})
	return placement, found, err
}

func removeSessionPlacement(tx *sql.Tx, now, sessionID string) (*setups.Desktop, error) {
	var desktopID, paneID string
	err := tx.QueryRow(`SELECT desktop_id, pane_id FROM desktop_panes WHERE session_id = ?`, sessionID).Scan(&desktopID, &paneID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	desktop, err := loadDesktop(tx, desktopID)
	if err != nil {
		return nil, err
	}
	next, ok := layouttree.Remove(desktop.Tree, paneID)
	if !ok {
		return nil, setups.Errorf(setups.CodeInvalid, "pane %s has a row on desktop %s but no leaf in its tree", paneID, desktopID)
	}
	desktop.Tree = next
	desktop.Panes = withoutPane(desktop.Panes, paneID)
	if err := writeDesktopArrangement(tx, now, &desktop); err != nil {
		return nil, err
	}
	return &desktop, nil
}

func (s *Store) unplaceSessionsLocked(reason, where string, args ...any) {
	rows, err := s.db.Query(`SELECT session_id FROM desktop_panes WHERE `+where, args...)
	if err != nil {
		log.Printf("[store] %s: listing placed sessions: %v", reason, err)
		return
	}
	var sessionIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			log.Printf("[store] %s: listing placed sessions: %v", reason, err)
			return
		}
		sessionIDs = append(sessionIDs, id)
	}
	if err := rows.Close(); err != nil {
		log.Printf("[store] %s: listing placed sessions: %v", reason, err)
		return
	}
	now := time.Now().UTC().Format(sortableTimeFormat)
	for _, id := range sessionIDs {
		tx, err := s.db.Begin()
		if err != nil {
			log.Printf("[store] %s: removing the pane of session %s: %v", reason, id, err)
			continue
		}
		if _, err := removeSessionPlacement(tx, now, id); err != nil {
			tx.Rollback()
			log.Printf("[store] %s: removing the pane of session %s: %v", reason, id, err)
			continue
		}
		if err := tx.Commit(); err != nil {
			log.Printf("[store] %s: removing the pane of session %s: %v", reason, id, err)
		}
	}
}

func (s *Store) RemoveSessionPlacement(sessionID string) (*setups.Desktop, error) {
	var desktop *setups.Desktop
	err := s.setupsTx(func(tx *sql.Tx, now string) error {
		var err error
		desktop, err = removeSessionPlacement(tx, now, sessionID)
		return err
	})
	return desktop, err
}

func (s *Store) MoveSessionToSetup(sessionID, destinationSetupID string) (SessionSetupMove, error) {
	move := SessionSetupMove{SessionID: sessionID, ToSetupID: destinationSetupID}
	err := s.setupsTx(func(tx *sql.Tx, now string) error {
		if _, err := loadLiveSetup(tx, destinationSetupID); err != nil {
			return err
		}
		var closedAt string
		err := tx.QueryRow(`SELECT setup_id, closed_at FROM sessions WHERE id = ?`, sessionID).Scan(&move.FromSetupID, &closedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return setups.Errorf(setups.CodeNotFound, "session %q does not exist", sessionID)
		}
		if err != nil {
			return err
		}
		if closedAt != "" {
			return setups.Errorf(setups.CodeSessionClosed, "session %s closed at %s; closed sessions keep their setup as history", sessionID, closedAt)
		}
		if move.FromSetupID == destinationSetupID {
			return setups.Errorf(setups.CodeDestinationSame, "session %s already belongs to setup %s", sessionID, destinationSetupID)
		}
		if move.SourceDesktop, err = removeSessionPlacement(tx, now, sessionID); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE sessions SET setup_id = ? WHERE id = ? AND closed_at = ''`, destinationSetupID, sessionID)
		return err
	})
	return move, err
}

func (s *Store) GetSetupMigration() (setups.MigrationState, bool, error) {
	var state setups.MigrationState
	found := false
	err := s.setupsTx(func(tx *sql.Tx, _ string) error {
		err := tx.QueryRow(`SELECT schema_version, phase, revision, imported_groups, draft FROM setup_migration WHERE id = 1`).
			Scan(&state.SchemaVersion, &state.Phase, &state.Revision, &state.ImportedGroups, &state.Draft)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		found = err == nil
		return err
	})
	return state, found, err
}

func (s *Store) SaveSetupMigration(state setups.MigrationState, expectedRevision int64) (setups.MigrationState, error) {
	err := s.setupsTx(func(tx *sql.Tx, _ string) error {
		if strings.TrimSpace(state.Phase) == "" {
			return setups.Errorf(setups.CodeInvalid, "migration phase is empty")
		}
		var current int64
		err := tx.QueryRow(`SELECT revision FROM setup_migration WHERE id = 1`).Scan(&current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := requireRevision("migration", "state", expectedRevision, current); err != nil {
			return err
		}
		state.Revision = current + 1
		_, err = tx.Exec(`
			INSERT INTO setup_migration (id, schema_version, phase, revision, imported_groups, draft)
			VALUES (1, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				schema_version = excluded.schema_version,
				phase = excluded.phase,
				revision = excluded.revision,
				imported_groups = excluded.imported_groups,
				draft = excluded.draft`,
			state.SchemaVersion, state.Phase, state.Revision, state.ImportedGroups, state.Draft)
		return err
	})
	return state, err
}
