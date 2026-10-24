package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

var (
	ErrVentNotFound = errors.New("no such vent")
)

type VentTurnTakenError struct{ VentID string }

func (e *VentTurnTakenError) Error() string {
	return fmt.Sprintf("this turn already has vent %s", e.VentID)
}

type Vent struct {
	ID            string
	ProfileID     string
	SessionID     protocol.SessionID
	Turn          string
	Text          string
	Context       protocol.VentContext
	CreatedAt     time.Time
	WithdrawnAt   time.Time
	DismissedAt   time.Time
	PlantedSeedID string
}

const ventColumns = "id, profile_id, session_id, turn, text, context, created_at, withdrawn_at, dismissed_at, planted_seed_id"

func ventTime(t time.Time) string { return t.UTC().Format(analyticsTimeLayout) }

func (s *Store) AddVent(v Vent) error {
	context, err := json.Marshal(v.Context)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("vents need the SQLite store")
	}
	var taken string
	err = s.db.QueryRow(`INSERT INTO vents (id, profile_id, session_id, turn, text, context, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (session_id, turn) WHERE withdrawn_at IS NULL DO UPDATE SET id = id RETURNING id`,
		v.ID, v.ProfileID, v.SessionID, v.Turn, v.Text, string(context), ventTime(v.CreatedAt)).Scan(&taken)
	if err != nil || taken == v.ID {
		return err
	}
	return &VentTurnTakenError{VentID: taken}
}

func (s *Store) WithdrawVent(sessionID protocol.SessionID, id string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, nil
	}
	result, err := s.db.Exec("UPDATE vents SET withdrawn_at = ? WHERE id = ? AND session_id = ? AND withdrawn_at IS NULL", ventTime(at), id, sessionID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *Store) SetVentDismissed(id string, dismissed bool, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, nil
	}
	query, args := "UPDATE vents SET dismissed_at = NULL WHERE id = ? AND dismissed_at IS NOT NULL", []any{id}
	if dismissed {
		query, args = "UPDATE vents SET dismissed_at = ? WHERE id = ? AND dismissed_at IS NULL", []any{ventTime(at), id}
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *Store) SetVentPlanted(id, seedID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, nil
	}
	result, err := s.db.Exec("UPDATE vents SET planted_seed_id = ? WHERE id = ? AND planted_seed_id IS NULL AND withdrawn_at IS NULL", seedID, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *Store) Vent(id string) (Vent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return Vent{}, ErrVentNotFound
	}
	v, err := scanVent(s.db.QueryRow("SELECT "+ventColumns+" FROM vents WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Vent{}, ErrVentNotFound
	}
	return v, err
}

func (s *Store) Vents(profileID string) ([]Vent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query("SELECT "+ventColumns+" FROM vents WHERE profile_id = ? ORDER BY created_at DESC, id DESC", profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var vents []Vent
	for rows.Next() {
		v, err := scanVent(rows)
		if err != nil {
			return nil, err
		}
		vents = append(vents, v)
	}
	return vents, rows.Err()
}

func (s *Store) PruneVents(before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return 0, nil
	}
	cutoff := ventTime(before)
	result, err := s.db.Exec("DELETE FROM vents WHERE withdrawn_at < ? OR dismissed_at < ?", cutoff, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func scanVent(row interface{ Scan(...any) error }) (Vent, error) {
	var (
		v                    Vent
		context, created     string
		withdrawn, dismissed sql.NullString
		planted              sql.NullString
	)
	if err := row.Scan(&v.ID, &v.ProfileID, &v.SessionID, &v.Turn, &v.Text, &context, &created, &withdrawn, &dismissed, &planted); err != nil {
		return Vent{}, err
	}
	if err := json.Unmarshal([]byte(context), &v.Context); err != nil {
		return Vent{}, fmt.Errorf("vent %s context: %w", v.ID, err)
	}
	v.CreatedAt = parseStoreTime(created)
	v.WithdrawnAt = parseStoreTime(withdrawn.String)
	v.DismissedAt = parseStoreTime(dismissed.String)
	v.PlantedSeedID = planted.String
	return v, nil
}
