package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profilemigration"
	"github.com/victorarias/attn/internal/profiles"
)

type LaunchDesktopSetting struct {
	Label         string
	Mode          string
	DestinationID string
	DesktopID     string
	DesktopName   string
	ShortcutSlot  int
	Pending       bool
	OwnerKind     string
	OwnerID       string
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
	rows, err := tx.Query(`SELECT id, COALESCE(shortcut_slot, 0), order_key FROM desktops WHERE profile_id = ? ORDER BY order_key`, desktop.ProfileID)
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
	return profiles.DesktopLabel(desktop, siblings), rows.Err()
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
	setting := &item.Setting
	var own bool
	err = tx.QueryRow(`SELECT d.id,d.name,d.requested_slot,d.live_desktop_id,d.own,a.confirmed FROM launch_desktops a JOIN launch_destinations d ON d.id = a.destination_id WHERE a.kind = ? AND a.item_id = ?`, kind, id).Scan(&setting.DestinationID, &setting.DesktopName, &setting.ShortcutSlot, &setting.DesktopID, &own, &item.Confirmed)
	if err != nil {
		return item, err
	}
	if err := tx.QueryRow(`SELECT kind,item_id FROM launch_desktops WHERE destination_id = ? ORDER BY joined_order LIMIT 1`, setting.DestinationID).Scan(&setting.OwnerKind, &setting.OwnerID); err != nil {
		return item, err
	}
	setting.Mode = "desktop"
	if own && setting.OwnerKind == kind && setting.OwnerID == id {
		setting.Mode = "own"
	}
	setting.Pending = setting.DesktopID == ""
	setting.Label = setting.DesktopName
	slot := setting.ShortcutSlot
	if setting.DesktopID != "" {
		desktop, err := loadDesktop(tx, setting.DesktopID)
		if err != nil {
			return item, err
		}
		slot = desktop.ShortcutSlot
		setting.Label, err = launchDesktopName(tx, desktop)
		if err != nil {
			return item, err
		}
	}
	if slot != 0 {
		setting.Label = fmt.Sprintf("%d · %s", slot, setting.Label)
	} else {
		setting.Label += " (no ⌘ number)"
	}
	return item, nil
}

func saveLaunchSetting(tx *sql.Tx, kind, id string, setting LaunchDesktopSetting, bumpAutomationRevision bool) (LaunchDesktopItem, error) {
	item, err := loadLaunchItem(tx, kind, id)
	if err != nil {
		return item, err
	}
	destination := setting.DestinationID
	changed := false
	if destination != "" {
		var profileID string
		if err := tx.QueryRow(`SELECT profile_id FROM launch_destinations WHERE id = ?`, destination).Scan(&profileID); err != nil {
			return item, profiles.Errorf(profiles.CodeNotFound, "launch destination %q does not exist", destination)
		}
		if profileID != item.ProfileID {
			return item, profiles.Errorf(profiles.CodeCrossProfile, "launch destination %s belongs to profile %s, not %s", destination, profileID, item.ProfileID)
		}
	} else if setting.Mode == "own" {
		name := strings.TrimSpace(setting.DesktopName)
		if name == "" {
			name = item.Name
		}
		if setting.ShortcutSlot != 0 && (setting.ShortcutSlot < 5 || setting.ShortcutSlot > 9) {
			return item, profiles.Errorf(profiles.CodeInvalid, "launch shortcut_slot must be 0 or an explicit empty slot 5–9, asked for %d", setting.ShortcutSlot)
		}
		binding := ""
		if item.Setting.Mode == "own" {
			destination = item.Setting.DestinationID
			binding = item.Setting.DesktopID
		}
		slotChanged := setting.ShortcutSlot != item.Setting.ShortcutSlot
		if destination == "" || slotChanged {
			if err := ensureShortcutSlotFree(tx, item.ProfileID, setting.ShortcutSlot, binding); err != nil {
				return item, err
			}
		}
		if destination == "" {
			destination = newProfileEntityID("launch")
			if _, err := tx.Exec(`INSERT INTO launch_destinations(id,profile_id,name,requested_slot) VALUES (?,?,?,?)`, destination, item.ProfileID, name, setting.ShortcutSlot); err != nil {
				return item, err
			}
		} else {
			changed = name != item.Setting.DesktopName || setting.ShortcutSlot != item.Setting.ShortcutSlot
			if _, err := tx.Exec(`UPDATE launch_destinations SET name = ?,requested_slot = ? WHERE id = ?`, name, setting.ShortcutSlot, destination); err != nil {
				return item, err
			}
			if binding != "" {
				desktop, err := loadDesktop(tx, binding)
				if err != nil {
					return item, err
				}
				liveSlot := desktop.ShortcutSlot
				if slotChanged {
					liveSlot = setting.ShortcutSlot
				}
				if desktop.Name != name || desktop.ShortcutSlot != liveSlot {
					changed = true
					desktop.Name, desktop.ShortcutSlot = name, liveSlot
					if err := saveDesktop(tx, time.Now().UTC().Format(sortableTimeFormat), &desktop); err != nil {
						return item, err
					}
					if _, err := appendBusEventWith(tx, BusEvent{Name: "profile.arrangement.changed", Subject: item.ProfileID}, time.Now()); err != nil {
						return item, err
					}
				}
			}
			if changed {
				if err := appendLaunchDestinationFacts(tx, destination); err != nil {
					return item, err
				}
			}
		}
	} else if setting.Mode == "desktop" {
		desktop, err := loadLaunchDesktop(tx, profiles.Profile{ID: item.ProfileID}, setting.DesktopID)
		if err != nil {
			return item, err
		}
		found, err := rowFound(tx.QueryRow(`SELECT id FROM launch_destinations WHERE live_desktop_id = ?`, desktop.ID), &destination)
		if err != nil {
			return item, err
		}
		if !found {
			name, err := launchDesktopName(tx, desktop)
			if err != nil {
				return item, err
			}
			destination = newProfileEntityID("launch")
			if _, err := tx.Exec(`INSERT INTO launch_destinations(id,profile_id,name,live_desktop_id,own) VALUES (?,?,?,?,0)`, destination, item.ProfileID, name, desktop.ID); err != nil {
				return item, err
			}
		}
	} else {
		return item, profiles.Errorf(profiles.CodeInvalid, "unknown launch desktop mode %q; choose own or desktop", setting.Mode)
	}
	if item.Setting.DestinationID != destination || changed {
		if kind == "automation" && bumpAutomationRevision {
			if _, err := tx.Exec(`UPDATE automation_definitions SET revision = revision + 1 WHERE id = ?`, id); err != nil {
				return item, err
			}
		}
	}
	if item.Setting.DestinationID != destination {
		if _, err := tx.Exec(`DELETE FROM launch_desktops WHERE kind = ? AND item_id = ?`, kind, id); err != nil {
			return item, err
		}
		if _, err := tx.Exec(`INSERT INTO launch_desktops(kind,item_id,destination_id,confirmed) VALUES (?,?,?,1)`, kind, id, destination); err != nil {
			return item, err
		}
		for _, changedDestination := range []string{item.Setting.DestinationID, destination} {
			if err := appendLaunchDestinationFacts(tx, changedDestination); err != nil {
				return item, err
			}
		}
	} else if _, err := tx.Exec(`UPDATE launch_desktops SET confirmed = 1 WHERE kind = ? AND item_id = ?`, kind, id); err != nil {
		return item, err
	}
	if _, err := tx.Exec(`DELETE FROM launch_destinations WHERE NOT EXISTS (SELECT 1 FROM launch_desktops WHERE destination_id = launch_destinations.id)`); err != nil {
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
		item, err = saveLaunchSetting(tx, kind, id, setting, true)
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
		if _, err := tx.Exec(`UPDATE launch_destinations SET profile_id = (SELECT profile_id FROM automation_definitions WHERE launch_destinations.id = 'automation:' || id) WHERE profile_id = '' AND id IN (SELECT 'automation:' || id FROM automation_definitions)`); err != nil {
			return err
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
		if _, err := tx.Exec(`DELETE FROM launch_destinations WHERE NOT EXISTS (SELECT 1 FROM launch_desktops WHERE destination_id = launch_destinations.id)`); err != nil {
			return err
		}
		for _, id := range crewIDs {
			destination := newProfileEntityID("launch")
			if _, err := tx.Exec(`INSERT INTO launch_destinations(id,profile_id,name) SELECT ?,profile_id,member_id FROM crew_profiles WHERE member_id = ? AND NOT EXISTS(SELECT 1 FROM launch_desktops WHERE kind = 'crew' AND item_id = member_id); INSERT OR IGNORE INTO launch_desktops(kind,item_id,destination_id) SELECT 'crew',?,id FROM launch_destinations WHERE id = ?`, destination, id, id, destination); err != nil {
				return err
			}
		}
		if complete {
			return nil
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
	if kind == "" {
		return loadLaunchDesktop(tx, *profile, "")
	}
	item, err := loadLaunchItem(tx, kind, id)
	if err != nil {
		return profiles.Desktop{}, err
	}
	if item.ProfileID != profile.ID {
		return profiles.Desktop{}, profiles.Errorf(profiles.CodeCrossProfile, "%s %s belongs to profile %s, not %s", kind, id, item.ProfileID, profile.ID)
	}
	if item.Setting.DesktopID != "" {
		return loadDesktop(tx, item.Setting.DesktopID)
	}
	slot := item.Setting.ShortcutSlot
	if slot != 0 {
		var occupied bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM desktops WHERE profile_id = ? AND shortcut_slot = ?)`, profile.ID, slot).Scan(&occupied); err != nil {
			return profiles.Desktop{}, err
		}
		if occupied {
			slot = 0
		}
	}
	desktop, err := insertDesktop(tx, now, profile.ID, item.Setting.DesktopName, slot)
	if err != nil {
		return desktop, err
	}
	if _, err := tx.Exec(`UPDATE launch_destinations SET live_desktop_id = ? WHERE id = ?`, desktop.ID, item.Setting.DestinationID); err != nil {
		return desktop, err
	}
	return desktop, appendLaunchDestinationFacts(tx, item.Setting.DestinationID)
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
		if _, err := saveLaunchSetting(tx, "crew", w.ID, setting, false); err != nil {
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
		all, err := migrationLaunchItems(tx)
		if err != nil {
			return err
		}
		for _, item := range all {
			if item.ProfileID == profileID {
				items = append(items, item)
			}
		}
		return nil
	})
	return items, err
}

func moveLaunchItemProfile(tx *sql.Tx, kind, id, profileID string) error {
	var destination, owner, name, binding string
	var slot int
	var own bool
	err := tx.QueryRow(`SELECT d.id,d.profile_id,d.name,d.requested_slot,d.live_desktop_id,d.own FROM launch_desktops a JOIN launch_destinations d ON d.id = a.destination_id WHERE a.kind = ? AND a.item_id = ?`, kind, id).Scan(&destination, &owner, &name, &slot, &binding, &own)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || owner == profileID {
		return err
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM launch_desktops WHERE destination_id = ?`, destination).Scan(&count); err != nil {
		return err
	}
	if count == 1 {
		_, err = tx.Exec(`UPDATE launch_destinations SET profile_id = ?,live_desktop_id = '' WHERE id = ?`, profileID, destination)
		return err
	}
	replacement := newProfileEntityID("launch")
	if _, err := tx.Exec(`INSERT INTO launch_destinations(id,profile_id,name,requested_slot,own) VALUES (?,?,?,?,?)`, replacement, profileID, name, slot, own); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE launch_desktops SET destination_id = ? WHERE kind = ? AND item_id = ?`, replacement, kind, id)
	return err
}

func pruneEmptyDesktops(tx *sql.Tx) error {
	ids, err := queryColumn[string](tx, `SELECT d.id FROM desktops d JOIN profiles p ON p.id = d.profile_id WHERE d.tree_json = '' AND d.id != p.current_desktop_id AND NOT EXISTS (SELECT 1 FROM profile_migration WHERE phase = 'placement_required')`)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := appendBoundLaunchDesktopFacts(tx, id); err != nil {
			return err
		}
		if err := deleteDesktop(tx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) profilesArrangementTx(fn func(tx *sql.Tx, now string) error) error {
	return s.profilesTx(func(tx *sql.Tx, now string) error {
		if err := fn(tx, now); err != nil {
			return err
		}
		return pruneEmptyDesktops(tx)
	})
}

func appendLaunchDestinationFacts(tx *sql.Tx, destinationID string) error {
	rows, err := tx.Query(`SELECT kind,item_id FROM launch_desktops WHERE destination_id = ? ORDER BY joined_order`, destinationID)
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
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
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

func appendBoundLaunchDesktopFacts(tx *sql.Tx, desktopID string) error {
	ids, err := queryColumn[string](tx, `SELECT id FROM launch_destinations WHERE live_desktop_id = ?`, desktopID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := appendLaunchDestinationFacts(tx, id); err != nil {
			return err
		}
	}
	return nil
}
