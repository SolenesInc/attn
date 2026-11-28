package store

import (
	"database/sql"
	"errors"

	"github.com/victorarias/attn/internal/profilemigration"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/rankkey"
)

type ProfileMigrationView struct {
	State    profiles.MigrationState
	Manifest profilemigration.Manifest
	Plan     profilemigration.Plan
	Live     []profilemigration.GroupState
}

func (v ProfileMigrationView) PlacementRequired() bool {
	return v.State.Phase == profilemigration.PhasePlacementRequired
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

func (s *Store) ProfileMigration() (ProfileMigrationView, error) {
	var view ProfileMigrationView
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var err error
		view, err = loadProfileMigration(tx)
		return err
	})
	return view, err
}

func saveMigrationRow(tx *sql.Tx, view *ProfileMigrationView) error {
	draft, err := profilemigration.EncodePlan(view.Plan)
	if err != nil {
		return err
	}
	view.State.Draft = draft
	view.State.Revision++
	_, err = tx.Exec(`UPDATE profile_migration SET phase = ?, revision = ?, draft = ? WHERE id = 1`, view.State.Phase, view.State.Revision, view.State.Draft)
	return err
}

// FinishPendingProfileMigration completes an upgraded home's migration at start, once crew files are
// imported: every imported workspace keeps the desktop its saved draft gives it, and every launch item
// without a desktop starts on its own.
func (s *Store) FinishPendingProfileMigration() error {
	return s.profilesTx(func(tx *sql.Tx, now string) error {
		var phase string
		var launchReviewed bool
		found, err := rowFound(tx.QueryRow(`SELECT phase, launch_review_complete FROM profile_migration WHERE id = 1`), &phase, &launchReviewed)
		if err != nil || !found || phase == profilemigration.PhaseComplete && launchReviewed {
			return err
		}
		view, err := loadProfileMigration(tx)
		if err != nil {
			return err
		}
		if view.PlacementRequired() {
			if err := finishPlacement(tx, now, &view); err != nil {
				return err
			}
		} else if err := placeEveryProfilesRemainder(tx, now); err != nil {
			return err
		}
		return finishLaunchMigration(tx, &view)
	})
}

func finishPlacement(tx *sql.Tx, now string, view *ProfileMigrationView) error {
	profile, err := loadLiveProfile(tx, view.Manifest.ProfileID)
	if err != nil {
		return err
	}
	current, err := profileDesktops(tx, profile.ID)
	if err != nil {
		return err
	}
	current = ordinaryDesktops(current)
	destinations, err := queryColumn[string](tx, `SELECT DISTINCT desktop_id FROM launch_desktops`)
	if err != nil {
		return err
	}
	launchDestinations := make(map[string]bool, len(destinations))
	for _, id := range destinations {
		launchDestinations[id] = true
	}
	outcome, err := profilemigration.Materialize(view.Plan, view.Live, current, launchDestinations, func() string { return newProfileEntityID("split") })
	if err != nil {
		return err
	}
	if err := writeMigrationOutcome(tx, now, &profile, current, &outcome); err != nil {
		return err
	}
	view.State.Phase = profilemigration.PhaseComplete
	return placeMigrationRemainder(tx, now, profile)
}

func writeMigrationOutcome(tx *sql.Tx, now string, profile *profiles.Profile, current []profiles.Desktop, outcome *profilemigration.Outcome) error {
	for _, desktop := range current {
		if _, err := tx.Exec(`DELETE FROM desktop_panes WHERE desktop_id = ?`, desktop.ID); err != nil {
			return err
		}
	}
	for _, id := range outcome.Deleted {
		if err := deleteDesktop(tx, id); err != nil {
			return err
		}
	}
	lastKey := ""
	for _, desktop := range current {
		lastKey = max(lastKey, desktop.OrderKey)
	}
	for i := range outcome.Desktops {
		desktop := &outcome.Desktops[i]
		if desktop.ID == "" {
			lastKey = rankkey.After(lastKey)
			desktop.OrderKey = lastKey
			desktop.ID, desktop.ProfileID, desktop.Revision = newDesktopID(profile.ID, desktop.ShortcutSlot), profile.ID, 0
			if _, err := tx.Exec(`
				INSERT INTO desktops (id, profile_id, name, order_key, tree_json, active_pane_id, revision, created_at, updated_at)
				VALUES (?, ?, '', ?, '', '', 0, ?, ?)`,
				desktop.ID, profile.ID, desktop.OrderKey, now, now); err != nil {
				return err
			}
		}
		if err := writeCurrentDesktopArrangement(tx, now, desktop); err != nil {
			return err
		}
	}
	if len(outcome.Desktops) == 0 {
		return errors.New("finishing the migration would leave the Default profile without a desktop")
	}
	if !finalDesktopExists(outcome.Desktops, profile.CurrentDesktopID) {
		profile.CurrentDesktopID = outcome.Desktops[0].ID
	}
	return bumpProfile(tx, profile)
}

func finalDesktopExists(desktops []profiles.Desktop, id string) bool {
	for _, desktop := range desktops {
		if desktop.ID == id {
			return true
		}
	}
	return false
}

func ensureNoPendingMigration(tx *sql.Tx, profile profiles.Profile) error {
	var phase, importedGroups string
	found, err := rowFound(tx.QueryRow(`SELECT phase, imported_groups FROM profile_migration WHERE id = 1`), &phase, &importedGroups)
	if err != nil || !found || phase != profilemigration.PhasePlacementRequired {
		return err
	}
	manifest, err := profilemigration.DecodeManifest(importedGroups)
	if err != nil {
		return err
	}
	if manifest.ProfileID == profile.ID {
		return profiles.Errorf(profiles.CodeInvalid, "profile %q holds the workspace migration that is still waiting for placement; finish the migration before deleting it", profile.Name)
	}
	return nil
}
