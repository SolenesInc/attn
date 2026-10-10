package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

type CrewIdentity struct {
	Key       who.MemberKey
	ProfileID string
	Name      string
	Retired   bool
}
type CrewNameTakenError struct{ Name, Holder string }

func (e *CrewNameTakenError) Error() string {
	return fmt.Sprintf("crew name %q is already taken by %s in this profile", e.Name, e.Holder)
}

const crewIdentityColumns = "member_key, profile_id, name, retired_at"

func scanCrewIdentity(row interface{ Scan(...any) error }) (CrewIdentity, error) {
	var m CrewIdentity
	var retired string
	err := row.Scan(&m.Key, &m.ProfileID, &m.Name, &retired)
	m.Retired = retired != ""
	return m, err
}
func (s *Store) CrewNamed(profileID, name string) (CrewIdentity, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	name = strings.TrimSpace(name)
	if s.db == nil {
		for _, m := range s.crewMembers {
			if m.ProfileID == profileID && strings.EqualFold(m.Name, name) {
				return m, true, nil
			}
		}
		return CrewIdentity{}, false, nil
	}
	return s.findCrewDB("profile_id = ? AND name = ? COLLATE NOCASE", profileID, name)
}
func (s *Store) CrewKeyed(profileID, keyText string) (CrewIdentity, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		for _, m := range s.crewMembers {
			if m.ProfileID == profileID && m.Key.String() == keyText {
				return m, true, nil
			}
		}
		return CrewIdentity{}, false, nil
	}
	return s.findCrewDB("profile_id = ? AND member_key = ?", profileID, keyText)
}
func (s *Store) findCrewDB(where string, args ...any) (CrewIdentity, bool, error) {
	m, err := scanCrewIdentity(s.db.QueryRow("SELECT "+crewIdentityColumns+" FROM crew_members WHERE "+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return CrewIdentity{}, false, nil
	}
	return m, err == nil, err
}
func (s *Store) CrewIdentity(key who.MemberKey) (CrewIdentity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var m CrewIdentity
	var ok bool
	var err error
	if s.db == nil {
		m, ok = s.crewMembers[key]
	} else {
		m, ok, err = s.findCrewDB("member_key = ?", key)
	}
	if err == nil && !ok {
		err = fmt.Errorf("crew member %s not found", key)
	}
	return m, err
}
func (s *Store) CrewNames() (map[who.MemberKey]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := map[who.MemberKey]string{}
	if s.db == nil {
		for key, m := range s.crewMembers {
			names[key] = m.Name
		}
		return names, nil
	}
	rows, err := s.db.Query(`SELECT member_key,name FROM crew_members`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key who.MemberKey
		var name string
		if err := rows.Scan(&key, &name); err != nil {
			return nil, err
		}
		names[key] = name
	}
	return names, rows.Err()
}

func (s *Store) CrewRoster(profileID string) ([]CrewIdentity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []CrewIdentity{}
	if s.db == nil {
		for _, m := range s.crewMembers {
			if m.ProfileID == profileID {
				out = append(out, m)
			}
		}
		sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
		return out, nil
	}
	rows, err := s.db.Query("SELECT "+crewIdentityColumns+" FROM crew_members WHERE profile_id = ? ORDER BY name COLLATE NOCASE", profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanCrewIdentity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func ensureCrewNameFree(tx *sql.Tx, profile, name string, key who.MemberKey) error {
	var holder string
	err := tx.QueryRow("SELECT name FROM crew_members WHERE profile_id = ? AND name = ? COLLATE NOCASE AND member_key != ?", profile, name, key).Scan(&holder)
	if err == nil {
		return &CrewNameTakenError{Name: name, Holder: holder}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
func (s *Store) EnsureCrewMember(m CrewIdentity) (CrewIdentity, error) {
	m.Name = strings.TrimSpace(m.Name)
	if err := crew.ValidateName(m.Name); err != nil {
		return CrewIdentity{}, err
	}
	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if existing, ok := s.crewMembers[m.Key]; ok {
			return existing, nil
		}
		for _, existing := range s.crewMembers {
			if existing.ProfileID == m.ProfileID && strings.EqualFold(existing.Name, m.Name) {
				return CrewIdentity{}, &CrewNameTakenError{Name: m.Name, Holder: existing.Name}
			}
		}
		if m.Key.IsZero() {
			return CrewIdentity{}, who.ErrNobody
		}
		if s.crewMembers == nil {
			s.crewMembers = map[who.MemberKey]CrewIdentity{}
		}
		s.crewMembers[m.Key] = m
		return m, nil
	}
	var result CrewIdentity
	err := s.profilesTx(func(tx *sql.Tx, now string) error {
		existing, err := scanCrewIdentity(tx.QueryRow("SELECT "+crewIdentityColumns+" FROM crew_members WHERE member_key = ?", m.Key))
		if err == nil {
			result = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := loadLiveProfile(tx, m.ProfileID); err != nil {
			return err
		}
		if err := ensureCrewNameFree(tx, m.ProfileID, m.Name, m.Key); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO crew_members(member_key,profile_id,name) VALUES(?,?,?)", m.Key, m.ProfileID, m.Name); err != nil {
			return err
		}
		result = m
		return startOnOwnDesktop(tx, now, "crew", m.Key.String())
	})
	return result, err
}
func (s *Store) RenameCrewMember(key who.MemberKey, name string) (string, error) {
	name = strings.TrimSpace(name)
	if err := crew.ValidateName(name); err != nil {
		if !errors.Is(err, crew.ErrNameReservedForChief) {
			return "", err
		}
		identity, readErr := s.CrewIdentity(key)
		if readErr != nil {
			return "", readErr
		}
		chief, readErr := s.ProfileChief(identity.ProfileID)
		if readErr != nil || chief != key {
			return "", err
		}
	}
	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		m, ok := s.crewMembers[key]
		if !ok {
			return "", fmt.Errorf("crew member %s not found", key)
		}
		for _, existing := range s.crewMembers {
			if existing.Key != key && existing.ProfileID == m.ProfileID && strings.EqualFold(existing.Name, name) {
				return "", &CrewNameTakenError{Name: name, Holder: existing.Name}
			}
		}
		previous := m.Name
		m.Name = name
		s.crewMembers[key] = m
		return previous, nil
	}
	var previous string
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		m, err := scanCrewIdentity(tx.QueryRow("SELECT "+crewIdentityColumns+" FROM crew_members WHERE member_key = ?", key))
		if err != nil {
			return err
		}
		if err := ensureCrewNameFree(tx, m.ProfileID, name, key); err != nil {
			return err
		}
		previous = m.Name
		_, err = tx.Exec("UPDATE crew_members SET name = ? WHERE member_key = ?", name, key)
		return err
	})
	return previous, err
}
func recordMemberKey(tx *sql.Tx, key who.MemberKey, id protocol.SessionID) error {
	var stored string
	if err := tx.QueryRow("SELECT member_key FROM sessions WHERE id = ?", id).Scan(&stored); err != nil {
		return err
	}
	if stored != "" && stored != key.String() {
		return fmt.Errorf("session %s already belongs to member %s", id, stored)
	}
	_, err := tx.Exec("UPDATE sessions SET member_key = ? WHERE id = ?", key, id)
	return err
}
func (s *Store) recordMemberKeyMemory(key who.MemberKey, id protocol.SessionID) error {
	if _, ok := s.crewMembers[key]; !ok {
		return fmt.Errorf("crew member %s not found", key)
	}
	if s.sessions[id] == nil {
		return fmt.Errorf("session %s not found", id)
	}
	if old := s.memberSessions[id]; !old.IsZero() && old != key {
		return fmt.Errorf("session %s already belongs to member %s", id, old)
	}
	if s.memberSessions == nil {
		s.memberSessions = map[protocol.SessionID]who.MemberKey{}
	}
	s.memberSessions[id] = key
	return nil
}
func (s *Store) RecordMemberLaunchIntent(key who.MemberKey, id protocol.SessionID) error {
	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.recordMemberKeyMemory(key, id)
	}
	return s.profilesTx(func(tx *sql.Tx, _ string) error { return recordMemberKey(tx, key, id) })
}
func (s *Store) RecordMemberSession(key who.MemberKey, id protocol.SessionID) error {
	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.recordMemberKeyMemory(key, id); err != nil {
			return err
		}
		if s.latestMemberSessions == nil {
			s.latestMemberSessions = map[who.MemberKey]protocol.SessionID{}
		}
		s.latestMemberSessions[key] = id
		return nil
	}
	return s.profilesTx(func(tx *sql.Tx, _ string) error {
		if err := recordMemberKey(tx, key, id); err != nil {
			return err
		}
		_, err := tx.Exec("UPDATE crew_members SET latest_session = ? WHERE member_key = ?", id, key)
		return err
	})
}

func (s *Store) MemberLatestSession(key who.MemberKey) (protocol.SessionID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return s.latestMemberSessions[key], nil
	}
	var id protocol.SessionID
	err := s.db.QueryRow("SELECT latest_session FROM crew_members WHERE member_key = ?", key).Scan(&id)
	return id, err
}

func (s *Store) MemberLatestSessions() ([]protocol.SessionID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := []protocol.SessionID{}
	if s.db == nil {
		for _, id := range s.latestMemberSessions {
			if id != "" {
				ids = append(ids, id)
			}
		}
		return ids, nil
	}
	rows, err := s.db.Query("SELECT latest_session FROM crew_members WHERE latest_session != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id protocol.SessionID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type NewCrewMember struct {
	Identity CrewIdentity
	Doc      DocumentWrite
	Fact     BusEvent
	Desktop  *LaunchDesktopSetting
}

func (s *Store) CreateCrewMember(m NewCrewMember, now time.Time) (DocumentWriteResult, error) {
	return s.commitCrewBirth(m, now, true)
}

func (s *Store) FurnishCrewMember(m NewCrewMember, now time.Time) (DocumentWriteResult, error) {
	return s.commitCrewBirth(m, now, false)
}

func (s *Store) commitCrewBirth(m NewCrewMember, now time.Time, insert bool) (DocumentWriteResult, error) {
	table, err := s.documentTable(m.Doc.Schema)
	if err != nil {
		return DocumentWriteResult{}, err
	}
	var result DocumentWriteResult
	err = s.profilesTx(func(tx *sql.Tx, stamp string) error {
		if _, err := loadLiveProfile(tx, m.Identity.ProfileID); err != nil {
			return err
		}
		if insert {
			if err := ensureCrewNameFree(tx, m.Identity.ProfileID, m.Identity.Name, m.Identity.Key); err != nil {
				return err
			}
			if _, err := tx.Exec("INSERT INTO crew_members(member_key,profile_id,name) VALUES(?,?,?)", m.Identity.Key, m.Identity.ProfileID, m.Identity.Name); err != nil {
				return err
			}
		} else {
			if _, err := scanCrewIdentity(tx.QueryRow("SELECT "+crewIdentityColumns+" FROM crew_members WHERE member_key = ? AND profile_id = ?", m.Identity.Key, m.Identity.ProfileID)); err != nil {
				return err
			}
		}
		results, _, err := commitDocumentWritesWith(tx, []DocumentCommit{{Write: m.Doc, Fact: m.Fact}}, []string{table}, now)
		if err != nil {
			return err
		}
		if m.Desktop == nil {
			err = startOnOwnDesktop(tx, stamp, "crew", m.Identity.Key.String())
		} else {
			err = saveLaunchSetting(tx, stamp, "crew", m.Identity.Key.String(), *m.Desktop, false)
		}
		result = results[0]
		return err
	})
	return result, err
}

func (s *Store) RetireCrewMember(key who.MemberKey, at time.Time) (int, error) {
	removed := 0
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		if _, err := tx.Exec("UPDATE crew_members SET retired_at = ? WHERE member_key = ? AND retired_at = ''", at.UTC().Format(time.RFC3339Nano), key); err != nil {
			return err
		}
		result, err := tx.Exec("DELETE FROM pull_request_watches WHERE watcher = ?", who.Member(key))
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		removed = int(count)
		result, err = tx.Exec("DELETE FROM garden_seed_watches WHERE watcher=?", who.Member(key))
		if err != nil {
			return err
		}
		count, err = result.RowsAffected()
		removed += int(count)
		return err
	})
	return removed, err
}

func (s *Store) RestoreCrewMember(key who.MemberKey) (bool, error) {
	restored := false
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		result, err := tx.Exec("UPDATE crew_members SET retired_at = '' WHERE member_key = ? AND retired_at != ''", key)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		restored = count != 0
		return err
	})
	return restored, err
}
