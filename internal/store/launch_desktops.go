package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profilemigration"
	"github.com/victorarias/attn/internal/profiles"
)

type LaunchDesktopSetting struct {
	Label       string
	DesktopName string
	Mode        string
	DesktopID   string
	Fallback    bool
}

type LaunchDesktopItem struct {
	Kind        string
	ID          string
	Name        string
	ProfileID   string
	ProfileName string
	Setting     LaunchDesktopSetting
	Confirmed   bool
}

func launchDesktopName(tx *sql.Tx, desktop profiles.Desktop) (string, error) {
	if desktop.Name != "" {
		return desktop.Name, nil
	}
	rows, err := tx.Query(`SELECT id, shortcut_slot, order_key FROM desktops WHERE profile_id = ? ORDER BY order_key`, desktop.ProfileID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var siblings []profiles.Desktop
	for rows.Next() {
		var sibling profiles.Desktop
		if err := rows.Scan(&sibling.ID, &sibling.ShortcutSlot, &sibling.OrderKey); err != nil {
			return "", err
		}
		siblings = append(siblings, sibling)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return profiles.DesktopLabel(desktop, siblings), nil
}

func loadLaunchItem(tx *sql.Tx, kind, id string) (LaunchDesktopItem, error) {
	item := LaunchDesktopItem{Kind: kind, ID: id}
	var err error
	switch kind {
	case "automation":
		item.Setting.Mode = "dedicated"
		err = tx.QueryRow(`SELECT name, profile_id FROM automation_definitions WHERE id = ? AND deleted_at = ''`, id).Scan(&item.Name, &item.ProfileID)
	case "crew":
		item.Setting.Mode = "current"
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
	err = tx.QueryRow(`SELECT mode, desktop_id, desktop_name, confirmed FROM launch_desktops WHERE kind = ? AND item_id = ?`, kind, id).Scan(&item.Setting.Mode, &item.Setting.DesktopID, &item.Setting.DesktopName, &item.Confirmed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return item, err
	}
	if err := tx.QueryRow(`SELECT name FROM profiles WHERE id = ?`, item.ProfileID).Scan(&item.ProfileName); err != nil {
		return item, err
	}
	item.Setting.Label = "current desktop"
	if item.Setting.Mode == "dedicated" {
		item.Setting.Label = "dedicated (made on first run)"
	}
	if item.Setting.Mode == "desktop" {
		desktop, err := loadDesktop(tx, item.Setting.DesktopID)
		var missing *profiles.Error
		if err != nil && (!errors.As(err, &missing) || missing.Code != profiles.CodeNotFound) {
			return item, err
		}
		item.Setting.Fallback = err != nil || desktop.ProfileID != item.ProfileID
		if item.Setting.Fallback {
			item.Setting.Label = item.Setting.DesktopName + " (unavailable) → current desktop"
		} else {
			item.Setting.Label, err = launchDesktopName(tx, desktop)
			if err != nil {
				return item, err
			}
			if desktop.ShortcutSlot != 0 {
				item.Setting.Label = fmt.Sprintf("%d · %s", desktop.ShortcutSlot, item.Setting.Label)
			}
		}
	}
	return item, nil
}

func saveLaunchSetting(tx *sql.Tx, kind, id string, setting LaunchDesktopSetting) (LaunchDesktopItem, error) {
	item, err := loadLaunchItem(tx, kind, id)
	if err != nil {
		return item, err
	}
	switch setting.Mode {
	case "current":
		setting.DesktopID = ""
	case "dedicated":
		if kind != "automation" {
			return item, profiles.Errorf(profiles.CodeInvalid, "only automations have a dedicated launch desktop")
		}
		setting.DesktopID = ""
	case "desktop":
		desktop, err := loadLaunchDesktop(tx, profiles.Profile{ID: item.ProfileID}, setting.DesktopID)
		if err != nil {
			return item, err
		}
		setting.DesktopName, err = launchDesktopName(tx, desktop)
		if err != nil {
			return item, err
		}
	default:
		return item, profiles.Errorf(profiles.CodeInvalid, "unknown launch desktop mode %q", setting.Mode)
	}
	_, err = tx.Exec(`INSERT INTO launch_desktops(kind, item_id, mode, desktop_id, desktop_name, confirmed) VALUES (?, ?, ?, ?, ?, 1)
 ON CONFLICT(kind, item_id) DO UPDATE SET mode = excluded.mode, desktop_id = excluded.desktop_id, desktop_name = excluded.desktop_name, confirmed = 1`, kind, id, setting.Mode, setting.DesktopID, setting.DesktopName)
	if err != nil {
		return item, err
	}
	return loadLaunchItem(tx, kind, id)
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

func (s *Store) SetLaunchDesktop(kind, id string, setting LaunchDesktopSetting) (LaunchDesktopItem, error) {
	var item LaunchDesktopItem
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var err error
		item, err = saveLaunchSetting(tx, kind, id, setting)
		if err != nil {
			return err
		}
		return nil
	})
	return item, err
}

func migrationLaunchItems(tx *sql.Tx) ([]LaunchDesktopItem, error) {
	rows, err := tx.Query(`SELECT kind, item_id FROM launch_desktops ORDER BY kind, item_id`)
	if err != nil {
		return nil, err
	}
	var keys [][2]string
	for rows.Next() {
		var key [2]string
		if err := rows.Scan(&key[0], &key[1]); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	items := []LaunchDesktopItem{}
	for _, key := range keys {
		item, err := loadLaunchItem(tx, key[0], key[1])
		var missing *profiles.Error
		if errors.As(err, &missing) && missing.Code == profiles.CodeNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// Crew files are imported after SQL upgrades, so finalize the review roster at startup.
func (s *Store) PrepareLaunchMigration(crewIDs []string) error {
	return s.profilesTx(func(tx *sql.Tx, now string) error {
		var complete bool
		if err := tx.QueryRow(`SELECT launch_review_complete FROM profile_migration WHERE id = 1`).Scan(&complete); err != nil {
			return err
		}
		if complete {
			return nil
		}
		profileIDs, err := queryColumn[string](tx, `SELECT id FROM profiles WHERE deleted_at = ''`)
		if err != nil {
			return err
		}
		var phase string
		if err := tx.QueryRow(`SELECT phase FROM profile_migration WHERE id = 1`).Scan(&phase); err != nil {
			return err
		}
		for _, id := range profileIDs {
			if phase == profilemigration.PhasePlacementRequired {
				continue
			}
			profile, err := loadLiveProfile(tx, id)
			if err != nil {
				return err
			}
			if err := placeMigrationRemainder(tx, now, profile); err != nil {
				return err
			}
		}
		known := map[string]bool{}
		for _, id := range crewIDs {
			known[id] = true
		}
		recorded, err := queryColumn[string](tx, `SELECT item_id FROM launch_desktops WHERE kind = 'crew'`)
		if err != nil {
			return err
		}
		for _, id := range recorded {
			if !known[id] {
				if _, err := tx.Exec(`DELETE FROM launch_desktops WHERE kind = 'crew' AND item_id = ?`, id); err != nil {
					return err
				}
			}
		}
		for _, id := range crewIDs {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO launch_desktops(kind, item_id, mode) VALUES ('crew', ?, 'current')`, id); err != nil {
				return err
			}
		}
		items, err := migrationLaunchItems(tx)
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

func launchItemDesktop(tx *sql.Tx, now string, profile *profiles.Profile, kind, id string) (profiles.Desktop, error) {
	item, err := loadLaunchItem(tx, kind, id)
	if err != nil {
		return profiles.Desktop{}, err
	}
	if item.ProfileID != profile.ID {
		return profiles.Desktop{}, profiles.Errorf(profiles.CodeCrossProfile, "%s %s belongs to profile %s, not %s", kind, id, item.ProfileID, profile.ID)
	}
	switch item.Setting.Mode {
	case "dedicated":
		slot, err := lowestFreeShortcutSlot(tx, profile.ID)
		if err != nil {
			return profiles.Desktop{}, err
		}
		desktop, err := insertDesktop(tx, now, profile.ID, item.Name, slot)
		if err != nil {
			return desktop, err
		}
		if _, err := tx.Exec(`INSERT INTO launch_desktops(kind, item_id, mode, desktop_id, desktop_name, confirmed) VALUES (?, ?, 'desktop', ?, ?, 1)
    ON CONFLICT(kind, item_id) DO UPDATE SET mode = excluded.mode, desktop_id = excluded.desktop_id, desktop_name = excluded.desktop_name`, kind, id, desktop.ID, desktop.Name); err != nil {
			return desktop, err
		}
		return desktop, bumpProfile(tx, profile)
	case "desktop":
		if !item.Setting.Fallback {
			return loadDesktop(tx, item.Setting.DesktopID)
		}
	}
	return loadLaunchDesktop(tx, *profile, "")
}

func (s *Store) PlaceBackgroundSession(sessionID, kind, id string, reopen bool) (profiles.Desktop, string, error) {
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
			desktop, err = loadDesktop(tx, last)
			var missing *profiles.Error
			if err != nil && (!errors.As(err, &missing) || missing.Code != profiles.CodeNotFound) {
				return err
			}
			if err != nil || desktop.ProfileID != profile.ID {
				desktop, err = loadLaunchDesktop(tx, profile, "")
			}
		} else {
			desktop, err = launchItemDesktop(tx, now, &profile, kind, id)
		}
		if err != nil {
			return err
		}
		var title string
		if err := tx.QueryRow(`SELECT label FROM sessions WHERE id = ?`, sessionID).Scan(&title); err != nil {
			return err
		}
		desktop, err = placeSessionInTree(desktop, SessionPlacementRequest{SessionID: sessionID, Title: title, Direction: layouttree.DirectionVertical, Status: profiles.PaneStatusReady}, paneID)
		if err != nil {
			return err
		}
		return writeDesktopArrangement(tx, now, &desktop)
	})
	return desktop, paneID, err
}

func finishLaunchMigration(tx *sql.Tx, view *ProfileMigrationView) error {
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
	err = s.profilesTx(func(tx *sql.Tx, _ string) error {
		results, _, err := commitDocumentWritesWith(tx, []DocumentCommit{{Write: w, Fact: fact}}, []string{table}, now)
		if err != nil {
			return err
		}
		if _, err := saveLaunchSetting(tx, "crew", w.ID, setting); err != nil {
			return err
		}
		result = results[0]
		return nil
	})
	return result, err
}
