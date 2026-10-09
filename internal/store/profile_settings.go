package store

import (
	"database/sql"
	"fmt"
	"maps"
	"strings"
)

func (s *Store) ProfileSetting(profileID, key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.profileSettings[profileID][key]
}

func (s *Store) ProfileSettings(profileID string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.profileSettings[profileID])
}

func (s *Store) SetProfileSetting(profileID, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := loadLiveProfile(tx, profileID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO profile_settings (profile_id, key, value) VALUES (?, ?, ?) ON CONFLICT(profile_id, key) DO UPDATE SET value = excluded.value`, profileID, key, value); err != nil {
		return fmt.Errorf("set profile setting %q: %w", key, err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.profileSettings[profileID] == nil {
		s.profileSettings[profileID] = make(map[string]string)
	}
	s.profileSettings[profileID][key] = value
	return nil
}

func readProfileSettings(db *sql.DB) (map[string]map[string]string, error) {
	rows, err := db.Query(`SELECT profile_id, key, value FROM profile_settings`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table: profile_settings") {
			return make(map[string]map[string]string), nil
		}
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]map[string]string)
	for rows.Next() {
		var id, key, value string
		if err := rows.Scan(&id, &key, &value); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = make(map[string]string)
		}
		out[id][key] = value
	}
	return out, rows.Err()
}
