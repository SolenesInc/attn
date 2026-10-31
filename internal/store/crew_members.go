package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

type CrewIdentity struct {
	Key       string
	ProfileID string
	Name      string
	Retired   bool
	Chief     bool
}

func (s *Store) CrewIdentities() (map[string]CrewIdentity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, profiles.Errorf(profiles.CodeUnavailable, "crew members need the SQLite store")
	}
	rows, err := s.db.Query(`SELECT m.member_key, m.profile_id, m.name, m.retired_at != '', COALESCE(p.chief_member = m.member_key, 0)
		FROM crew_members m LEFT JOIN profiles p ON p.id = m.profile_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	identities := make(map[string]CrewIdentity)
	for rows.Next() {
		var m CrewIdentity
		if err := rows.Scan(&m.Key, &m.ProfileID, &m.Name, &m.Retired, &m.Chief); err != nil {
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

func (s *Store) CreateCrewMember(identity CrewIdentity) error {
	if err := crew.ValidateName(identity.Name); err != nil {
		return err
	}
	return s.profilesTx(func(tx *sql.Tx, now string) error {
		if _, err := loadLiveProfile(tx, identity.ProfileID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO crew_members(member_key, profile_id, name) VALUES (?, ?, ?)`, identity.Key, identity.ProfileID, identity.Name); err != nil {
			return err
		}
		return startOnOwnDesktop(tx, now, "crew", identity.Key)
	})
}

func (s *Store) ProfileChiefMembers() (map[string]string, error) {
	chiefs := map[string]string{}
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		rows, err := tx.Query(`SELECT id, chief_member FROM profiles WHERE deleted_at = ''`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var profileID, key string
			if err := rows.Scan(&profileID, &key); err != nil {
				return err
			}
			chiefs[profileID] = key
		}
		return rows.Err()
	})
	return chiefs, err
}

func (s *Store) CreateChiefMember(profileID, key string) error {
	return s.profilesTx(func(tx *sql.Tx, now string) error {
		if _, err := loadLiveProfile(tx, profileID); err != nil {
			return err
		}
		result, err := tx.Exec(`UPDATE profiles SET chief_member = ? WHERE id = ? AND chief_member = ''`, key, profileID)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed == 0 {
			if err == nil {
				err = fmt.Errorf("profile %s already has a Chief", profileID)
			}
			return err
		}
		if _, err := tx.Exec(`INSERT INTO crew_members(member_key, profile_id, name) VALUES (?, ?, ?)`, key, profileID, crew.ChiefName); err != nil {
			return err
		}
		return startOnOwnDesktop(tx, now, "crew", key)
	})
}

func (s *Store) DeleteCrewMember(key string) error {
	return s.profilesTx(func(tx *sql.Tx, _ string) error {
		_, err := tx.Exec(`DELETE FROM crew_members WHERE member_key = ?`, key)
		return err
	})
}

type CrewRetirement struct {
	AlreadyRetired bool
	RemovedWatches int
	ClosedAsks     []string
}

func (s *Store) RetireCrewMember(key who.MemberKey, at time.Time) (CrewRetirement, error) {
	var retirement CrewRetirement
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		result, err := tx.Exec(`UPDATE crew_members SET retired_at = ? WHERE member_key = ? AND retired_at = ''`, at.UTC().Format(time.RFC3339Nano), key.String())
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		retirement.AlreadyRetired = changed == 0
		result, err = tx.Exec(`DELETE FROM garden_seed_watches WHERE watcher = ?`, who.Member(key))
		if err != nil {
			return err
		}
		removed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		retirement.RemovedWatches = int(removed)
		retirement.ClosedAsks, err = closeAsksTo(tx, who.Member(key).Address())
		return err
	})
	return retirement, err
}

func (s *Store) RestoreCrewMember(key string) (bool, error) {
	restored := false
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		result, err := tx.Exec(`UPDATE crew_members SET retired_at = '' WHERE member_key = ? AND retired_at != ''`, key)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		restored = changed != 0
		return err
	})
	return restored, err
}

func (s *Store) RenameCrewMember(key, name string) (previous string, err error) {
	err = s.profilesTx(func(tx *sql.Tx, _ string) error {
		previous, err = renameCrewMember(tx, key, name)
		return err
	})
	return previous, err
}

func (s *Store) RenameCrewMemberMovingHome(key, name string, home DocumentCommit, now time.Time) (previous string, written DocumentWriteResult, err error) {
	table, err := s.documentTable(home.Write.Schema)
	if err != nil {
		return "", DocumentWriteResult{}, err
	}
	err = s.profilesTx(func(tx *sql.Tx, _ string) error {
		if previous, err = renameCrewMember(tx, key, name); err != nil {
			return err
		}
		results, _, err := commitDocumentWritesWith(tx, []DocumentCommit{home}, []string{table}, now)
		if err != nil {
			return err
		}
		written = results[0]
		return nil
	})
	return previous, written, err
}

func renameCrewMember(tx *sql.Tx, key, name string) (previous string, err error) {
	var profileID string
	err = tx.QueryRow(`SELECT profile_id, name FROM crew_members WHERE member_key = ?`, key).Scan(&profileID, &previous)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("crew member %s has no identity record", key)
	}
	if err != nil {
		return "", err
	}
	var holder string
	err = tx.QueryRow(`SELECT name FROM crew_members WHERE profile_id = ? AND name = ? COLLATE NOCASE AND member_key != ?`, profileID, name, key).Scan(&holder)
	if err == nil {
		return "", fmt.Errorf("the name %q is already taken by %s in this profile", name, holder)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	_, err = tx.Exec(`UPDATE crew_members SET name = ? WHERE member_key = ?`, name, key)
	return previous, err
}

func (s *Store) RecordMemberSession(key string, id protocol.SessionID) error {
	return s.profilesTx(func(tx *sql.Tx, _ string) error {
		return recordMemberSessionTx(tx, key, id)
	})
}

func recordMemberSessionTx(tx *sql.Tx, key string, id protocol.SessionID) error {
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
}
