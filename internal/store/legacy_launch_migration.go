package store

import (
	"database/sql"
	"errors"
	"github.com/victorarias/attn/internal/profilemigration"
	"github.com/victorarias/attn/internal/profiles"
)

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

var launchItemQueries = [][2]string{
	{"automation", `SELECT id FROM automation_definitions WHERE deleted_at = '' AND profile_id IN (SELECT id FROM profiles WHERE deleted_at = '') ORDER BY id`},
	{"crew", `SELECT member_id FROM crew_profiles WHERE profile_id IN (SELECT id FROM profiles WHERE deleted_at = '') ORDER BY member_id`},
}

func loadProfileMigration(tx *sql.Tx) (ProfileMigrationView, error) {
	var view ProfileMigrationView
	state := &view.State
	found, err := rowFound(tx.QueryRow(`SELECT schema_version, phase, revision, imported_groups, draft FROM profile_migration WHERE id = 1`),
		&state.SchemaVersion, &state.Phase, &state.Revision, &state.ImportedGroups, &state.Draft)
	if err != nil {
		return view, err
	}
	if !found {
		return view, profiles.Errorf(profiles.CodeNotFound, "no workspace conversion is recorded in this database")
	}
	if view.Manifest, err = profilemigration.DecodeManifest(state.ImportedGroups); err != nil {
		return view, err
	}
	if state.Phase == profilemigration.PhaseLaunchRequired {
		if view.LaunchItems, err = launchItems(tx); err != nil {
			return view, err
		}
		seen := map[string]bool{}
		for _, item := range view.LaunchItems {
			if seen[item.ProfileID] {
				continue
			}
			seen[item.ProfileID] = true
			desktops, err := listDesktops(tx, item.ProfileID)
			if err != nil {
				return view, err
			}
			view.LaunchDesktops = append(view.LaunchDesktops, desktops...)
		}
	}
	if !view.PlacementRequired() {
		return view, nil
	}
	if view.Plan, err = profilemigration.DecodePlan(state.Draft); err != nil {
		return view, err
	}
	desktops, err := listDesktops(tx, view.Manifest.ProfileID)
	if err != nil {
		return view, err
	}
	view.Live = profilemigration.LiveGroups(view.Manifest, desktops)
	view.Plan = view.Plan.Reconcile(desktops).Retire(view.Live)
	return view, nil
}
