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

type ProfileMigrationFinish struct {
	View     ProfileMigrationView
	Finished bool
	Profile  profiles.Profile
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

func requirePlacement(view ProfileMigrationView, expectedRevision int64) error {
	if !view.PlacementRequired() {
		return profiles.Errorf(profiles.CodeInvalid, "the workspace migration is already %s", view.State.Phase)
	}
	return requireRevision("migration", "draft", expectedRevision, view.State.Revision)
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

func (s *Store) EditProfileMigration(expectedRevision int64, edit func(plan profilemigration.Plan, live []profilemigration.GroupState) (profilemigration.Plan, error)) (ProfileMigrationView, error) {
	var view ProfileMigrationView
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var err error
		if view, err = loadProfileMigration(tx); err != nil {
			return err
		}
		if err := requirePlacement(view, expectedRevision); err != nil {
			return err
		}
		edited, err := edit(view.Plan, view.Live)
		if err != nil {
			return err
		}
		if err := edited.Check(view.Live); err != nil {
			return err
		}
		view.Plan = edited
		return saveMigrationRow(tx, &view)
	})
	return view, err
}

func (s *Store) FinishProfileMigration(expectedRevision int64) (ProfileMigrationFinish, error) {
	var result ProfileMigrationFinish
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		view, err := loadProfileMigration(tx)
		if err != nil {
			return err
		}
		result.View = view
		if view.State.Phase == profilemigration.PhaseComplete {
			return nil
		}
		if err := requirePlacement(view, expectedRevision); err != nil {
			return err
		}
		profile, err := loadLiveProfile(tx, view.Manifest.ProfileID)
		if err != nil {
			return err
		}
		current, err := listDesktops(tx, profile.ID)
		if err != nil {
			return err
		}
		outcome, err := profilemigration.Materialize(view.Plan, view.Live, current, func() string { return newProfileEntityID("split") })
		if err != nil {
			return err
		}
		if err := writeMigrationOutcome(tx, now, &profile, current, &outcome); err != nil {
			return err
		}
		view.State.Phase = profilemigration.PhaseComplete
		if err := saveMigrationRow(tx, &view); err != nil {
			return err
		}
		result = ProfileMigrationFinish{View: view, Finished: true, Profile: profile}
		return nil
	})
	return result, err
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
			desktop.ID, desktop.ProfileID, desktop.Revision = newProfileEntityID("desktop"), profile.ID, 0
			if _, err := tx.Exec(`
				INSERT INTO desktops (id, profile_id, name, shortcut_slot, order_key, tree_json, active_pane_id, revision, created_at, updated_at)
				VALUES (?, ?, '', ?, ?, '', '', 0, ?, ?)`,
				desktop.ID, profile.ID, slotValue(desktop.ShortcutSlot), desktop.OrderKey, now, now); err != nil {
				return err
			}
		}
		if err := writeDesktopArrangement(tx, now, desktop); err != nil {
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
