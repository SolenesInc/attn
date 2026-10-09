package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/rankkey"
)

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
	Activate               bool
}

type SessionPlacementRequest struct {
	DesktopID        string
	ExpectedRevision int64
	SessionID        protocol.SessionID
	// RuntimeID selects a terminal; absent, reuse the session binding or allocate one.
	RuntimeID    protocol.TerminalID
	AnchorPaneID string
	Direction    layouttree.Direction
	NewPaneShare float64
	Title        string
	Status       profiles.PaneStatus
	Focus        bool
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
	_, err := s.profilesTxSeq(fn)
	return err
}

// Every profiles transaction takes the next seq: a read with a higher one never shows less.
func (s *Store) profilesTxSeq(fn func(tx *sql.Tx, now string) error) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return 0, profiles.Errorf(profiles.CodeUnavailable, "profiles need the SQLite store")
	}
	s.profilesSeq++
	seq := s.profilesSeq
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	at := time.Now().UTC()
	now := at.Format(sortableTimeFormat)
	if err := fn(tx, now); err != nil {
		return 0, err
	}
	emptied, err := stampEmptyDesktops(tx, now)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.announceTerminalBindingsLocked()
	if emptied {
		s.announceEmptyDesktop(at)
	}
	return seq, nil
}

const profileColumns = `id, name, current_desktop_id, last_used_at, revision, deleted_at, chief_session_id`

func scanProfile(row rowScanner) (profiles.Profile, error) {
	var profile profiles.Profile
	err := row.Scan(&profile.ID, &profile.Name, &profile.CurrentDesktopID, &profile.LastUsedAt, &profile.Revision, &profile.DeletedAt, &profile.ChiefSessionID)
	return profile, err
}

func loadProfile(q rowQuerier, id string) (profiles.Profile, error) {
	profile, err := scanProfile(q.QueryRow(`SELECT `+profileColumns+` FROM profiles WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return profiles.Profile{}, profiles.Errorf(profiles.CodeNotFound, "profile %q does not exist", id)
	}
	return profile, err
}

func loadLiveProfile(q rowQuerier, id string) (profiles.Profile, error) {
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
	var (
		holder     string
		holderName string
	)
	taken, err := rowFound(tx.QueryRow(`SELECT id, name FROM profiles WHERE name = ? COLLATE NOCASE AND deleted_at = '' AND id != ?`, name, exceptID), &holder, &holderName)
	if err != nil {
		return err
	}
	if taken {
		return profiles.Errorf(profiles.CodeNameTaken, "profile name %q is already used by profile %q (%s); names ignore case", name, holderName, holder)
	}
	return nil
}

const desktopColumns = `id, profile_id, name, order_key, tree_json, active_pane_id, focus_history, revision`

func scanDesktopRow(row rowScanner) (profiles.Desktop, error) {
	var desktop profiles.Desktop
	var treeJSON, focusJSON string
	if err := row.Scan(&desktop.ID, &desktop.ProfileID, &desktop.Name, &desktop.OrderKey, &treeJSON, &desktop.ActivePaneID, &focusJSON, &desktop.Revision); err != nil {
		return profiles.Desktop{}, err
	}
	desktop.ShortcutSlot = profiles.DesktopSlot(desktop.ID)
	tree, err := layouttree.DecodeLayout(treeJSON)
	if err != nil {
		return profiles.Desktop{}, profiles.Errorf(profiles.CodeInvalid, "desktop %s has a stored tree that does not decode: %v", desktop.ID, err)
	}
	desktop.Tree = tree
	if err := json.Unmarshal([]byte(focusJSON), &desktop.FocusHistory); err != nil {
		return profiles.Desktop{}, profiles.Errorf(profiles.CodeInvalid, "desktop %s has a stored focus history that does not decode: %v", desktop.ID, err)
	}
	return desktop, nil
}

type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func loadDesktopPanes(q queryer, desktopID string) ([]profiles.Pane, error) {
	rows, err := q.Query(`
		SELECT pane_id, desktop_id, kind, session_id, runtime_id, title, status, error
		FROM desktop_panes WHERE desktop_id = ? ORDER BY created_at, pane_id`, desktopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var panes []profiles.Pane
	for rows.Next() {
		var pane profiles.Pane
		if err := rows.Scan(&pane.PaneID, &pane.DesktopID, &pane.Kind, &pane.SessionID, &pane.RuntimeID, &pane.Title, &pane.Status, &pane.Error); err != nil {
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
	if err := ensureShortcutSlotFree(tx, profileID, slot); err != nil {
		return profiles.Desktop{}, err
	}
	desktop := profiles.Desktop{
		ID:           newDesktopID(profileID, slot),
		ProfileID:    profileID,
		Name:         strings.TrimSpace(name),
		ShortcutSlot: slot,
		OrderKey:     rankkey.After(lastKey),
		Revision:     1,
	}
	_, err := tx.Exec(`
		INSERT INTO desktops (id, profile_id, name, order_key, tree_json, active_pane_id, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', '', 1, ?, ?)`,
		desktop.ID, profileID, desktop.Name, desktop.OrderKey, now, now)
	return desktop, err
}

func newDesktopID(profileID string, slot int) string {
	if slot == 0 {
		return newProfileEntityID("desktop")
	}
	return profiles.NumberedDesktopID(profileID, slot)
}

func ensureShortcutSlotFree(tx *sql.Tx, profileID string, slot int) error {
	if slot == 0 {
		return nil
	}
	holder := profiles.NumberedDesktopID(profileID, slot)
	taken, err := rowFound(tx.QueryRow(`SELECT 1 FROM desktops WHERE id = ?`, holder), new(int))
	if err != nil {
		return err
	}
	if taken {
		return profiles.Errorf(profiles.CodeSlotTaken, "shortcut slot %d of profile %s is held by desktop %s", slot, profileID, holder)
	}
	return nil
}

func lowestFreeShortcutSlot(tx *sql.Tx, profileID string) (int, error) {
	ids, err := queryColumn[string](tx, `SELECT id FROM desktops WHERE profile_id = ?`, profileID)
	if err != nil {
		return 0, err
	}
	taken := make(map[int]bool, len(ids))
	for _, id := range ids {
		taken[profiles.DesktopSlot(id)] = true
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
	profile, desktops, _, err := s.ProfileArrangementSeq(id)
	return profile, desktops, err
}

func (s *Store) ProfileArrangementSeq(id string) (profiles.Profile, []profiles.Desktop, int64, error) {
	var profile profiles.Profile
	var desktops []profiles.Desktop
	seq, err := s.profilesTxSeq(func(tx *sql.Tx, _ string) error {
		var err error
		if profile, err = loadProfile(tx, id); err != nil {
			return err
		}
		desktops, err = listDesktops(tx, id)
		return err
	})
	return profile, desktops, seq, err
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

func deleteProfileDesktops(tx *sql.Tx, profileID string) error {
	if _, err := tx.Exec(`DELETE FROM desktop_panes WHERE desktop_id IN (SELECT id FROM desktops WHERE profile_id = ?)`, profileID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM launch_desktops WHERE desktop_id IN (SELECT id FROM desktops WHERE profile_id = ?)`, profileID); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM desktops WHERE profile_id = ?`, profileID)
	return err
}

func (s *Store) DeleteProfile(id string, expectedRevision int64, remoteLiveSessions, members int) (profiles.Profile, error) {
	var profile profiles.Profile
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		var err error
		if profile, err = loadLiveProfile(tx, id); err != nil {
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
		var sessions, automations, tiles int
		if err := tx.QueryRow(`SELECT count(*) FROM sessions WHERE profile_id = ? AND closed_at = ''`, id).Scan(&sessions); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT count(*) FROM automation_definitions WHERE profile_id = ? AND deleted_at = ''`, id).Scan(&automations); err != nil {
			return err
		}
		desktops, err := listDesktops(tx, id)
		if err != nil {
			return err
		}
		for _, desktop := range desktops {
			tiles += len(layouttree.TileIDs(desktop.Tree))
		}

		seeds, err := countOpenProfileSeeds(tx, id)
		if err != nil {
			return err
		}
		pending, err := countPendingProfileDelegations(tx, id)
		if err != nil {
			return err
		}
		reviews, err := countRunningProfileReviews(tx, id)
		if err != nil {
			return err
		}
		sessions += remoteLiveSessions
		if sessions != 0 || members != 0 || automations != 0 || tiles != 0 || seeds != 0 || pending != 0 || reviews != 0 {
			return fmt.Errorf("profile %q is not empty: %d live agents, %d crew members, %d automations, %d tiles, %d open seeds and %d pending delegations and %d running Garden reviews; clean up the profile yourself or ask your agent before deleting it", profile.Name, sessions, members, automations, tiles, seeds, pending, reviews)
		}
		if err := deleteProfileDesktops(tx, id); err != nil {
			return err
		}
		profile.CurrentDesktopID = ""
		profile.ChiefSessionID = ""
		profile.DeletedAt = now
		profile.Revision++
		_, err = tx.Exec(`UPDATE profiles SET current_desktop_id = '', chief_session_id = '', deleted_at = ?, revision = ? WHERE id = ?`, now, profile.Revision, id)
		return err
	})
	return profile, err
}

func (s *Store) SetProfileChief(sessionID protocol.SessionID) (profiles.Profile, protocol.SessionID, error) {
	var profile profiles.Profile
	var previous protocol.SessionID
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var profileID string
		err := tx.QueryRow(`SELECT profile_id FROM sessions WHERE id = ? AND closed_at = ''`, sessionID).Scan(&profileID)
		if errors.Is(err, sql.ErrNoRows) {
			return profiles.Errorf(profiles.CodeNotFound, "session %s is not a live agent", sessionID)
		}
		if err != nil {
			return err
		}
		if profile, err = loadLiveProfile(tx, profileID); err != nil {
			return err
		}
		previous = profile.ChiefSessionID
		profile.ChiefSessionID = sessionID
		_, err = tx.Exec(`UPDATE profiles SET chief_session_id = ? WHERE id = ?`, sessionID, profile.ID)
		return err
	})
	return profile, previous, err
}

func (s *Store) ClaimProfileChief(profileID string, sessionID protocol.SessionID) (bool, error) {
	claimed := false
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		result, err := tx.Exec(`UPDATE profiles SET chief_session_id = ? WHERE id = ? AND chief_session_id = '' AND deleted_at = ''`, sessionID, profileID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		claimed = affected == 1
		return err
	})
	return claimed, err
}

func (s *Store) ClearProfileChief(sessionID protocol.SessionID) (string, error) {
	var profileID string
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		found, err := rowFound(tx.QueryRow(`SELECT id FROM profiles WHERE chief_session_id = ? AND deleted_at = ''`, sessionID), &profileID)
		if err != nil || !found {
			return err
		}
		_, err = tx.Exec(`UPDATE profiles SET chief_session_id = '' WHERE id = ?`, profileID)
		return err
	})
	return profileID, err
}

func (s *Store) ProfileChiefs() (map[string]protocol.SessionID, error) {
	chiefs := map[string]protocol.SessionID{}
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		rows, err := tx.Query(`SELECT id, chief_session_id FROM profiles WHERE deleted_at = '' AND chief_session_id != ''`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				profileID string
				sessionID protocol.SessionID
			)
			if err := rows.Scan(&profileID, &sessionID); err != nil {
				return err
			}
			chiefs[profileID] = sessionID
		}
		return rows.Err()
	})
	return chiefs, err
}

func (s *Store) OldestProfile() (profiles.Profile, error) {
	var profile profiles.Profile
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var err error
		profile, err = scanProfile(tx.QueryRow(`SELECT ` + profileColumns + ` FROM profiles WHERE deleted_at = '' ORDER BY created_at, id LIMIT 1`))
		if errors.Is(err, sql.ErrNoRows) {
			return profiles.Errorf(profiles.CodeNotFound, "no live profile exists")
		}
		return err
	})
	return profile, err
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
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		err := tx.QueryRow(`SELECT profile_id FROM crew_profiles WHERE member_id = ?`, memberID).Scan(&assigned)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := loadLiveProfile(tx, profileID); err != nil {
			return err
		}
		assigned = profileID
		if _, err = tx.Exec(`INSERT INTO crew_profiles(member_id, profile_id) VALUES (?, ?)`, memberID, profileID); err != nil {
			return err
		}
		return startOnOwnDesktop(tx, now, "crew", memberID)
	})
	return assigned, err
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
		profile.CurrentDesktopID = desktop.ID
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
	focusJSON, err := json.Marshal(desktop.FocusHistory)
	if err != nil {
		return err
	}
	desktop.Revision++
	_, err = tx.Exec(`
		UPDATE desktops SET name = ?, order_key = ?, tree_json = ?, active_pane_id = ?, focus_history = ?, revision = ?, updated_at = ?
		WHERE id = ?`,
		desktop.Name, desktop.OrderKey, treeJSON, desktop.ActivePaneID, string(focusJSON), desktop.Revision, now, desktop.ID)
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
	return s.editDesktopRow(id, expectedRevision, func(tx *sql.Tx, desktop *profiles.Desktop) error {
		if desktop.Name == strings.TrimSpace(name) {
			return nil
		}
		desktop.Name = strings.TrimSpace(name)
		return appendLaunchDesktopFacts(tx, desktop.ID)
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

func deleteDesktop(tx *sql.Tx, id string) error {
	if _, err := tx.Exec(`DELETE FROM desktop_panes WHERE desktop_id = ?`, id); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM desktops WHERE id = ?`, id)
	return err
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
		if !layouttree.HasLeaf(desktop.Tree, paneID) {
			return profiles.Errorf(profiles.CodeNotFound, "leaf %q does not belong to desktop %s", paneID, desktopID)
		}
		if profile, err = loadLiveProfile(tx, desktop.ProfileID); err != nil {
			return err
		}
		if err := saveDesktopFocus(tx, now, &desktop, paneID); err != nil {
			return err
		}
		return touchProfileUse(tx, &profile, now)
	})
	return profile, desktop, err
}

func saveDesktopFocus(tx *sql.Tx, now string, desktop *profiles.Desktop, leafID string) error {
	*desktop = profiles.Focus(profiles.Focus(*desktop, desktop.ActivePaneID), leafID)
	focusJSON, err := json.Marshal(desktop.FocusHistory)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE desktops SET active_pane_id = ?, focus_history = ?, updated_at = ? WHERE id = ?`, leafID, string(focusJSON), now, desktop.ID)
	return err
}

func showDesktopLeaf(tx *sql.Tx, now string, profile *profiles.Profile, desktop *profiles.Desktop, leafID string) error {
	if !layouttree.HasLeaf(desktop.Tree, leafID) {
		return profiles.Errorf(profiles.CodeNotFound, "leaf %q does not belong to desktop %s", leafID, desktop.ID)
	}
	if err := saveDesktopFocus(tx, now, desktop, leafID); err != nil {
		return err
	}
	profile.CurrentDesktopID = desktop.ID
	profile.LastUsedAt = now
	_, err := tx.Exec(`UPDATE profiles SET current_desktop_id = ?, last_used_at = ? WHERE id = ?`, desktop.ID, now, profile.ID)
	return err
}

func (s *Store) ShowLeaf(desktopID, leafID string) (profiles.Profile, profiles.Desktop, string, error) {
	desktopID, leafID = strings.TrimSpace(desktopID), strings.TrimSpace(leafID)
	var profile profiles.Profile
	var desktop profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		if desktopID == "" || leafID == "" {
			return profiles.Errorf(profiles.CodeInvalid, "showing a leaf needs desktop_id and leaf_id")
		}
		var err error
		if desktop, err = loadDesktop(tx, desktopID); err != nil {
			return err
		}
		if profile, err = loadLiveProfile(tx, desktop.ProfileID); err != nil {
			return err
		}
		return showDesktopLeaf(tx, now, &profile, &desktop, leafID)
	})
	return profile, desktop, leafID, err
}

func (s *Store) ShowSession(sessionID protocol.SessionID) (profiles.Profile, profiles.Desktop, string, error) {
	sessionID = protocol.TrimID(sessionID)
	var profile profiles.Profile
	var desktop profiles.Desktop
	var leafID string
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		if sessionID == "" {
			return profiles.Errorf(profiles.CodeInvalid, "showing a session needs session_id")
		}
		profileID, err := openSessionProfileID(tx, sessionID)
		if err != nil {
			return err
		}
		if profileID == "" {
			return profiles.Errorf(profiles.CodeNotFound, "session %s belongs to no profile yet", sessionID)
		}
		if profile, err = loadLiveProfile(tx, profileID); err != nil {
			return err
		}
		tiles, err := sessionTiles(tx, sessionID)
		if err != nil {
			return err
		}
		if len(tiles) > 0 {
			if desktop, err = loadDesktop(tx, tiles[0].desktopID); err != nil {
				return err
			}
			leafID = tiles[0].tileID
			return showDesktopLeaf(tx, now, &profile, &desktop, leafID)
		}
		if desktop, leafID, err = placeShownSession(tx, now, profile, sessionID); err != nil {
			return err
		}
		return showDesktopLeaf(tx, now, &profile, &desktop, leafID)
	})
	return profile, desktop, leafID, err
}

func placeShownSession(tx *sql.Tx, now string, profile profiles.Profile, sessionID protocol.SessionID) (profiles.Desktop, string, error) {
	var title string
	if err := tx.QueryRow(`SELECT label FROM sessions WHERE id = ?`, sessionID).Scan(&title); err != nil {
		return profiles.Desktop{}, "", err
	}
	current, err := loadLaunchDesktop(tx, profile, "")
	if err != nil {
		return profiles.Desktop{}, "", err
	}
	paneID := newProfileEntityID("pane")
	desktop, err := placeSessionInTree(tx, current, SessionPlacementRequest{
		SessionID: sessionID,
		Title:     title,
		Status:    profiles.PaneStatusReady,
		Focus:     true,
	}, paneID)
	if err != nil {
		return profiles.Desktop{}, "", err
	}
	return desktop, paneID, writeCurrentDesktopArrangement(tx, now, &desktop)
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

func checkTileID(tx *sql.Tx, desktop profiles.Desktop, pane profiles.Pane) error {
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

func refuseSecondTile(tx *sql.Tx, desktop profiles.Desktop, pane profiles.Pane) error {
	for _, other := range desktop.Panes {
		if other.PaneID != pane.PaneID && other.SessionID == pane.SessionID {
			return profiles.Errorf(profiles.CodeAlreadyPlaced, "desktop %s: session %s is placed in tiles %s and %s", desktop.ID, pane.SessionID, other.PaneID, pane.PaneID)
		}
	}
	var holderDesktop, holderTile string
	held, err := rowFound(tx.QueryRow(`SELECT desktop_id, pane_id FROM desktop_panes WHERE session_id = ? AND desktop_id != ? LIMIT 1`, pane.SessionID, desktop.ID), &holderDesktop, &holderTile)
	if err != nil {
		return err
	}
	if held {
		return profiles.Errorf(profiles.CodeAlreadyPlaced, "session %s is already placed in tile %s of desktop %s", pane.SessionID, holderTile, holderDesktop)
	}
	return nil
}

func checkPaneMembership(tx *sql.Tx, desktop profiles.Desktop, arriving map[string]string) error {
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
		if err := checkTileID(tx, desktop, pane); err != nil {
			return err
		}
		if _, carried := arriving[pane.PaneID]; persisted || carried {
			continue
		}
		if err := refuseSecondTile(tx, desktop, pane); err != nil {
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

func insertCurrentDesktopPanes(tx *sql.Tx, now string, desktop profiles.Desktop, createdAt map[string]string) error {
	for _, pane := range desktop.Panes {
		at := createdAt[pane.PaneID]
		if at == "" {
			at = now
		}
		if pane.Kind == profiles.PaneKindAgent && pane.RuntimeID != "" {
			if err := bindTerminalTx(tx, pane.RuntimeID, pane.SessionID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`
			INSERT INTO desktop_panes (pane_id, desktop_id, kind, session_id, runtime_id, title, status, error, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			pane.PaneID, desktop.ID, string(pane.Kind), pane.SessionID, pane.RuntimeID, pane.Title, string(pane.Status), pane.Error, at, now); err != nil {
			return err
		}
	}
	return nil
}

func settleForWrite(desktop profiles.Desktop, previousTree layouttree.Node) profiles.Desktop {
	desktop = profiles.SettleAfter(desktop, previousTree)
	for i := range desktop.Panes {
		desktop.Panes[i].DesktopID = desktop.ID
		if desktop.Panes[i].Status == "" {
			desktop.Panes[i].Status = profiles.PaneStatusReady
		}
	}
	return desktop
}

func writeCurrentDesktopArrangement(tx *sql.Tx, now string, desktop *profiles.Desktop) error {
	return writeCurrentArrivingArrangement(tx, now, desktop, nil)
}

func writeCurrentArrivingArrangement(tx *sql.Tx, now string, desktop *profiles.Desktop, arrivingCreatedAt map[string]string) error {
	if err := layouttree.Validate(desktop.Tree); err != nil {
		return profiles.Errorf(profiles.CodeInvalid, "desktop %s: %v", desktop.ID, err)
	}
	previous, err := scanDesktopRow(tx.QueryRow(`SELECT `+desktopColumns+` FROM desktops WHERE id = ?`, desktop.ID))
	if err != nil {
		return err
	}
	desktop.FocusHistory = profiles.Focus(previous, previous.ActivePaneID).FocusHistory
	*desktop = settleForWrite(*desktop, previous.Tree)
	if err := profiles.CheckDesktop(*desktop); err != nil {
		return err
	}
	if err := checkPaneMembership(tx, *desktop, arrivingCreatedAt); err != nil {
		return err
	}
	createdAt, err := paneCreationTimes(tx, desktop.ID)
	if err != nil {
		return err
	}
	maps.Copy(createdAt, arrivingCreatedAt)
	if _, err := tx.Exec(`DELETE FROM desktop_panes WHERE desktop_id = ?`, desktop.ID); err != nil {
		return err
	}
	if err := insertCurrentDesktopPanes(tx, now, *desktop, createdAt); err != nil {
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
		return writeCurrentDesktopArrangement(tx, now, &desktop)
	})
	return desktop, err
}

func placeSessionInTree(tx *sql.Tx, desktop profiles.Desktop, request SessionPlacementRequest, paneID string) (profiles.Desktop, error) {
	anchor := strings.TrimSpace(request.AnchorPaneID)
	if anchor != "" && !layouttree.HasLeaf(desktop.Tree, anchor) {
		return desktop, profiles.Errorf(profiles.CodeNotFound, "anchor leaf %q does not belong to desktop %s", anchor, desktop.ID)
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
			return desktop, profiles.Errorf(profiles.CodeNotFound, "anchor leaf %q does not belong to desktop %s", anchor, desktop.ID)
		}
		if request.NewPaneShare > 0 && request.NewPaneShare < 1 {
			next, _ = layouttree.SetSplitRatio(next, splitID, ratio)
		}
		desktop.Tree = next
	}
	sessionID := protocol.TrimID(request.SessionID)
	runtimeID := protocol.TrimID(request.RuntimeID)
	if runtimeID == "" {
		err := tx.QueryRow(`SELECT terminal_id FROM terminal_bindings WHERE session_id = ? ORDER BY terminal_id LIMIT 1`, sessionID).Scan(&runtimeID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return desktop, err
		}
		if runtimeID == "" {
			runtimeID = protocol.TerminalID(uuid.NewString())
		}
	}
	desktop.Panes = append(desktop.Panes, profiles.Pane{
		PaneID:    paneID,
		Kind:      profiles.PaneKindAgent,
		SessionID: sessionID,
		RuntimeID: runtimeID,
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
	var desktop profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		current, err := loadDesktop(tx, request.DesktopID)
		if err != nil {
			return err
		}
		if err := requireRevision("desktop", current.ID, request.ExpectedRevision, current.Revision); err != nil {
			return err
		}
		if _, err := loadLiveProfile(tx, current.ProfileID); err != nil {
			return err
		}
		desktop, err = placeSessionInTree(tx, current, request, paneID)
		if err != nil {
			return err
		}
		return writeCurrentDesktopArrangement(tx, now, &desktop)
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
		var missing *profiles.Error
		if request.DesktopID != "" && errors.As(err, &missing) && missing.Code == profiles.CodeNotFound {
			request.AnchorPaneID, request.Focus = "", false
			current, err = loadLaunchDesktop(tx, profile, "")
		}
		if err != nil {
			return err
		}
		desktop, err = placeSessionInTree(tx, current, request, paneID)
		if err != nil {
			return err
		}
		return writeCurrentDesktopArrangement(tx, now, &desktop)
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
		}
	}
	source.Panes = withoutPane(source.Panes, leafID)
}

type LeafFollower struct {
	PaneID       string
	BesidePaneID string
}

func (s *Store) MoveLeaf(request LeafMoveRequest) (LeafMove, error) {
	return s.MoveLeafGroup(request, nil)
}

func (s *Store) MoveLeafGroup(request LeafMoveRequest, followers []LeafFollower) (LeafMove, error) {
	if request.SourceDesktopID == request.TargetDesktopID {
		return s.moveLeafWithinDesktop(request)
	}
	var result LeafMove
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		source, target, err := loadMoveEnds(tx, request)
		if err != nil {
			return err
		}
		result, err = moveLeafGroupBetweenDesktops(tx, now, source, target, request, followers)
		return err
	})
	return result, err
}

func recordArrivingPane(tx *sql.Tx, arrivals map[string]string, sourceID, targetID string) error {
	var createdAt string
	if _, err := rowFound(tx.QueryRow(`SELECT created_at FROM desktop_panes WHERE pane_id = ?`, sourceID), &createdAt); err != nil {
		return err
	}
	arrivals[targetID] = createdAt
	return nil
}

func moveLeafGroupBetweenDesktops(tx *sql.Tx, now string, source, target profiles.Desktop, request LeafMoveRequest, followers []LeafFollower) (LeafMove, error) {
	result, err := arrangeLeafBetweenDesktops(source, target, request)
	if err != nil {
		return LeafMove{}, err
	}
	arrivals := map[string]string{}
	if err := recordArrivingPane(tx, arrivals, request.LeafID, result.FinalLeafID); err != nil {
		return LeafMove{}, err
	}
	movedIDs := map[string]string{request.LeafID: result.FinalLeafID}
	for start := 0; start < len(followers); {
		end := start + 1
		for end < len(followers) && followers[end].BesidePaneID == followers[start].BesidePaneID {
			end++
		}
		for _, follower := range slices.Backward(followers[start:end]) {
			anchorID := movedIDs[follower.BesidePaneID]
			if anchorID == "" {
				return LeafMove{}, profiles.Errorf(profiles.CodeInvalid, "delegate pane %q has no moved dispatcher pane %q", follower.PaneID, follower.BesidePaneID)
			}
			moved, err := arrangeLeafBetweenDesktops(result.Source, result.Target, LeafMoveRequest{
				SourceDesktopID: request.SourceDesktopID,
				TargetDesktopID: request.TargetDesktopID,
				LeafID:          follower.PaneID,
				AnchorID:        anchorID,
				Direction:       layouttree.DirectionVertical,
			})
			if err != nil {
				return LeafMove{}, err
			}
			if err := recordArrivingPane(tx, arrivals, follower.PaneID, moved.FinalLeafID); err != nil {
				return LeafMove{}, err
			}
			movedIDs[follower.PaneID] = moved.FinalLeafID
			result.Source, result.Target = moved.Source, moved.Target
		}
		start = end
	}
	if err := writeCurrentDesktopArrangement(tx, now, &result.Source); err != nil {
		return LeafMove{}, err
	}
	if err := writeCurrentArrivingArrangement(tx, now, &result.Target, arrivals); err != nil {
		return LeafMove{}, err
	}
	return result, nil
}

func arrangeLeafBetweenDesktops(source, target profiles.Desktop, request LeafMoveRequest) (LeafMove, error) {
	moved, ok := layouttree.MoveLeafBetweenLayouts(source.Tree, target.Tree, request.LeafID, request.AnchorID, newProfileEntityID("split"), request.Direction, request.Before, firstChildRatio(request.LeafShare, request.Before), uuid.NewString())
	if !ok {
		return LeafMove{}, profiles.Errorf(profiles.CodeInvalid, "leaf %q could not move from desktop %s beside %q on desktop %s", request.LeafID, source.ID, request.AnchorID, target.ID)
	}
	source.Tree, target.Tree = moved.SourceLayout, moved.TargetLayout
	handOverPane(&source, &target, request.LeafID, moved.FinalLeafID)
	if request.Activate {
		target.ActivePaneID = moved.FinalLeafID
	}
	return LeafMove{Source: source, Target: target, FinalLeafID: moved.FinalLeafID}, nil
}

// SessionDesktopMove is what MoveSessionToDesktop did: Placed for an unplaced
// session, Move.Source empty when the session's newest tile already was on the desktop.
type SessionDesktopMove struct {
	Move       LeafMove
	FromLeafID string
	Placed     bool
}

// MoveSessionToDesktop puts a session's newest tile beside another desktop's active leaf;
// only a moved active tile, onto a desktop not on screen, takes that leaf.
func (s *Store) MoveSessionToDesktop(sessionID protocol.SessionID, targetDesktopID, title string) (SessionDesktopMove, error) {
	var result SessionDesktopMove
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		profileID, err := openSessionProfileID(tx, sessionID)
		if err != nil {
			return err
		}
		target, err := loadDesktop(tx, targetDesktopID)
		if err != nil {
			return err
		}
		if target.ProfileID != profileID {
			return profiles.Errorf(profiles.CodeCrossProfile, "desktop %s belongs to profile %s, and session %s to profile %s; a move between desktops cannot change membership", target.ID, target.ProfileID, sessionID, profileID)
		}
		profile, err := loadLiveProfile(tx, profileID)
		if err != nil {
			return err
		}
		tiles, err := sessionTiles(tx, sessionID)
		if err != nil {
			return err
		}
		if len(tiles) == 0 {
			paneID := newProfileEntityID("pane")
			desktop, err := placeSessionInTree(tx, target, SessionPlacementRequest{SessionID: sessionID, Direction: layouttree.DirectionVertical, Title: title, Status: profiles.PaneStatusReady}, paneID)
			if err != nil {
				return err
			}
			if err := writeCurrentDesktopArrangement(tx, now, &desktop); err != nil {
				return err
			}
			result = SessionDesktopMove{Move: LeafMove{Target: desktop, FinalLeafID: paneID}, Placed: true}
			return nil
		}
		tile := tiles[0]
		if tile.desktopID == target.ID {
			result = SessionDesktopMove{Move: LeafMove{Target: target, FinalLeafID: tile.tileID}}
			return nil
		}
		source, err := loadDesktop(tx, tile.desktopID)
		if err != nil {
			return err
		}
		move, err := moveLeafGroupBetweenDesktops(tx, now, source, target, LeafMoveRequest{
			LeafID: tile.tileID, AnchorID: target.ActivePaneID, Direction: layouttree.DirectionVertical,
			Activate: source.ActivePaneID == tile.tileID && target.ID != profile.CurrentDesktopID,
		}, nil)
		result = SessionDesktopMove{Move: move, FromLeafID: tile.tileID}
		return err
	})
	return result, err
}

func openSessionProfileID(tx *sql.Tx, sessionID protocol.SessionID) (string, error) {
	var (
		profileID string
		closedAt  string
	)
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

func (s *Store) SessionProfileID(sessionID protocol.SessionID) (string, error) {
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

func (s *Store) SessionPlacement(sessionID protocol.SessionID) (profiles.Placement, bool, error) {
	var placement profiles.Placement
	found := false
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		var err error
		found, err = rowFound(tx.QueryRow(`
			SELECT d.profile_id, p.desktop_id, p.pane_id
			FROM desktop_panes p JOIN desktops d ON d.id = p.desktop_id
			WHERE p.session_id = ? ORDER BY p.created_at DESC, p.pane_id DESC LIMIT 1`, sessionID), &placement.ProfileID, &placement.DesktopID, &placement.PaneID)
		return err
	})
	return placement, found, err
}

type sessionTile struct {
	desktopID, tileID string
}

func sessionTiles(tx *sql.Tx, sessionID protocol.SessionID) ([]sessionTile, error) {
	rows, err := tx.Query(`SELECT desktop_id, pane_id FROM desktop_panes WHERE session_id = ? ORDER BY created_at DESC, pane_id DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tiles []sessionTile
	for rows.Next() {
		var tile sessionTile
		if err := rows.Scan(&tile.desktopID, &tile.tileID); err != nil {
			return nil, err
		}
		tiles = append(tiles, tile)
	}
	return tiles, rows.Err()
}

func removeSessionPlacement(tx *sql.Tx, now string, sessionID protocol.SessionID) ([]profiles.Desktop, error) {
	tiles, err := sessionTiles(tx, sessionID)
	if err != nil {
		return nil, err
	}
	var changed []profiles.Desktop
	for _, tile := range tiles {
		desktop, err := removeTile(tx, now, tile)
		if err != nil {
			return nil, err
		}
		changed = slices.DeleteFunc(changed, func(d profiles.Desktop) bool { return d.ID == desktop.ID })
		changed = append(changed, desktop)
	}
	return changed, nil
}

func removeTile(tx *sql.Tx, now string, tile sessionTile) (profiles.Desktop, error) {
	desktop, err := loadDesktop(tx, tile.desktopID)
	if err != nil {
		return desktop, err
	}
	next, ok := layouttree.Remove(desktop.Tree, tile.tileID)
	if !ok {
		return desktop, profiles.Errorf(profiles.CodeInvalid, "pane %s has a row on desktop %s but no leaf in its tree", tile.tileID, tile.desktopID)
	}
	desktop.Tree = next
	desktop.Panes = withoutPane(desktop.Panes, tile.tileID)
	return desktop, writeCurrentDesktopArrangement(tx, now, &desktop)
}

func (s *Store) RemoveTerminalTile(terminal protocol.TerminalID) (desktop profiles.Desktop, removed bool, err error) {
	err = s.profilesTx(func(tx *sql.Tx, now string) error {
		if _, err := tx.Exec(`DELETE FROM terminal_bindings WHERE terminal_id = ?`, terminal); err != nil {
			return err
		}
		var tile sessionTile
		found, err := rowFound(tx.QueryRow(`SELECT desktop_id, pane_id FROM desktop_panes WHERE runtime_id = ?`, terminal), &tile.desktopID, &tile.tileID)
		if err != nil || !found {
			return err
		}
		desktop, err = removeTile(tx, now, tile)
		removed = err == nil
		return err
	})
	return desktop, removed, err
}

func (s *Store) unplaceSessionLocked(at time.Time, sessionID protocol.SessionID) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := at.Format(sortableTimeFormat)
	if _, err := removeSessionPlacement(tx, now, sessionID); err != nil {
		return err
	}
	emptied, err := stampEmptyDesktops(tx, now)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.announceTerminalBindingsLocked()
	if emptied {
		s.announceEmptyDesktop(at)
	}
	return nil
}

func (s *Store) unplaceSessionsLocked(reason, where string, args ...any) {
	sessionIDs, err := queryColumn[string](s.db, `SELECT DISTINCT session_id FROM desktop_panes WHERE `+where, args...)
	if err != nil {
		log.Printf("[store] %s: listing placed sessions: %v", reason, err)
		return
	}
	at := time.Now().UTC()
	for _, id := range sessionIDs {
		if err := s.unplaceSessionLocked(at, protocol.SessionID(id)); err != nil {
			log.Printf("[store] %s: removing the pane of session %s: %v", reason, id, err)
		}
	}
}

func (s *Store) RemoveSessionPlacement(sessionID protocol.SessionID) ([]profiles.Desktop, error) {
	var desktops []profiles.Desktop
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		var err error
		desktops, err = removeSessionPlacement(tx, now, sessionID)
		return err
	})
	return desktops, err
}
