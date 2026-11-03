package store

import (
	"time"
)

type PhonePairing struct {
	DeviceID       string
	ProfileID      string
	DeviceSignPub  []byte
	DeviceNoisePub []byte
	Name           string
	CreatedAt      time.Time
	LastSeenAt     time.Time
}

func (s *Store) PhonePairings() ([]PhonePairing, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT device_id, profile_id, device_sign_pub, device_noise_pub, name, created_at, last_seen_at
		FROM phone_pairings ORDER BY created_at, device_id, profile_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pairings := []PhonePairing{}
	for rows.Next() {
		var p PhonePairing
		var created, seen string
		if err := rows.Scan(&p.DeviceID, &p.ProfileID, &p.DeviceSignPub, &p.DeviceNoisePub, &p.Name, &created, &seen); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		p.LastSeenAt, _ = time.Parse(time.RFC3339Nano, seen)
		pairings = append(pairings, p)
	}
	return pairings, rows.Err()
}

func (s *Store) AddPhonePairing(p PhonePairing) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := p.CreatedAt.UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`INSERT INTO phone_pairings (device_id, profile_id, device_sign_pub, device_noise_pub, name, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id, profile_id) DO UPDATE SET device_sign_pub = excluded.device_sign_pub,
			device_noise_pub = excluded.device_noise_pub, name = excluded.name, created_at = excluded.created_at, last_seen_at = excluded.last_seen_at`,
		p.DeviceID, p.ProfileID, p.DeviceSignPub, p.DeviceNoisePub, p.Name, now, now)
	return err
}

func (s *Store) TouchPhonePairing(deviceID, profileID string, seen time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE phone_pairings SET last_seen_at = ? WHERE device_id = ? AND profile_id = ?`,
		seen.UTC().Format(time.RFC3339Nano), deviceID, profileID)
	return err
}

func (s *Store) RemovePhonePairing(deviceID, profileID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`DELETE FROM phone_pairings WHERE device_id = ? AND profile_id = ?`, deviceID, profileID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}
