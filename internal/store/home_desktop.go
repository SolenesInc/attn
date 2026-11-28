package store

import (
	"database/sql"
	"errors"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/rankkey"
)

// ListHomeDesktop says whether the profiles' lists of desktops hold their Home desktop, as views_enabled does.
func (s *Store) ListHomeDesktop(listed bool) {
	s.homeListed.Store(listed)
}

func (s *Store) listedDesktops(desktops []profiles.Desktop) []profiles.Desktop {
	if s.homeListed.Load() {
		return desktops
	}
	return ordinaryDesktops(desktops)
}

func ordinaryDesktops(desktops []profiles.Desktop) []profiles.Desktop {
	var ordinary []profiles.Desktop
	for _, desktop := range desktops {
		if desktop.Kind != profiles.DesktopKindHome {
			ordinary = append(ordinary, desktop)
		}
	}
	return ordinary
}

func profileDesktops(q queryer, profileID string) ([]profiles.Desktop, error) {
	desktops, err := listDesktops(q, profileID)
	if err != nil {
		return nil, err
	}
	stored, err := desktopKindsStored(q)
	if err != nil {
		return nil, err
	}
	var home string
	if stored {
		if _, err := rowFound(q.QueryRow(`SELECT id FROM desktops WHERE profile_id = ? AND kind = 'home'`, profileID), &home); err != nil {
			return nil, err
		}
	}
	for i := range desktops {
		desktops[i].Kind = profiles.DesktopKindDesktop
		if desktops[i].ID == home {
			desktops[i].Kind = profiles.DesktopKindHome
		}
	}
	return desktops, nil
}

func loadDesktopKind(q queryer, desktop *profiles.Desktop) error {
	desktop.Kind = profiles.DesktopKindDesktop
	stored, err := desktopKindsStored(q)
	if err != nil || !stored {
		return err
	}
	return q.QueryRow(`SELECT kind FROM desktops WHERE id = ?`, desktop.ID).Scan(&desktop.Kind)
}

// desktopKindsStored is false on a database before the migration that gave desktops their kind.
func desktopKindsStored(q queryer) (bool, error) {
	var stored bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('desktops') WHERE name = 'kind')`).Scan(&stored)
	return stored, err
}

func insertHomeDesktop(tx *sql.Tx, now, profileID string) error {
	if stored, err := desktopKindsStored(tx); err != nil || !stored {
		return err
	}
	var first string
	if err := tx.QueryRow(`SELECT COALESCE(MIN(order_key), '') FROM desktops WHERE profile_id = ?`, profileID).Scan(&first); err != nil {
		return err
	}
	orderKey, err := rankkey.Between("", first)
	if err != nil {
		return err
	}
	if first != "" && orderKey >= first {
		return errors.New("no order key sorts before " + first)
	}
	tree, err := layouttree.EncodeLayout(profiles.HomeTree())
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO desktops (id, profile_id, name, kind, order_key, tree_json, active_pane_id, revision, created_at, updated_at)
		VALUES (?, ?, '', 'home', ?, ?, '', 1, ?, ?)`,
		profiles.HomeDesktopID(profileID), profileID, orderKey, tree, now, now)
	return err
}

// HomeDesktop is the profile's Home desktop, listed or not.
func (s *Store) HomeDesktop(profileID string) (profiles.Desktop, error) {
	var desktop profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var id string
		found, err := rowFound(tx.QueryRow(`SELECT id FROM desktops WHERE profile_id = ? AND kind = 'home'`, profileID), &id)
		if err != nil {
			return err
		}
		if !found {
			return profiles.Errorf(profiles.CodeNotFound, "profile %s has no Home desktop", profileID)
		}
		desktop, err = loadDesktop(tx, id)
		return err
	})
	return desktop, err
}

func refuseHomeDesktop(desktop profiles.Desktop, refusal string) error {
	if desktop.Kind == profiles.DesktopKindHome {
		return profiles.Errorf(profiles.CodeInvalid, "%s", refusal)
	}
	return nil
}

// ResetHomeDesktop puts the profile's Home back at the top of its desktops, its views where HomeTree places them and its other tiles after them.
func (s *Store) ResetHomeDesktop(profileID string) ([]profiles.Desktop, error) {
	home, err := s.HomeDesktop(profileID)
	if err != nil {
		return nil, err
	}
	if !s.homeListed.Load() {
		return nil, refuseHomeDesktop(home, "Home is a desktop only while the experimental setting views_enabled is on")
	}
	if _, err := s.EditDesktopArrangement(home.ID, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		desktop.Tree = profiles.ResetHomeTree(desktop.Tree, newSplitID)
		return desktop, nil
	}); err != nil {
		return nil, err
	}
	_, desktops, err := s.ProfileArrangement(profileID)
	if err != nil {
		return nil, err
	}
	order := []string{home.ID}
	for _, desktop := range desktops {
		if desktop.ID != home.ID {
			order = append(order, desktop.ID)
		}
	}
	return s.SetDesktopOrder(profileID, order)
}
