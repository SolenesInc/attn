package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

type CrewIdentity struct {
	Key       string
	ProfileID string
	Name      string
}

func (s *Store) CrewIdentities() (map[string]CrewIdentity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, profiles.Errorf(profiles.CodeUnavailable, "crew members need the SQLite store")
	}
	rows, err := s.db.Query(`SELECT member_key, profile_id, name FROM crew_members`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	identities := make(map[string]CrewIdentity)
	for rows.Next() {
		var m CrewIdentity
		if err := rows.Scan(&m.Key, &m.ProfileID, &m.Name); err != nil {
			return nil, err
		}
		identities[m.Key] = m
	}
	return identities, rows.Err()
}

func (s *Store) CrewProfile(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return "", profiles.Errorf(profiles.CodeUnavailable, "profiles need the SQLite store")
	}
	var profileID string
	err := s.db.QueryRow(`SELECT profile_id FROM crew_members WHERE member_key = ?`, key).Scan(&profileID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return profileID, err
}

func (s *Store) EnsureCrewMember(key, profileID string) (CrewIdentity, error) {
	identity := CrewIdentity{Key: key}
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		err := tx.QueryRow(`SELECT profile_id, name FROM crew_members WHERE member_key = ?`, key).Scan(&identity.ProfileID, &identity.Name)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := loadLiveProfile(tx, profileID); err != nil {
			return err
		}
		names, err := queryColumn[string](tx, `SELECT lower(name) FROM crew_members WHERE profile_id = ?`, profileID)
		if err != nil {
			return err
		}
		taken := make(map[string]bool, len(names))
		for _, name := range names {
			taken[name] = true
		}
		identity.ProfileID = profileID
		identity.Name = crew.UnusedName(crew.NameFromKey(key), func(name string) bool { return taken[strings.ToLower(name)] })
		if _, err := tx.Exec(`INSERT INTO crew_members(member_key, profile_id, name) VALUES (?, ?, ?)`, key, profileID, identity.Name); err != nil {
			return err
		}
		return startOnOwnDesktop(tx, now, "crew", key)
	})
	return identity, err
}

func (s *Store) RenameCrewMember(key, name string) (previous string, err error) {
	if err := crew.ValidateName(name); err != nil {
		return "", err
	}
	err = s.profilesTx(func(tx *sql.Tx, _ string) error {
		var profileID string
		err := tx.QueryRow(`SELECT profile_id, name FROM crew_members WHERE member_key = ?`, key).Scan(&profileID, &previous)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("crew member %s has no identity record", key)
		}
		if err != nil {
			return err
		}
		var holder string
		err = tx.QueryRow(`SELECT name FROM crew_members WHERE profile_id = ? AND name = ? COLLATE NOCASE AND member_key != ?`, profileID, name, key).Scan(&holder)
		if err == nil {
			return fmt.Errorf("the name %q is already taken by %s in this profile", name, holder)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.Exec(`UPDATE crew_members SET name = ? WHERE member_key = ?`, name, key)
		return err
	})
	return previous, err
}

func (s *Store) RecordMemberSession(key string, id protocol.SessionID) error {
	return s.profilesTx(func(tx *sql.Tx, _ string) error {
		var stored string
		if err := tx.QueryRow(`SELECT member_key FROM sessions WHERE id = ?`, id).Scan(&stored); err != nil {
			return fmt.Errorf("record session %s for crew member %s: %w", id, key, err)
		}
		if stored != "" && stored != key {
			return fmt.Errorf("session %s already belongs to crew member %s", id, stored)
		}
		if _, err := tx.Exec(`UPDATE sessions SET member_key = ? WHERE id = ?`, key, id); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE crew_members SET latest_session = ? WHERE member_key = ?`, id, key)
		return err
	})
}
