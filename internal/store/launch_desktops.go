package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profilemigration"
	"github.com/victorarias/attn/internal/profiles"
)

// LaunchDesktopSetting names an existing desktop, or with DesktopName a new desktop to create;
// a numbered DesktopID then gives the new desktop that ⌘ slot.
type LaunchDesktopSetting struct {
	DesktopID   string
	DesktopName string
}

// LaunchDesktopItem is a crew member or automation and the desktop it starts on. DesktopID is
// empty only during the launch review, before Finish creates the suggested desktop.
type LaunchDesktopItem struct {
	Kind        string
	ID          string
	Name        string
	ProfileID   string
	ProfileName string
	DesktopID   string
	Label       string
	Confirmed   bool
}

func launchDesktopName(tx *sql.Tx, desktop profiles.Desktop) (string, error) {
	if desktop.Name != "" {
		return desktop.Name, nil
	}
	rows, err := tx.Query(`SELECT id, order_key FROM desktops WHERE profile_id = ?`, desktop.ProfileID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var siblings []profiles.Desktop
	for rows.Next() {
		var sibling profiles.Desktop
		if err := rows.Scan(&sibling.ID, &sibling.OrderKey); err != nil {
			return "", err
		}
		sibling.ShortcutSlot = profiles.DesktopSlot(sibling.ID)
		siblings = append(siblings, sibling)
	}
	return profiles.DesktopLabel(desktop, siblings), rows.Err()
}

func launchDesktopLabel(name string, slot int) string {
	if slot != 0 {
		return fmt.Sprintf("%d · %s", slot, name)
	}
	return name + " (no ⌘ number)"
}

// LaunchDesktopLabel names a desktop the way launch settings show it.
func (s *Store) LaunchDesktopLabel(desktopID string) (string, error) {
	var label string
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		desktop, err := loadDesktop(tx, desktopID)
		if err != nil {
			return err
		}
		name, err := launchDesktopName(tx, desktop)
		label = launchDesktopLabel(name, desktop.ShortcutSlot)
		return err
	})
	return label, err
}

func loadLaunchItem(tx *sql.Tx, kind, id string) (LaunchDesktopItem, error) {
	item := LaunchDesktopItem{Kind: kind, ID: id}
	var err error
	switch kind {
	case "automation":
		err = tx.QueryRow(`SELECT name,profile_id FROM automation_definitions WHERE id = ? AND deleted_at = ''`, id).Scan(&item.Name, &item.ProfileID)
	case "crew":
		item.Name = id
		err = tx.QueryRow(`SELECT profile_id FROM crew_profiles WHERE member_id = ?`, id).Scan(&item.ProfileID)
	default:
		return item, profiles.Errorf(profiles.CodeInvalid, "unknown launch desktop kind %q", kind)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return item, profiles.Errorf(profiles.CodeNotFound, "%s %q does not exist", kind, id)
	}
	if err != nil {
		return item, err
	}
	if err := tx.QueryRow(`SELECT name FROM profiles WHERE id = ?`, item.ProfileID).Scan(&item.ProfileName); err != nil {
		return item, err
	}
	found, err := rowFound(tx.QueryRow(`SELECT desktop_id,confirmed FROM launch_desktops WHERE kind = ? AND item_id = ?`, kind, id), &item.DesktopID, &item.Confirmed)
	if err != nil {
		return item, err
	}
	if !found {
		item.Label = item.Name + " (new)"
		return item, nil
	}
	desktop, err := loadDesktop(tx, item.DesktopID)
	if err != nil {
		return item, err
	}
	name, err := launchDesktopName(tx, desktop)
	item.Label = launchDesktopLabel(name, desktop.ShortcutSlot)
	return item, err
}

func saveLaunchSetting(tx *sql.Tx, now string, kind, id string, setting LaunchDesktopSetting, bumpAutomationRevision bool) error {
	item, err := loadLaunchItem(tx, kind, id)
	if err != nil {
		return err
	}
	desktopID := setting.DesktopID
	if name := strings.TrimSpace(setting.DesktopName); name != "" {
		desktop, err := insertDesktop(tx, now, item.ProfileID, name, profiles.DesktopSlot(desktopID))
		if err != nil {
			return err
		}
		desktopID = desktop.ID
	} else if _, err := loadLaunchDesktop(tx, profiles.Profile{ID: item.ProfileID}, desktopID); err != nil {
		return err
	}
	if kind == "automation" && bumpAutomationRevision && desktopID != item.DesktopID {
		if _, err := tx.Exec(`UPDATE automation_definitions SET revision = revision + 1 WHERE id = ?`, id); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO launch_desktops(kind,item_id,desktop_id,confirmed) VALUES (?,?,?,1)
		ON CONFLICT(kind,item_id) DO UPDATE SET desktop_id = excluded.desktop_id, confirmed = 1`, kind, id, desktopID)
	return err
}

// startOnOwnDesktop gives a new item a desktop named after it. During the launch review Finish does it instead.
func startOnOwnDesktop(tx *sql.Tx, now, kind, id string) error {
	var reviewed bool
	if err := tx.QueryRow(`SELECT launch_review_complete FROM profile_migration WHERE id = 1`).Scan(&reviewed); err != nil || !reviewed {
		return err
	}
	return createOwnLaunchDesktop(tx, now, kind, id, false)
}

func createOwnLaunchDesktop(tx *sql.Tx, now, kind, id string, confirmed bool) error {
	item, err := loadLaunchItem(tx, kind, id)
	if err != nil || item.DesktopID != "" {
		return err
	}
	desktop, err := insertDesktop(tx, now, item.ProfileID, item.Name, 0)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO launch_desktops(kind,item_id,desktop_id,confirmed) VALUES (?,?,?,?)`, kind, id, desktop.ID, confirmed)
	return err
}

func (s *Store) LaunchDesktopItem(kind, id string) (LaunchDesktopItem, error) {
	var item LaunchDesktopItem
	err := s.profilesTx(func(tx *sql.Tx, _ string) error { var err error; item, err = loadLaunchItem(tx, kind, id); return err })
	return item, err
}

func (s *Store) LaunchDesktopChoices(kind, id string) (LaunchDesktopItem, []profiles.Desktop, error) {
	var item LaunchDesktopItem
	var desktops []profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var err error
		item, err = loadLaunchItem(tx, kind, id)
		if err != nil {
			return err
		}
		desktops, err = listDesktops(tx, item.ProfileID)
		return err
	})
	return item, desktops, err
}

func (s *Store) SetLaunchDesktop(kind, id string, setting LaunchDesktopSetting) error {
	return s.profilesTx(func(tx *sql.Tx, now string) error {
		return saveLaunchSetting(tx, now, kind, id, setting, true)
	})
}

var launchItemQueries = [][2]string{
	{"automation", `SELECT id FROM automation_definitions WHERE deleted_at = '' AND profile_id IN (SELECT id FROM profiles WHERE deleted_at = '') ORDER BY id`},
	{"crew", `SELECT member_id FROM crew_profiles WHERE profile_id IN (SELECT id FROM profiles WHERE deleted_at = '') ORDER BY member_id`},
}

func launchItems(tx *sql.Tx) ([]LaunchDesktopItem, error) {
	items := []LaunchDesktopItem{}
	for _, query := range launchItemQueries {
		ids, err := queryColumn[string](tx, query[1])
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			item, err := loadLaunchItem(tx, query[0], id)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
	}
	return items, nil
}

// PrepareLaunchMigration opens the launch review once crew files are imported, which happens after SQL upgrades.
func (s *Store) PrepareLaunchMigration() error {
	return s.profilesTx(func(tx *sql.Tx, now string) error {
		var complete bool
		var phase string
		if err := tx.QueryRow(`SELECT launch_review_complete, phase FROM profile_migration WHERE id = 1`).Scan(&complete, &phase); err != nil || complete {
			return err
		}
		// Only an upgrade leaves agents without a pane; later, one would be a placement bug.
		if phase != profilemigration.PhasePlacementRequired {
			profileIDs, err := queryColumn[string](tx, `SELECT id FROM profiles WHERE deleted_at = ''`)
			if err != nil {
				return err
			}
			for _, id := range profileIDs {
				profile, err := loadLiveProfile(tx, id)
				if err != nil {
					return err
				}
				if err := placeMigrationRemainder(tx, now, profile); err != nil {
					return err
				}
			}
		}
		items, err := launchItems(tx)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			_, err = tx.Exec(`UPDATE profile_migration SET launch_review_complete = 1 WHERE id = 1`)
		} else {
			_, err = tx.Exec(`UPDATE profile_migration SET phase = 'launch_required', revision = revision + 1 WHERE phase = 'complete'`)
		}
		return err
	})
}

func launchItemDesktop(tx *sql.Tx, profile profiles.Profile, kind, id string) (profiles.Desktop, error) {
	var desktopID string
	if _, err := rowFound(tx.QueryRow(`SELECT desktop_id FROM launch_desktops WHERE kind = ? AND item_id = ?`, kind, id), &desktopID); err != nil {
		return profiles.Desktop{}, err
	}
	return loadLaunchDesktop(tx, profile, desktopID)
}

// findDesktop reports a missing desktop as not found; a reopened session's last desktop may be gone.
func findDesktop(tx *sql.Tx, id string) (profiles.Desktop, bool, error) {
	desktop, err := loadDesktop(tx, id)
	var missing *profiles.Error
	if errors.As(err, &missing) && missing.Code == profiles.CodeNotFound {
		return desktop, false, nil
	}
	return desktop, err == nil, err
}

func (s *Store) PlaceBackgroundSession(sessionID, runtimeID, kind, id string, reopen bool) (profiles.Desktop, string, error) {
	paneID := newProfileEntityID("pane")
	var desktop profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		profileID, err := openSessionProfileID(tx, sessionID)
		if err != nil {
			return err
		}
		profile, err := loadLiveProfile(tx, profileID)
		if err != nil {
			return err
		}
		if reopen {
			var last string
			if err := tx.QueryRow(`SELECT last_desktop_id FROM sessions WHERE id = ?`, sessionID).Scan(&last); err != nil {
				return err
			}
			var found bool
			desktop, found, err = findDesktop(tx, last)
			slot := profiles.DesktopSlot(last)
			switch {
			case err != nil:
			case !found && slot != 0 && last == profiles.NumberedDesktopID(profile.ID, slot):
				desktop, err = insertDesktop(tx, now, profile.ID, "", slot)
			case !found || desktop.ProfileID != profile.ID:
				desktop, err = loadLaunchDesktop(tx, profile, "")
			}
		} else {
			desktop, err = launchItemDesktop(tx, profile, kind, id)
		}
		if err != nil {
			return err
		}
		var title string
		if err := tx.QueryRow(`SELECT label FROM sessions WHERE id = ?`, sessionID).Scan(&title); err != nil {
			return err
		}
		desktop, err = placeSessionInTree(desktop, SessionPlacementRequest{SessionID: sessionID, RuntimeID: runtimeID, Title: title, Direction: layouttree.DirectionVertical, Status: profiles.PaneStatusReady}, paneID)
		if err != nil {
			return err
		}
		return writeDesktopArrangement(tx, now, &desktop)
	})
	return desktop, paneID, err
}

func finishLaunchMigration(tx *sql.Tx, now string, view *ProfileMigrationView) error {
	for _, item := range view.LaunchItems {
		if err := createOwnLaunchDesktop(tx, now, item.Kind, item.ID, true); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE launch_desktops SET confirmed = 1`); err != nil {
		return err
	}
	view.State.Phase = profilemigration.PhaseComplete
	if _, err := tx.Exec(`UPDATE profile_migration SET launch_review_complete = 1 WHERE id = 1`); err != nil {
		return err
	}
	return saveMigrationRow(tx, view)
}

func placeMigrationRemainder(tx *sql.Tx, now string, profile profiles.Profile) error {
	ids, err := queryColumn[string](tx, `SELECT id FROM sessions WHERE profile_id = ? AND closed_at = '' AND NOT EXISTS(SELECT 1 FROM desktop_panes WHERE session_id = sessions.id) ORDER BY id`, profile.ID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	desktop, err := loadLaunchDesktop(tx, profile, "")
	if err != nil {
		return err
	}
	for _, id := range ids {
		desktop, err = placeSessionInTree(desktop, SessionPlacementRequest{SessionID: id, Direction: layouttree.DirectionVertical, Status: profiles.PaneStatusReady}, newProfileEntityID("pane"))
		if err != nil {
			return err
		}
	}
	return writeDesktopArrangement(tx, now, &desktop)
}

func applyMigration167(tx *sql.Tx, migrationSQL string) error {
	if err := addProfileStampColumns(tx); err != nil {
		return err
	}
	has, err := columnExists(tx, "sessions", "last_desktop_id")
	if err != nil {
		return err
	}
	if !has {
		if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN last_desktop_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE sessions SET last_desktop_id = COALESCE((SELECT desktop_id FROM desktop_panes WHERE session_id = sessions.id), '')`); err != nil {
			return err
		}
	}
	has, err = columnExists(tx, "desktops", "empty_since")
	if err != nil {
		return err
	}
	if !has {
		if _, err := tx.Exec(`ALTER TABLE desktops ADD COLUMN empty_since TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	has, err = columnExists(tx, "profile_migration", "launch_review_complete")
	if err != nil {
		return err
	}
	if !has {
		if _, err := tx.Exec(`ALTER TABLE profile_migration ADD COLUMN launch_review_complete INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	_, err = tx.Exec(migrationSQL)
	return err
}

func (s *Store) CommitCrewSettings(w DocumentWrite, fact BusEvent, now time.Time, setting LaunchDesktopSetting) (DocumentWriteResult, error) {
	table, err := s.documentTable(w.Schema)
	if err != nil {
		return DocumentWriteResult{}, err
	}
	var result DocumentWriteResult
	err = s.profilesTx(func(tx *sql.Tx, stamp string) error {
		results, _, err := commitDocumentWritesWith(tx, []DocumentCommit{{Write: w, Fact: fact}}, []string{table}, now)
		if err != nil {
			return err
		}
		if err := saveLaunchSetting(tx, stamp, "crew", w.ID, setting, false); err != nil {
			return err
		}
		result = results[0]
		return nil
	})
	return result, err
}

func (s *Store) LaunchDesktopItems(profileID string) ([]LaunchDesktopItem, error) {
	var items []LaunchDesktopItem
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		all, err := launchItems(tx)
		for _, item := range all {
			if item.ProfileID == profileID {
				items = append(items, item)
			}
		}
		return err
	})
	return items, err
}

const removableDesktop = `tree_json = '' AND name = '' AND id != (SELECT current_desktop_id FROM profiles WHERE id = desktops.profile_id) AND NOT EXISTS (SELECT 1 FROM profile_migration WHERE phase = 'placement_required') AND NOT EXISTS (SELECT 1 FROM launch_desktops WHERE desktop_id = desktops.id)`

// stampEmptyDesktops records when each unnamed desktop no item starts on became empty and not current;
// true when one just did. It reads first so a read-only profiles transaction never takes SQLite's write lock.
func stampEmptyDesktops(tx *sql.Tx, now string) (bool, error) {
	var stale bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM desktops WHERE (empty_since = '') = (` + removableDesktop + `))`).Scan(&stale); err != nil || !stale {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE desktops SET empty_since = '' WHERE empty_since != '' AND NOT (` + removableDesktop + `)`); err != nil {
		return false, err
	}
	stamped, err := tx.Exec(`UPDATE desktops SET empty_since = ? WHERE empty_since = '' AND `+removableDesktop, now)
	if err != nil {
		return false, err
	}
	count, err := stamped.RowsAffected()
	return count > 0, err
}

// OnEmptyDesktop tells fn when a removable desktop just became empty and not current.
func (s *Store) OnEmptyDesktop(fn func(emptiedAt time.Time)) {
	s.emptyDesktop = fn
}

// PaneTerminal is one pane row as the terminal registry sees it.
type PaneTerminal struct {
	RuntimeID, SessionID string
}

// OnPaneTerminals hands fn every pane row after each commit that can change them, under the store
// lock, so the registry it feeds never sees two commits out of order.
func (s *Store) OnPaneTerminals(fn func([]PaneTerminal)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paneTerminals = fn
	s.announcePaneTerminalsLocked()
}

func (s *Store) announcePaneTerminalsLocked() {
	if s.paneTerminals == nil || s.db == nil {
		return
	}
	rows, err := s.db.Query(`SELECT runtime_id, session_id FROM desktop_panes ORDER BY created_at, pane_id`)
	if err != nil {
		log.Printf("[store] listing pane terminals: %v", err)
		return
	}
	defer rows.Close()
	var panes []PaneTerminal
	for rows.Next() {
		var pane PaneTerminal
		if err := rows.Scan(&pane.RuntimeID, &pane.SessionID); err != nil {
			log.Printf("[store] listing pane terminals: %v", err)
			return
		}
		panes = append(panes, pane)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[store] listing pane terminals: %v", err)
		return
	}
	s.paneTerminals(panes)
}

func (s *Store) announceEmptyDesktop(emptiedAt time.Time) {
	if s.emptyDesktop != nil {
		s.emptyDesktop(emptiedAt)
	}
}

// RemoveEmptyDesktops deletes removable desktops empty and not current for at least grace. It names their
// profiles and when the next remaining one is due, zero when none is.
func (s *Store) RemoveEmptyDesktops(grace time.Duration) ([]string, time.Time, error) {
	var profileIDs []string
	var next time.Time
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		if _, err := stampEmptyDesktops(tx, now); err != nil {
			return err
		}
		at, err := time.Parse(sortableTimeFormat, now)
		if err != nil {
			return err
		}
		const due = `empty_since != '' AND empty_since <= ?`
		cutoff := at.Add(-grace).Format(sortableTimeFormat)
		if profileIDs, err = queryColumn[string](tx, `SELECT DISTINCT profile_id FROM desktops WHERE `+due, cutoff); err != nil {
			return err
		}
		ids, err := queryColumn[string](tx, `SELECT id FROM desktops WHERE `+due, cutoff)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := deleteDesktop(tx, id); err != nil {
				return err
			}
		}
		var earliest string
		if err := tx.QueryRow(`SELECT COALESCE(MIN(empty_since), '') FROM desktops WHERE empty_since != ''`).Scan(&earliest); err != nil || earliest == "" {
			return err
		}
		since, err := time.Parse(sortableTimeFormat, earliest)
		next = since.Add(grace)
		return err
	})
	return profileIDs, next, err
}

// appendLaunchDesktopFacts refreshes the items that start on desktopID, whose labels name it.
func appendLaunchDesktopFacts(tx *sql.Tx, desktopID string) error {
	rows, err := tx.Query(`SELECT kind,item_id FROM launch_desktops WHERE desktop_id = ?`, desktopID)
	if err != nil {
		return err
	}
	var items [][2]string
	for rows.Next() {
		var item [2]string
		if err := rows.Scan(&item[0], &item[1]); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		name := "crew.updated"
		if item[0] == "automation" {
			name = "automation.changed"
		}
		if _, err := appendBusEventWith(tx, BusEvent{Name: name, Subject: item[1]}, time.Now()); err != nil {
			return err
		}
	}
	return nil
}
