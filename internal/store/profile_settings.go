package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
)

func (s *Store) ProfileSetting(profileID, key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return ""
	}
	var value string
	if err := s.db.QueryRow(`SELECT value FROM profile_settings WHERE profile_id = ? AND key = ?`, profileID, key).Scan(&value); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("[store] read profile setting %q of %s: %v", key, profileID, err)
		}
		return ""
	}
	return value
}

func (s *Store) LiveProfileSettingValues(key string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	values := map[string]string{}
	if s.db == nil {
		return values, nil
	}
	rows, err := s.db.Query(`SELECT ps.profile_id, ps.value FROM profile_settings ps JOIN profiles p ON p.id = ps.profile_id WHERE ps.key = ? AND p.deleted_at = ''`, key)
	if err != nil {
		return nil, fmt.Errorf("read profile setting %q: %w", key, err)
	}
	defer rows.Close()
	for rows.Next() {
		var profileID, value string
		if err := rows.Scan(&profileID, &value); err != nil {
			return nil, err
		}
		values[profileID] = value
	}
	return values, rows.Err()
}

func (s *Store) SetProfileSetting(profileID, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("settings database is unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := loadLiveProfile(tx, profileID); err != nil {
		return err
	}
	if value == "" {
		_, err = tx.Exec(`DELETE FROM profile_settings WHERE profile_id = ? AND key = ?`, profileID, key)
	} else {
		_, err = tx.Exec(`INSERT INTO profile_settings (profile_id, key, value) VALUES (?, ?, ?) ON CONFLICT(profile_id, key) DO UPDATE SET value = excluded.value`, profileID, key, value)
	}
	if err != nil {
		return fmt.Errorf("set profile setting %q: %w", key, err)
	}
	return tx.Commit()
}
