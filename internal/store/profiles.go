package store

import (
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/rankkey"
)

type ProfileDeletion struct {
	Deleted            profiles.Profile
	Destination        profiles.Profile
	MovedSessionIDs    []string
	MovedAutomationIDs []string
	MovedCrewIDs       []string
}

type DesktopDeletion struct {
	Profile           profiles.Profile
	Deleted           profiles.Desktop
	UnplacedSessionID []string
}

type LeafMove struct {
	Source      profiles.Desktop
	Target      profiles.Desktop
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
	Status           profiles.PaneStatus
	Focus            bool
}

type SessionProfileMove struct {
	SessionID     string
	FromProfileID string
	ToProfileID   string
	SourceDesktop *profiles.Desktop
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

func newProfileEntityID(prefix string) string {
	return prefix + "-" + uuid.NewString()
}

func (s *Store) profilesTx(fn func(tx *sql.Tx, now string) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return profiles.Errorf(profiles.CodeUnavailable, "profiles need the SQLite store")
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

const profileColumns = `id, name, current_desktop_id, last_used_at, revision, deleted_at`

func scanProfile(row rowScanner) (profiles.Profile, error) {
	var profile profiles.Profile
	err := row.Scan(&profile.ID, &profile.Name, &profile.CurrentDesktopID, &profile.LastUsedAt, &profile.Revision, &profile.DeletedAt)
	return profile, err
}

func loadProfile(q queryer, id string) (profiles.Profile, error) {
	profile, err := scanProfile(q.QueryRow(`SELECT `+profileColumns+` FROM profiles WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return profiles.Profile{}, profiles.Errorf(profiles.CodeNotFound, "profile %q does not exist", id)
	}
	return profile, err
}

func loadLiveProfile(q queryer, id string) (profiles.Profile, error) {
	profile, err := loadProfile(q, id)
	if err != nil {
		return profiles.Profile{}, err
	}
	if profile.Deleted() {
		return profiles.Profile{}, profiles.Errorf(profiles.CodeProfileDeleted, "profile %q (%s) was deleted at %s", profile.Name, profile.ID, profile.DeletedAt)
	}
	return profile, nil
}

func requireRevision(entity, id string, expected, current int64) error {
	if expected != current {
		return profiles.Stale(entity, id, expected, current)
	}
	return nil
}

func rowFound(row rowScanner, dest ...any) (bool, error) {
	err := row.Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func queryColumn[T any](q queryer, query string, args ...any) ([]T, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		var value T
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func ensureLiveProfileNameFree(tx *sql.Tx, name, exceptID string) error {
	var holder string
	taken, err := rowFound(tx.QueryRow(`SELECT id FROM profiles WHERE name = ? AND deleted_at = '' AND id != ?`, name, exceptID), &holder)
	if err != nil {
		return err
	}
	if taken {
		return profiles.Errorf(profiles.CodeNameTaken, "profile name %q is already used by %s", name, holder)
	}
	return nil
}

const desktopColumns = `id, profile_id, name, COALESCE(shortcut_slot, 0), order_key, tree_json, active_pane_id, revision`

func scanDesktopRow(row rowScanner) (profiles.Desktop, error) {
	var desktop profiles.Desktop
	var treeJSON string
	if err := row.Scan(&desktop.ID, &desktop.ProfileID, &desktop.Name, &desktop.ShortcutSlot, &desktop.OrderKey, &treeJSON, &desktop.ActivePaneID, &desktop.Revision); err != nil {
		return profiles.Desktop{}, err
	}
	tree, err := layouttree.DecodeLayout(treeJSON)
	if err != nil {
		return profiles.Desktop{}, profiles.Errorf(profiles.CodeInvalid, "desktop %s has a stored tree that does not decode: %v", desktop.ID, err)
	}
	desktop.Tree = tree
	return desktop, nil
}

type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func loadDesktopPanes(q queryer, desktopID string) ([]profiles.Pane, error) {
	rows, err := q.Query(`
		SELECT pane_id, desktop_id, kind, session_id, title, status, error
		FROM desktop_panes WHERE desktop_id = ? ORDER BY created_at, pane_id`, desktopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var panes []profiles.Pane
	for rows.Next() {
		var pane profiles.Pane
		if err := rows.Scan(&pane.PaneID, &pane.DesktopID, &pane.Kind, &pane.SessionID, &pane.Title, &pane.Status, &pane.Error); err != nil {
			return nil, err
		}
		panes = append(panes, pane)
	}
	return panes, rows.Err()
}

func loadDesktop(q queryer, id string) (profiles.Desktop, error) {
	desktop, err := scanDesktopRow(q.QueryRow(`SELECT `+desktopColumns+` FROM desktops WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return profiles.Desktop{}, profiles.Errorf(profiles.CodeNotFound, "desktop %q does not exist", id)
	}
	if err != nil {
		return profiles.Desktop{}, err
	}
	desktop.Panes, err = loadDesktopPanes(q, id)
	return desktop, err
}

func listDesktops(q queryer, profileID string) ([]profiles.Desktop, error) {
	rows, err := q.Query(`SELECT `+desktopColumns+` FROM desktops WHERE profile_id = ? ORDER BY order_key, id`, profileID)
	if err != nil {
		return nil, err
	}
	var desktops []profiles.Desktop
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

func insertDesktop(tx *sql.Tx, now, profileID, name string, slot int) (profiles.Desktop, error) {
	if err := profiles.ValidateShortcutSlot(slot); err != nil {
		return profiles.Desktop{}, err
	}
	var lastKey string
	if err := tx.QueryRow(`SELECT COALESCE(MAX(order_key), '') FROM desktops WHERE profile_id = ?`, profileID).Scan(&lastKey); err != nil {
		return profiles.Desktop{}, err
	}
	if err := ensureShortcutSlotFree(tx, profileID, slot, ""); err != nil {
		return profiles.Desktop{}, err
	}
	desktop := profiles.Desktop{
		ID:           newProfileEntityID("desktop"),
		ProfileID:    profileID,
		Name:         strings.TrimSpace(name),
		ShortcutSlot: slot,
		OrderKey:     rankkey.After(lastKey),
		Revision:     1,
	}
	_, err := tx.Exec(`
		INSERT INTO desktops (id, profile_id, name, shortcut_slot, order_key, tree_json, active_pane_id, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, '', '', 1, ?, ?)`,
		desktop.ID, profileID, desktop.Name, slotValue(slot), desktop.OrderKey, now, now)
	return desktop, err
}

func slotValue(slot int) any {
	if slot == 0 {
		return nil
	}
	return slot
}

func ensureShortcutSlotFree(tx *sql.Tx, profileID string, slot int, exceptDesktopID string) error {
	if slot == 0 {
		return nil
	}
	var holder string
	taken, err := rowFound(tx.QueryRow(`SELECT id FROM desktops WHERE profile_id = ? AND shortcut_slot = ? AND id != ?`, profileID, slot, exceptDesktopID), &holder)
	if err != nil {
		return err
	}
	if taken {
		return profiles.Errorf(profiles.CodeSlotTaken, "shortcut slot %d of profile %s is held by desktop %s", slot, profileID, holder)
	}
	return nil
}

func lowestFreeShortcutSlot(tx *sql.Tx, profileID string) (int, error) {
	slots, err := queryColumn[int](tx, `SELECT shortcut_slot FROM desktops WHERE profile_id = ? AND shortcut_slot IS NOT NULL`, profileID)
	if err != nil {
		return 0, err
	}
	taken := make(map[int]bool, len(slots))
	for _, slot := range slots {
		taken[slot] = true
	}
	for slot := profiles.FirstShortcutSlot; slot <= profiles.LastShortcutSlot; slot++ {
		if !taken[slot] {
			return slot, nil
		}
	}
	return 0, nil
}

func bumpProfile(tx *sql.Tx, profile *profiles.Profile) error {
	profile.Revision++
	_, err := tx.Exec(`UPDATE profiles SET name = ?, current_desktop_id = ?, revision = ? WHERE id = ?`,
		profile.Name, profile.CurrentDesktopID, profile.Revision, profile.ID)
	return err
}

func touchProfileUse(tx *sql.Tx, profile *profiles.Profile, now string) error {
	profile.LastUsedAt = now
	_, err := tx.Exec(`UPDATE profiles SET last_used_at = ? WHERE id = ?`, now, profile.ID)
	return err
}

func (s *Store) CreateProfile(name string) (profiles.Profile, profiles.Desktop, error) {
	var profile profiles.Profile
	var desktop profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		trimmed, err := profiles.ValidateName("profile", name)
		if err != nil {
			return err
		}
		if err := ensureLiveProfileNameFree(tx, trimmed, ""); err != nil {
			return err
		}
		profile = profiles.Profile{ID: newProfileEntityID("profile"), Name: trimmed, Revision: 1}
		if desktop, err = insertDesktop(tx, now, profile.ID, "", profiles.FirstShortcutSlot); err != nil {
			return err
		}
		profile.CurrentDesktopID = desktop.ID
		_, err = tx.Exec(`
			INSERT INTO profiles (id, name, current_desktop_id, last_used_at, revision, created_at, deleted_at)
			VALUES (?, ?, ?, '', 1, ?, '')`, profile.ID, profile.Name, profile.CurrentDesktopID, now)
		return err
	})
	return profile, desktop, err
}

func (s *Store) LiveProfile(id string) (profiles.Profile, error) {
	var profile profiles.Profile
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var err error
		profile, err = loadLiveProfile(tx, id)
		return err
	})
	return profile, err
}

func (s *Store) GetProfile(id string) (profiles.Profile, error) {
	var profile profiles.Profile
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var err error
		profile, err = loadProfile(tx, id)
		return err
	})
	return profile, err
}

func (s *Store) ListProfiles(includeDeleted bool) ([]profiles.Profile, error) {
	var out []profiles.Profile
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		query := `SELECT ` + profileColumns + ` FROM profiles`
		if !includeDeleted {
			query += ` WHERE deleted_at = ''`
		}
		rows, err := tx.Query(query + ` ORDER BY created_at, id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			profile, err := scanProfile(rows)
			if err != nil {
				return err
			}
			out = append(out, profile)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) MostRecentlyUsedProfile() (profiles.Profile, error) {
	var profile profiles.Profile
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var err error
		profile, err = scanProfile(tx.QueryRow(`
			SELECT ` + profileColumns + ` FROM profiles WHERE deleted_at = ''
			ORDER BY last_used_at DESC, created_at, id LIMIT 1`))
		if errors.Is(err, sql.ErrNoRows) {
			return profiles.Errorf(profiles.CodeNotFound, "no profile exists")
		}
		return err
	})
	return profile, err
}

func (s *Store) RenameProfile(id, name string, expectedRevision int64) (profiles.Profile, error) {
	var profile profiles.Profile
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		trimmed, err := profiles.ValidateName("profile", name)
		if err != nil {
			return err
		}
		if profile, err = loadLiveProfile(tx, id); err != nil {
			return err
		}
		if err := requireRevision("profile", id, expectedRevision, profile.Revision); err != nil {
			return err
		}
		if err := ensureLiveProfileNameFree(tx, trimmed, id); err != nil {
			return err
		}
		profile.Name = trimmed
		return bumpProfile(tx, &profile)
	})
	return profile, err
}

func (s *Store) SelectProfile(id string) (profiles.Profile, error) {
	var profile profiles.Profile
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		var err error
		if profile, err = loadLiveProfile(tx, id); err != nil {
			return err
		}
		return touchProfileUse(tx, &profile, now)
	})
	return profile, err
}

func (s *Store) ProfileArrangement(id string) (profiles.Profile, []profiles.Desktop, error) {
	var profile profiles.Profile
	var desktops []profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var err error
		if profile, err = loadProfile(tx, id); err != nil {
			return err
		}
		desktops, err = listDesktops(tx, id)
		return err
	})
	return profile, desktops, err
}

func ensureNotLastProfile(tx *sql.Tx, profile profiles.Profile) error {
	var live int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM profiles WHERE deleted_at = ''`).Scan(&live); err != nil {
		return err
	}
	if live <= 1 {
		return profiles.Errorf(profiles.CodeLastProfile, "profile %q (%s) is the last profile and cannot be deleted", profile.Name, profile.ID)
	}
	return nil
}

func loadDeletionDestination(tx *sql.Tx, profile profiles.Profile, destinationID string) (profiles.Profile, error) {
	if strings.TrimSpace(destinationID) == "" {
		return profiles.Profile{}, profiles.Errorf(profiles.CodeInvalid, "deleting profile %q needs a destination profile for its agents", profile.Name)
	}
	if destinationID == profile.ID {
		return profiles.Profile{}, profiles.Errorf(profiles.CodeDestinationSame, "profile %s cannot be its own destination", profile.ID)
	}
	return loadLiveProfile(tx, destinationID)
}

func deleteProfileDesktops(tx *sql.Tx, profileID string) error {
	if _, err := tx.Exec(`DELETE FROM desktop_panes WHERE desktop_id IN (SELECT id FROM desktops WHERE profile_id = ?)`, profileID); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM desktops WHERE profile_id = ?`, profileID)
	return err
}

func (s *Store) DeleteProfile(id string, expectedRevision int64, destinationID string) (ProfileDeletion, error) {
	var result ProfileDeletion
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		profile, err := loadLiveProfile(tx, id)
		if err != nil {
			return err
		}
		if err := requireRevision("profile", id, expectedRevision, profile.Revision); err != nil {
			return err
		}
		if err := ensureNotLastProfile(tx, profile); err != nil {
			return err
		}
		if err := ensureNoPendingMigration(tx, profile); err != nil {
			return err
		}
		destination, err := loadDeletionDestination(tx, profile, destinationID)
		if err != nil {
			return err
		}
		if result.MovedSessionIDs, err = queryColumn[string](tx, `SELECT id FROM sessions WHERE profile_id = ? AND closed_at = '' ORDER BY id`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE sessions SET profile_id = ? WHERE profile_id = ? AND closed_at = ''`, destinationID, id); err != nil {
			return err
		}
		if result.MovedAutomationIDs, err = moveProfileAutomations(tx, id, destinationID); err != nil {
			return err
		}
		if result.MovedCrewIDs, err = queryColumn[string](tx, `SELECT member_id FROM crew_profiles WHERE profile_id = ? ORDER BY member_id`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE crew_profiles SET profile_id = ? WHERE profile_id = ?`, destinationID, id); err != nil {
			return err
		}
		if err := deleteProfileDesktops(tx, id); err != nil {
			return err
		}
		profile.CurrentDesktopID = ""
		profile.DeletedAt = now
		profile.Revision++
		if _, err := tx.Exec(`UPDATE profiles SET current_desktop_id = '', deleted_at = ?, revision = ? WHERE id = ?`, now, profile.Revision, id); err != nil {
			return err
		}
		result.Deleted = profile
		result.Destination = destination
		return nil
	})
	return result, err
}

func (s *Store) CrewProfile(memberID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return "", profiles.Errorf(profiles.CodeUnavailable, "profiles need the SQLite store")
	}
	var profileID string
	err := s.db.QueryRow(`SELECT profile_id FROM crew_profiles WHERE member_id = ?`, memberID).Scan(&profileID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return profileID, err
}

func (s *Store) EnsureCrewProfile(memberID, profileID string) (string, error) {
	var assigned string
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		err := tx.QueryRow(`SELECT profile_id FROM crew_profiles WHERE member_id = ?`, memberID).Scan(&assigned)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := loadLiveProfile(tx, profileID); err != nil {
			return err
		}
		assigned = profileID
		_, err = tx.Exec(`INSERT INTO crew_profiles(member_id, profile_id) VALUES (?, ?)`, memberID, profileID)
		return err
	})
	return assigned, err
}

func moveProfileAutomations(tx *sql.Tx, from, to string) ([]string, error) {
	moved, err := queryColumn[string](tx, `SELECT id FROM automation_definitions WHERE profile_id = ? ORDER BY id`, from)
	if err != nil {
		return nil, err
	}
	for _, statement := range []string{
		`UPDATE automation_definitions SET profile_id = ? WHERE profile_id = ?`,
		`UPDATE automation_runs SET profile_id = ? WHERE profile_id = ? AND state = '` + AutomationRunStatePending + `'`,
		`UPDATE automation_continuity_bindings SET profile_id = ? WHERE profile_id = ? AND status = '` + AutomationBindingStatusActive + `'`,
	} {
		if _, err := tx.Exec(statement, to, from); err != nil {
			return nil, err
		}
	}
	return moved, nil
}

func (s *Store) CreateDesktop(profileID, name string, shortcutSlot int, takeFreeSlot bool) (profiles.Profile, profiles.Desktop, error) {
	var profile profiles.Profile
	var desktop profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		var err error
		if profile, err = loadLiveProfile(tx, profileID); err != nil {
			return err
		}
		if shortcutSlot == 0 && takeFreeSlot {
			if shortcutSlot, err = lowestFreeShortcutSlot(tx, profileID); err != nil {
				return err
			}
		}
		if desktop, err = insertDesktop(tx, now, profileID, name, shortcutSlot); err != nil {
			return err
		}
		return bumpProfile(tx, &profile)
	})
	return profile, desktop, err
}

func (s *Store) GetDesktop(id string) (profiles.Desktop, error) {
	var desktop profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var err error
		desktop, err = loadDesktop(tx, id)
		return err
	})
	return desktop, err
}

func saveDesktop(tx *sql.Tx, now string, desktop *profiles.Desktop) error {
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

func (s *Store) editDesktopRow(id string, expectedRevision int64, edit func(tx *sql.Tx, desktop *profiles.Desktop) error) (profiles.Desktop, error) {
	var desktop profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
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

func (s *Store) RenameDesktop(id, name string, expectedRevision int64) (profiles.Desktop, error) {
	return s.editDesktopRow(id, expectedRevision, func(_ *sql.Tx, desktop *profiles.Desktop) error {
		desktop.Name = strings.TrimSpace(name)
		return nil
	})
}

func (s *Store) SetDesktopShortcutSlot(id string, slot int, expectedRevision int64) (profiles.Desktop, error) {
	return s.editDesktopRow(id, expectedRevision, func(tx *sql.Tx, desktop *profiles.Desktop) error {
		if err := profiles.ValidateShortcutSlot(slot); err != nil {
			return err
		}
		if err := ensureShortcutSlotFree(tx, desktop.ProfileID, slot, id); err != nil {
			return err
		}
		desktop.ShortcutSlot = slot
		return nil
	})
}

func (s *Store) ReorderDesktop(id, previousID, nextID string, expectedRevision int64) (profiles.Desktop, error) {
	return s.editDesktopRow(id, expectedRevision, func(tx *sql.Tx, desktop *profiles.Desktop) error {
		neighbourKey := func(neighbourID string) (string, error) {
			if neighbourID == "" {
				return "", nil
			}
			if neighbourID == id {
				return "", profiles.Errorf(profiles.CodeInvalid, "desktop %s cannot be ordered against itself", id)
			}
			neighbour, err := loadDesktop(tx, neighbourID)
			if err != nil {
				return "", err
			}
			if neighbour.ProfileID != desktop.ProfileID {
				return "", profiles.Errorf(profiles.CodeCrossProfile, "desktop %s belongs to profile %s, not %s", neighbourID, neighbour.ProfileID, desktop.ProfileID)
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
			return profiles.Errorf(profiles.CodeInvalid, "desktop %s cannot be ordered between %q and %q: %v", id, previousID, nextID, err)
		}
		desktop.OrderKey = key
		return nil
	})
}

func repointCurrentDesktop(profile *profiles.Profile, siblings []profiles.Desktop, removedID string) {
	if profile.CurrentDesktopID != removedID {
		return
	}
	for i, sibling := range siblings {
		if sibling.ID != removedID {
			continue
		}
		if i+1 < len(siblings) {
			profile.CurrentDesktopID = siblings[i+1].ID
		} else {
			profile.CurrentDesktopID = siblings[i-1].ID
		}
	}
}

func paneSessionIDs(panes []profiles.Pane) []string {
	var ids []string
	for _, pane := range panes {
		ids = append(ids, pane.SessionID)
	}
	return ids
}

func deleteDesktop(tx *sql.Tx, id string) error {
	if _, err := tx.Exec(`DELETE FROM desktop_panes WHERE desktop_id = ?`, id); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM desktops WHERE id = ?`, id)
	return err
}

func (s *Store) DeleteDesktop(id string, expectedRevision int64) (DesktopDeletion, error) {
	var result DesktopDeletion
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		desktop, err := loadDesktop(tx, id)
		if err != nil {
			return err
		}
		if err := requireRevision("desktop", id, expectedRevision, desktop.Revision); err != nil {
			return err
		}
		profile, err := loadLiveProfile(tx, desktop.ProfileID)
		if err != nil {
			return err
		}
		siblings, err := listDesktops(tx, desktop.ProfileID)
		if err != nil {
			return err
		}
		if len(siblings) <= 1 {
			return profiles.Errorf(profiles.CodeLastDesktop, "desktop %s is the last desktop of profile %q and cannot be deleted", id, profile.Name)
		}
		repointCurrentDesktop(&profile, siblings, id)
		result.UnplacedSessionID = paneSessionIDs(desktop.Panes)
		if err := deleteDesktop(tx, id); err != nil {
			return err
		}
		if err := bumpProfile(tx, &profile); err != nil {
			return err
		}
		result.Profile = profile
		result.Deleted = desktop
		return nil
	})
	return result, err
}

func (s *Store) SetCurrentDesktop(profileID, desktopID string) (profiles.Profile, error) {
	var profile profiles.Profile
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		var err error
		if profile, err = loadLiveProfile(tx, profileID); err != nil {
			return err
		}
		desktop, err := loadDesktop(tx, desktopID)
		if err != nil {
			return err
		}
		if desktop.ProfileID != profileID {
			return profiles.Errorf(profiles.CodeCrossProfile, "desktop %s belongs to profile %s, not %s", desktopID, desktop.ProfileID, profileID)
		}
		profile.CurrentDesktopID = desktopID
		profile.LastUsedAt = now
		_, err = tx.Exec(`UPDATE profiles SET current_desktop_id = ?, last_used_at = ? WHERE id = ?`, desktopID, now, profileID)
		return err
	})
	return profile, err
}

func (s *Store) SetActivePane(desktopID, paneID string) (profiles.Profile, profiles.Desktop, error) {
	var profile profiles.Profile
	var desktop profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		var err error
		if desktop, err = loadDesktop(tx, desktopID); err != nil {
			return err
		}
		if !layouttree.HasPane(desktop.Tree, paneID) {
			return profiles.Errorf(profiles.CodeNotFound, "pane %q does not belong to desktop %s", paneID, desktopID)
		}
		if profile, err = loadLiveProfile(tx, desktop.ProfileID); err != nil {
			return err
		}
		desktop.ActivePaneID = paneID
		if _, err := tx.Exec(`UPDATE desktops SET active_pane_id = ?, updated_at = ? WHERE id = ?`, paneID, now, desktopID); err != nil {
			return err
		}
		return touchProfileUse(tx, &profile, now)
	})
	return profile, desktop, err
}

func (s *Store) FocusSession(sessionID string) (profiles.Profile, *profiles.Desktop, error) {
	var profile profiles.Profile
	var focused *profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		var profileID string
		err := tx.QueryRow(`SELECT profile_id FROM sessions WHERE id = ?`, sessionID).Scan(&profileID)
		if errors.Is(err, sql.ErrNoRows) {
			return profiles.Errorf(profiles.CodeNotFound, "session %q does not exist", sessionID)
		}
		if err != nil {
			return err
		}
		if profile, err = loadLiveProfile(tx, profileID); err != nil {
			return err
		}
		var desktopID, paneID string
		placed, err := rowFound(tx.QueryRow(`SELECT desktop_id, pane_id FROM desktop_panes WHERE session_id = ?`, sessionID), &desktopID, &paneID)
		if err != nil {
			return err
		}
		if placed {
			desktop, err := loadDesktop(tx, desktopID)
			if err != nil {
				return err
			}
			desktop.ActivePaneID = paneID
			if _, err := tx.Exec(`UPDATE desktops SET active_pane_id = ?, updated_at = ? WHERE id = ?`, paneID, now, desktopID); err != nil {
				return err
			}
			profile.CurrentDesktopID = desktopID
			if _, err := tx.Exec(`UPDATE profiles SET current_desktop_id = ? WHERE id = ?`, desktopID, profile.ID); err != nil {
				return err
			}
			focused = &desktop
		}
		return touchProfileUse(tx, &profile, now)
	})
	return profile, focused, err
}

func panePersisted(tx *sql.Tx, desktopID string, pane profiles.Pane) (bool, error) {
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM desktop_panes WHERE pane_id = ? AND session_id = ? AND desktop_id = ?`,
		pane.PaneID, pane.SessionID, desktopID).Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}

func checkPaneSession(tx *sql.Tx, desktop profiles.Desktop, pane profiles.Pane, persisted bool) (bool, error) {
	var profileID, closedAt string
	known, err := rowFound(tx.QueryRow(`SELECT profile_id, closed_at FROM sessions WHERE id = ?`, pane.SessionID), &profileID, &closedAt)
	if err != nil {
		return false, err
	}
	if !known {
		if persisted {
			return false, nil
		}
		return false, profiles.Errorf(profiles.CodeNotFound, "pane %s names session %s, which does not exist", pane.PaneID, pane.SessionID)
	}
	if closedAt != "" && !persisted {
		return false, profiles.Errorf(profiles.CodeSessionClosed, "pane %s names session %s, which closed at %s", pane.PaneID, pane.SessionID, closedAt)
	}
	if profileID != desktop.ProfileID {
		return false, profiles.Errorf(profiles.CodeCrossProfile, "session %s belongs to profile %q, desktop %s belongs to profile %q; a layout write cannot change membership", pane.SessionID, profileID, desktop.ID, desktop.ProfileID)
	}
	return true, nil
}

func checkPaneHolders(tx *sql.Tx, desktop profiles.Desktop, pane profiles.Pane) error {
	var holderDesktop, holderPane string
	held, err := rowFound(tx.QueryRow(`SELECT desktop_id, pane_id FROM desktop_panes WHERE session_id = ? AND pane_id != ?`, pane.SessionID, pane.PaneID), &holderDesktop, &holderPane)
	if err != nil {
		return err
	}
	if held && holderDesktop != desktop.ID {
		return profiles.Errorf(profiles.CodeAlreadyPlaced, "session %s is already placed in pane %s of desktop %s", pane.SessionID, holderPane, holderDesktop)
	}
	var paneHolder string
	used, err := rowFound(tx.QueryRow(`SELECT desktop_id FROM desktop_panes WHERE pane_id = ?`, pane.PaneID), &paneHolder)
	if err != nil {
		return err
	}
	if used && paneHolder != desktop.ID {
		return profiles.Errorf(profiles.CodeInvalid, "pane id %s is already used on desktop %s", pane.PaneID, paneHolder)
	}
	return nil
}

func checkPaneMembership(tx *sql.Tx, desktop profiles.Desktop) error {
	for _, pane := range desktop.Panes {
		persisted, err := panePersisted(tx, desktop.ID, pane)
		if err != nil {
			return err
		}
		sessionKnown, err := checkPaneSession(tx, desktop, pane, persisted)
		if err != nil {
			return err
		}
		if !sessionKnown {
			continue
		}
		if err := checkPaneHolders(tx, desktop, pane); err != nil {
			return err
		}
	}
	return nil
}

func paneCreationTimes(tx *sql.Tx, desktopID string) (map[string]string, error) {
	rows, err := tx.Query(`SELECT pane_id, created_at FROM desktop_panes WHERE desktop_id = ?`, desktopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	createdAt := make(map[string]string)
	for rows.Next() {
		var paneID, at string
		if err := rows.Scan(&paneID, &at); err != nil {
			return nil, err
		}
		createdAt[paneID] = at
	}
	return createdAt, rows.Err()
}

func insertDesktopPanes(tx *sql.Tx, now string, desktop profiles.Desktop, createdAt map[string]string) error {
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
	return nil
}

func settleForWrite(desktop profiles.Desktop) profiles.Desktop {
	desktop = profiles.Settle(desktop)
	for i := range desktop.Panes {
		desktop.Panes[i].DesktopID = desktop.ID
		if desktop.Panes[i].Status == "" {
			desktop.Panes[i].Status = profiles.PaneStatusReady
		}
	}
	return desktop
}

func writeDesktopArrangement(tx *sql.Tx, now string, desktop *profiles.Desktop) error {
	if err := layouttree.Validate(desktop.Tree); err != nil {
		return profiles.Errorf(profiles.CodeInvalid, "desktop %s: %v", desktop.ID, err)
	}
	*desktop = settleForWrite(*desktop)
	if err := profiles.CheckDesktop(*desktop); err != nil {
		return err
	}
	if err := checkPaneMembership(tx, *desktop); err != nil {
		return err
	}
	createdAt, err := paneCreationTimes(tx, desktop.ID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM desktop_panes WHERE desktop_id = ?`, desktop.ID); err != nil {
		return err
	}
	if err := insertDesktopPanes(tx, now, *desktop, createdAt); err != nil {
		return err
	}
	return saveDesktop(tx, now, desktop)
}

func (s *Store) UpdateDesktopArrangement(id string, expectedRevision int64, edit func(desktop profiles.Desktop) (profiles.Desktop, error)) (profiles.Desktop, error) {
	var desktop profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		current, err := loadDesktop(tx, id)
		if err != nil {
			return err
		}
		if err := requireRevision("desktop", id, expectedRevision, current.Revision); err != nil {
			return err
		}
		if _, err := loadLiveProfile(tx, current.ProfileID); err != nil {
			return err
		}
		edited, err := edit(current)
		if err != nil {
			return err
		}
		edited.ID, edited.ProfileID, edited.Revision = current.ID, current.ProfileID, current.Revision
		edited.Name, edited.ShortcutSlot, edited.OrderKey = current.Name, current.ShortcutSlot, current.OrderKey
		desktop = edited
		return writeDesktopArrangement(tx, now, &desktop)
	})
	return desktop, err
}

func placeSessionInTree(desktop profiles.Desktop, request SessionPlacementRequest, paneID string) (profiles.Desktop, error) {
	anchor := strings.TrimSpace(request.AnchorPaneID)
	if anchor != "" && !layouttree.HasPane(desktop.Tree, anchor) {
		return desktop, profiles.Errorf(profiles.CodeNotFound, "anchor pane %q does not belong to desktop %s", anchor, desktop.ID)
	}
	if anchor == "" {
		anchor = desktop.ActivePaneID
	}
	switch {
	case layouttree.LayoutEmpty(desktop.Tree):
		desktop.Tree = layouttree.DefaultLayout(paneID)
	case anchor == "":
		leaves := layouttree.TileIDs(desktop.Tree)
		next, ok := layouttree.MoveLeafBetweenLayouts(layouttree.DefaultLayout(paneID), desktop.Tree, paneID, "", newProfileEntityID("split"), request.Direction, false, firstChildRatio(request.NewPaneShare, false), "")
		if !ok {
			return desktop, profiles.Errorf(profiles.CodeInvalid, "desktop %s holds only tiles %v and the new pane could not dock beside them", desktop.ID, leaves)
		}
		desktop.Tree = next.TargetLayout
	default:
		splitID := newProfileEntityID("split")
		ratio := firstChildRatio(request.NewPaneShare, false)
		next, ok := layouttree.Split(desktop.Tree, anchor, paneID, splitID, request.Direction, ratio)
		if !ok {
			return desktop, profiles.Errorf(profiles.CodeNotFound, "anchor pane %q does not belong to desktop %s", anchor, desktop.ID)
		}
		if request.NewPaneShare > 0 && request.NewPaneShare < 1 {
			next, _ = layouttree.SetSplitRatio(next, splitID, ratio)
		}
		desktop.Tree = next
	}
	desktop.Panes = append(desktop.Panes, profiles.Pane{
		PaneID:    paneID,
		Kind:      profiles.PaneKindAgent,
		SessionID: strings.TrimSpace(request.SessionID),
		Title:     strings.TrimSpace(request.Title),
		Status:    request.Status,
	})
	if request.Focus || desktop.ActivePaneID == "" {
		desktop.ActivePaneID = paneID
	}
	return desktop, nil
}

func (s *Store) PlaceSession(request SessionPlacementRequest) (profiles.Desktop, string, error) {
	paneID := newProfileEntityID("pane")
	desktop, err := s.UpdateDesktopArrangement(request.DesktopID, request.ExpectedRevision, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		return placeSessionInTree(desktop, request, paneID)
	})
	return desktop, paneID, err
}

func loadLaunchDesktop(tx *sql.Tx, profile profiles.Profile, desktopID string) (profiles.Desktop, error) {
	if strings.TrimSpace(desktopID) == "" {
		desktopID = profile.CurrentDesktopID
	}
	desktop, err := loadDesktop(tx, desktopID)
	if err != nil {
		return profiles.Desktop{}, err
	}
	if desktop.ProfileID != profile.ID {
		return profiles.Desktop{}, profiles.Errorf(profiles.CodeCrossProfile, "desktop %s belongs to profile %s, not profile %s", desktop.ID, desktop.ProfileID, profile.ID)
	}
	return desktop, nil
}

func (s *Store) LaunchDesktop(profileID, desktopID string) (profiles.Desktop, error) {
	var desktop profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		profile, err := loadLiveProfile(tx, profileID)
		if err != nil {
			return err
		}
		desktop, err = loadLaunchDesktop(tx, profile, desktopID)
		return err
	})
	return desktop, err
}

func (s *Store) PlaceLaunchedSession(request SessionPlacementRequest) (profiles.Desktop, string, error) {
	paneID := newProfileEntityID("pane")
	var desktop profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		profileID, err := openSessionProfileID(tx, request.SessionID)
		if err != nil {
			return err
		}
		profile, err := loadLiveProfile(tx, profileID)
		if err != nil {
			return err
		}
		current, err := loadLaunchDesktop(tx, profile, request.DesktopID)
		if err != nil {
			return err
		}
		desktop, err = placeSessionInTree(current, request, paneID)
		if err != nil {
			return err
		}
		return writeDesktopArrangement(tx, now, &desktop)
	})
	return desktop, paneID, err
}

func withoutPane(panes []profiles.Pane, paneID string) []profiles.Pane {
	kept := make([]profiles.Pane, 0, len(panes))
	for _, pane := range panes {
		if pane.PaneID != paneID {
			kept = append(kept, pane)
		}
	}
	return kept
}

func (s *Store) RemoveLeaf(desktopID, leafID string, expectedRevision int64) (profiles.Desktop, error) {
	return s.UpdateDesktopArrangement(desktopID, expectedRevision, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		next, ok := layouttree.Remove(desktop.Tree, leafID)
		if !ok {
			return desktop, profiles.Errorf(profiles.CodeNotFound, "leaf %q does not belong to desktop %s", leafID, desktopID)
		}
		desktop.Tree = next
		desktop.Panes = withoutPane(desktop.Panes, leafID)
		return desktop, nil
	})
}

func (s *Store) SetDesktopSplitRatio(desktopID, splitID string, ratio float64, expectedRevision int64) (profiles.Desktop, error) {
	return s.UpdateDesktopArrangement(desktopID, expectedRevision, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		next, ok := layouttree.SetSplitRatio(desktop.Tree, splitID, ratio)
		if !ok {
			return desktop, profiles.Errorf(profiles.CodeNotFound, "split %q does not belong to desktop %s", splitID, desktopID)
		}
		desktop.Tree = next
		return desktop, nil
	})
}

func (s *Store) moveLeafWithinDesktop(request LeafMoveRequest) (LeafMove, error) {
	desktop, err := s.UpdateDesktopArrangement(request.SourceDesktopID, request.ExpectedSourceRevision, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		next, ok := layouttree.MoveLeaf(desktop.Tree, request.LeafID, request.AnchorID, newProfileEntityID("split"), request.Direction, request.Before, firstChildRatio(request.LeafShare, request.Before))
		if !ok {
			return desktop, profiles.Errorf(profiles.CodeInvalid, "leaf %q could not move beside %q on desktop %s", request.LeafID, request.AnchorID, desktop.ID)
		}
		desktop.Tree = next
		return desktop, nil
	})
	return LeafMove{Source: desktop, Target: desktop, FinalLeafID: request.LeafID}, err
}

func loadMoveEnds(tx *sql.Tx, request LeafMoveRequest) (profiles.Desktop, profiles.Desktop, error) {
	source, err := loadDesktop(tx, request.SourceDesktopID)
	if err != nil {
		return profiles.Desktop{}, profiles.Desktop{}, err
	}
	target, err := loadDesktop(tx, request.TargetDesktopID)
	if err != nil {
		return profiles.Desktop{}, profiles.Desktop{}, err
	}
	if err := requireRevision("desktop", source.ID, request.ExpectedSourceRevision, source.Revision); err != nil {
		return profiles.Desktop{}, profiles.Desktop{}, err
	}
	if err := requireRevision("desktop", target.ID, request.ExpectedTargetRevision, target.Revision); err != nil {
		return profiles.Desktop{}, profiles.Desktop{}, err
	}
	if source.ProfileID != target.ProfileID {
		return profiles.Desktop{}, profiles.Desktop{}, profiles.Errorf(profiles.CodeCrossProfile, "desktop %s belongs to profile %s and desktop %s to profile %s; a move between desktops cannot change membership", source.ID, source.ProfileID, target.ID, target.ProfileID)
	}
	if _, err := loadLiveProfile(tx, source.ProfileID); err != nil {
		return profiles.Desktop{}, profiles.Desktop{}, err
	}
	return source, target, nil
}

func handOverPane(source, target *profiles.Desktop, leafID, finalLeafID string) {
	for _, pane := range source.Panes {
		if pane.PaneID == leafID {
			pane.PaneID = finalLeafID
			target.Panes = append(target.Panes, pane)
			target.ActivePaneID = finalLeafID
		}
	}
	source.Panes = withoutPane(source.Panes, leafID)
}

func (s *Store) MoveLeaf(request LeafMoveRequest) (LeafMove, error) {
	if request.SourceDesktopID == request.TargetDesktopID {
		return s.moveLeafWithinDesktop(request)
	}
	var result LeafMove
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		source, target, err := loadMoveEnds(tx, request)
		if err != nil {
			return err
		}
		moved, ok := layouttree.MoveLeafBetweenLayouts(source.Tree, target.Tree, request.LeafID, request.AnchorID, newProfileEntityID("split"), request.Direction, request.Before, firstChildRatio(request.LeafShare, request.Before), uuid.NewString())
		if !ok {
			return profiles.Errorf(profiles.CodeInvalid, "leaf %q could not move from desktop %s beside %q on desktop %s", request.LeafID, source.ID, request.AnchorID, target.ID)
		}
		source.Tree, target.Tree = moved.SourceLayout, moved.TargetLayout
		handOverPane(&source, &target, request.LeafID, moved.FinalLeafID)
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

func openSessionProfileID(tx *sql.Tx, sessionID string) (string, error) {
	var profileID, closedAt string
	known, err := rowFound(tx.QueryRow(`SELECT profile_id, closed_at FROM sessions WHERE id = ?`, sessionID), &profileID, &closedAt)
	if err != nil {
		return "", err
	}
	if !known {
		return "", profiles.Errorf(profiles.CodeNotFound, "session %q does not exist", sessionID)
	}
	if closedAt != "" {
		return "", profiles.Errorf(profiles.CodeSessionClosed, "session %s closed at %s; closed sessions keep their profile as history", sessionID, closedAt)
	}
	return profileID, nil
}

func (s *Store) AssignSessionProfile(sessionID, profileID string) error {
	return s.profilesTx(func(tx *sql.Tx, _ string) error {
		if _, err := loadLiveProfile(tx, profileID); err != nil {
			return err
		}
		current, err := openSessionProfileID(tx, sessionID)
		if err != nil {
			return err
		}
		if current != "" && current != profileID {
			return profiles.Errorf(profiles.CodeCrossProfile, "session %s already belongs to profile %s; membership changes only through a move", sessionID, current)
		}
		_, err = tx.Exec(`UPDATE sessions SET profile_id = ? WHERE id = ? AND closed_at = ''`, profileID, sessionID)
		return err
	})
}

func (s *Store) SessionProfileID(sessionID string) (string, error) {
	var profileID string
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		err := tx.QueryRow(`SELECT profile_id FROM sessions WHERE id = ?`, sessionID).Scan(&profileID)
		if errors.Is(err, sql.ErrNoRows) {
			return profiles.Errorf(profiles.CodeNotFound, "session %q does not exist", sessionID)
		}
		return err
	})
	return profileID, err
}

func (s *Store) SessionPlacement(sessionID string) (profiles.Placement, bool, error) {
	var placement profiles.Placement
	found := false
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var err error
		found, err = rowFound(tx.QueryRow(`
			SELECT d.profile_id, p.desktop_id, p.pane_id
			FROM desktop_panes p JOIN desktops d ON d.id = p.desktop_id
			WHERE p.session_id = ?`, sessionID), &placement.ProfileID, &placement.DesktopID, &placement.PaneID)
		return err
	})
	return placement, found, err
}

func removeSessionPlacement(tx *sql.Tx, now, sessionID string) (*profiles.Desktop, error) {
	var desktopID, paneID string
	placed, err := rowFound(tx.QueryRow(`SELECT desktop_id, pane_id FROM desktop_panes WHERE session_id = ?`, sessionID), &desktopID, &paneID)
	if err != nil {
		return nil, err
	}
	if !placed {
		return nil, nil
	}
	desktop, err := loadDesktop(tx, desktopID)
	if err != nil {
		return nil, err
	}
	next, ok := layouttree.Remove(desktop.Tree, paneID)
	if !ok {
		return nil, profiles.Errorf(profiles.CodeInvalid, "pane %s has a row on desktop %s but no leaf in its tree", paneID, desktopID)
	}
	desktop.Tree = next
	desktop.Panes = withoutPane(desktop.Panes, paneID)
	if err := writeDesktopArrangement(tx, now, &desktop); err != nil {
		return nil, err
	}
	return &desktop, nil
}

func (s *Store) unplaceSessionLocked(now, sessionID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := removeSessionPlacement(tx, now, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) unplaceSessionsLocked(reason, where string, args ...any) {
	sessionIDs, err := queryColumn[string](s.db, `SELECT session_id FROM desktop_panes WHERE `+where, args...)
	if err != nil {
		log.Printf("[store] %s: listing placed sessions: %v", reason, err)
		return
	}
	now := time.Now().UTC().Format(sortableTimeFormat)
	for _, id := range sessionIDs {
		if err := s.unplaceSessionLocked(now, id); err != nil {
			log.Printf("[store] %s: removing the pane of session %s: %v", reason, id, err)
		}
	}
}

func (s *Store) RemoveSessionPlacement(sessionID string) (*profiles.Desktop, error) {
	var desktop *profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		var err error
		desktop, err = removeSessionPlacement(tx, now, sessionID)
		return err
	})
	return desktop, err
}

func (s *Store) MoveSessionToProfile(sessionID, destinationProfileID string) (SessionProfileMove, error) {
	move := SessionProfileMove{SessionID: sessionID, ToProfileID: destinationProfileID}
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		if _, err := loadLiveProfile(tx, destinationProfileID); err != nil {
			return err
		}
		from, err := openSessionProfileID(tx, sessionID)
		if err != nil {
			return err
		}
		move.FromProfileID = from
		if move.FromProfileID == destinationProfileID {
			return profiles.Errorf(profiles.CodeDestinationSame, "session %s already belongs to profile %s", sessionID, destinationProfileID)
		}
		if move.SourceDesktop, err = removeSessionPlacement(tx, now, sessionID); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE sessions SET profile_id = ? WHERE id = ? AND closed_at = ''`, destinationProfileID, sessionID)
		return err
	})
	return move, err
}
