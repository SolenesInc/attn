package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/who"
)

type SeedStandby struct {
	ProfileID string
	Tender    who.Party
	garden.Standby
}

const seedStandbyColumns = `seed_id, profile_id, tender, note, set_at, set_by`

func scanSeedStandby(scan func(...any) error) (string, SeedStandby, error) {
	var seedID, setAt string
	var standby SeedStandby
	if err := scan(&seedID, &standby.ProfileID, &standby.Tender, &standby.Note, &setAt, &standby.SetBy); err != nil {
		return "", SeedStandby{}, err
	}
	at, err := time.Parse(time.RFC3339Nano, setAt)
	if err != nil {
		return "", SeedStandby{}, err
	}
	standby.SetAt = at
	return seedID, standby, nil
}

func (s *Store) SeedStandby(seedID string) (*SeedStandby, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, standby, err := scanSeedStandby(s.db.QueryRow(`SELECT `+seedStandbyColumns+` FROM seed_standby WHERE seed_id = ?`, seedID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &standby, nil
}

func (s *Store) SeedsOnStandby() (map[string]SeedStandby, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT ` + seedStandbyColumns + ` FROM seed_standby`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SeedStandby{}
	for rows.Next() {
		seedID, standby, err := scanSeedStandby(rows.Scan)
		if err != nil {
			return nil, err
		}
		out[seedID] = standby
	}
	return out, rows.Err()
}

func (s *Store) SeedsOnStandbyTendedBy(profileID string, tender who.Party) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT seed_id FROM seed_standby WHERE profile_id = ? AND tender = ? ORDER BY seed_id`, profileID, tender)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func keepSeedStandbyOnlyFor(tx *sql.Tx, seedID string, tender who.Party) error {
	if tender.IsZero() {
		_, err := tx.Exec(`DELETE FROM seed_standby WHERE seed_id = ?`, seedID)
		return err
	}
	_, err := tx.Exec(`DELETE FROM seed_standby WHERE seed_id = ? AND tender != ?`, seedID, tender)
	return err
}

func putSeedStandby(tx *sql.Tx, seedID string, standby SeedStandby) error {
	_, err := tx.Exec(`INSERT INTO seed_standby (`+seedStandbyColumns+`) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(seed_id) DO UPDATE SET profile_id = excluded.profile_id, tender = excluded.tender,
			note = excluded.note, set_at = excluded.set_at, set_by = excluded.set_by`,
		seedID, standby.ProfileID, standby.Tender, standby.Note, standby.SetAt.UTC().Format(time.RFC3339Nano), standby.SetBy)
	return err
}
