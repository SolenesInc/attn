package store

import (
	"database/sql"
	"errors"

	"github.com/victorarias/attn/internal/rankkey"
	"github.com/victorarias/attn/internal/setupmigration"
	"github.com/victorarias/attn/internal/setups"
)

type SetupMigrationView struct {
	State    setups.MigrationState
	Manifest setupmigration.Manifest
	Plan     setupmigration.Plan
	Live     []setupmigration.GroupState
}

func (v SetupMigrationView) PlacementRequired() bool {
	return v.State.Phase == setupmigration.PhasePlacementRequired
}

type SetupMigrationFinish struct {
	View     SetupMigrationView
	Finished bool
	Setup    setups.Setup
	Desktops []setups.Desktop
	Deleted  []string
}

func loadSetupMigration(tx *sql.Tx) (SetupMigrationView, error) {
	var view SetupMigrationView
	state := &view.State
	found, err := rowFound(tx.QueryRow(`SELECT schema_version, phase, revision, imported_groups, draft FROM setup_migration WHERE id = 1`),
		&state.SchemaVersion, &state.Phase, &state.Revision, &state.ImportedGroups, &state.Draft)
	if err != nil {
		return view, err
	}
	if !found {
		return view, setups.Errorf(setups.CodeNotFound, "no workspace conversion is recorded in this database")
	}
	if view.Manifest, err = setupmigration.DecodeManifest(state.ImportedGroups); err != nil {
		return view, err
	}
	if !view.PlacementRequired() {
		return view, nil
	}
	if view.Plan, err = setupmigration.DecodePlan(state.Draft); err != nil {
		return view, err
	}
	desktops, err := listDesktops(tx, view.Manifest.SetupID)
	if err != nil {
		return view, err
	}
	view.Live = setupmigration.LiveGroups(view.Manifest, desktops)
	view.Plan = view.Plan.Reconcile(desktops).Retire(view.Live)
	return view, nil
}

func (s *Store) SetupMigration() (SetupMigrationView, error) {
	var view SetupMigrationView
	err := s.setupsTx(func(tx *sql.Tx, _ string) error {
		var err error
		view, err = loadSetupMigration(tx)
		return err
	})
	return view, err
}

func requirePlacement(view SetupMigrationView, expectedRevision int64) error {
	if !view.PlacementRequired() {
		return setups.Errorf(setups.CodeInvalid, "the workspace migration is already %s", view.State.Phase)
	}
	return requireRevision("migration", "draft", expectedRevision, view.State.Revision)
}

func saveMigrationRow(tx *sql.Tx, view *SetupMigrationView) error {
	draft, err := setupmigration.EncodePlan(view.Plan)
	if err != nil {
		return err
	}
	view.State.Draft = draft
	view.State.Revision++
	_, err = tx.Exec(`UPDATE setup_migration SET phase = ?, revision = ?, draft = ? WHERE id = 1`, view.State.Phase, view.State.Revision, view.State.Draft)
	return err
}

func (s *Store) EditSetupMigration(expectedRevision int64, edit func(plan setupmigration.Plan, live []setupmigration.GroupState) (setupmigration.Plan, error)) (SetupMigrationView, error) {
	var view SetupMigrationView
	err := s.setupsTx(func(tx *sql.Tx, _ string) error {
		var err error
		if view, err = loadSetupMigration(tx); err != nil {
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

func (s *Store) FinishSetupMigration(expectedRevision int64) (SetupMigrationFinish, error) {
	var result SetupMigrationFinish
	err := s.setupsTx(func(tx *sql.Tx, now string) error {
		view, err := loadSetupMigration(tx)
		if err != nil {
			return err
		}
		result.View = view
		if view.State.Phase == setupmigration.PhaseComplete {
			return nil
		}
		if err := requirePlacement(view, expectedRevision); err != nil {
			return err
		}
		setup, err := loadLiveSetup(tx, view.Manifest.SetupID)
		if err != nil {
			return err
		}
		current, err := listDesktops(tx, setup.ID)
		if err != nil {
			return err
		}
		outcome, err := setupmigration.Materialize(view.Plan, view.Live, current, func() string { return newSetupEntityID("split") })
		if err != nil {
			return err
		}
		if err := writeMigrationOutcome(tx, now, &setup, current, &outcome); err != nil {
			return err
		}
		view.State.Phase = setupmigration.PhaseComplete
		if err := saveMigrationRow(tx, &view); err != nil {
			return err
		}
		result = SetupMigrationFinish{View: view, Finished: true, Setup: setup, Desktops: outcome.Desktops, Deleted: outcome.Deleted}
		return nil
	})
	return result, err
}

func writeMigrationOutcome(tx *sql.Tx, now string, setup *setups.Setup, current []setups.Desktop, outcome *setupmigration.Outcome) error {
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
			desktop.ID, desktop.SetupID, desktop.Revision = newSetupEntityID("desktop"), setup.ID, 0
			if _, err := tx.Exec(`
				INSERT INTO desktops (id, setup_id, name, shortcut_slot, order_key, tree_json, active_pane_id, revision, created_at, updated_at)
				VALUES (?, ?, '', ?, ?, '', '', 0, ?, ?)`,
				desktop.ID, setup.ID, slotValue(desktop.ShortcutSlot), desktop.OrderKey, now, now); err != nil {
				return err
			}
		}
		if err := writeDesktopArrangement(tx, now, desktop); err != nil {
			return err
		}
	}
	if len(outcome.Desktops) == 0 {
		return errors.New("finishing the migration would leave the Default setup without a desktop")
	}
	if !finalDesktopExists(outcome.Desktops, setup.CurrentDesktopID) {
		setup.CurrentDesktopID = outcome.Desktops[0].ID
	}
	return bumpSetup(tx, setup)
}

func finalDesktopExists(desktops []setups.Desktop, id string) bool {
	for _, desktop := range desktops {
		if desktop.ID == id {
			return true
		}
	}
	return false
}

func ensureNoPendingMigration(tx *sql.Tx, setup setups.Setup) error {
	var phase, importedGroups string
	found, err := rowFound(tx.QueryRow(`SELECT phase, imported_groups FROM setup_migration WHERE id = 1`), &phase, &importedGroups)
	if err != nil || !found || phase != setupmigration.PhasePlacementRequired {
		return err
	}
	manifest, err := setupmigration.DecodeManifest(importedGroups)
	if err != nil {
		return err
	}
	if manifest.SetupID == setup.ID {
		return setups.Errorf(setups.CodeInvalid, "setup %q holds the workspace migration that is still waiting for placement; finish the migration before deleting it", setup.Name)
	}
	return nil
}
