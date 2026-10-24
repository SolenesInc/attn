package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/protocol"
)

var (
	ErrVentNotFound = errors.New("no such vent")
	ErrVentSent     = errors.New("the vent was already sent")
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
	Queued        bool
	SentAt        time.Time
}

const ventColumns = "id, profile_id, session_id, turn, text, context, created_at, withdrawn_at, dismissed_at, planted_seed_id, sent_at, " +
	"EXISTS (SELECT 1 FROM analytics_outbox WHERE stream = 'vent' AND seq = vents.outbox_seq AND sent_at IS NULL)"

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
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE vents SET withdrawn_at = ? WHERE id = ? AND session_id = ? AND withdrawn_at IS NULL", ventTime(at), id, sessionID)
	if err != nil {
		return false, err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if _, err := tx.Exec("DELETE FROM analytics_outbox WHERE stream = ? AND sent_at IS NULL AND seq = (SELECT outbox_seq FROM vents WHERE id = ?)", analytics.StreamVent, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
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

func (s *Store) ClearVentPlanted(id, seedID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, nil
	}
	result, err := s.db.Exec("UPDATE vents SET planted_seed_id = NULL WHERE id = ? AND planted_seed_id = ?", id, seedID)
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
	return scanVents(rows)
}

func (s *Store) PruneVents(before time.Time) ([]Vent, error) {
	cutoff := ventTime(before)
	return s.deleteVents("withdrawn_at < ? OR dismissed_at < ?", cutoff, cutoff)
}

func (s *Store) DeleteDismissedVents(profileID string) ([]Vent, error) {
	return s.deleteVents("profile_id = ? AND dismissed_at IS NOT NULL", profileID)
}

func (s *Store) deleteVents(where string, args ...any) ([]Vent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query("DELETE FROM vents WHERE "+where+" RETURNING "+ventColumns, args...)
	if err != nil {
		return nil, err
	}
	return scanVents(rows)
}

func scanVents(rows *sql.Rows) ([]Vent, error) {
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

func (s *Store) QueueVentRecord(id string, record analytics.Record) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, fmt.Errorf("vents need the SQLite store")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var sent sql.NullString
	switch err := tx.QueryRow(`SELECT sent_at FROM vents WHERE id = ? AND withdrawn_at IS NULL AND NOT EXISTS (
		SELECT 1 FROM analytics_outbox WHERE stream = ? AND seq = vents.outbox_seq AND sent_at IS NULL)`, id, analytics.StreamVent).Scan(&sent); {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	case sent.Valid:
		return false, ErrVentSent
	}
	seq, err := appendStreamRecord(tx, record)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec("UPDATE vents SET outbox_seq = ? WHERE id = ?", seq, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) UnqueueVentRecord(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var sent sql.NullString
	switch err := tx.QueryRow("SELECT sent_at FROM vents WHERE id = ?", id).Scan(&sent); {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	case sent.Valid:
		return false, ErrVentSent
	}
	result, err := tx.Exec("DELETE FROM analytics_outbox WHERE stream = ? AND sent_at IS NULL AND seq = (SELECT outbox_seq FROM vents WHERE id = ?)", analytics.StreamVent, id)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec("UPDATE vents SET outbox_seq = NULL WHERE id = ?", id); err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, tx.Commit()
}

func scanVent(row interface{ Scan(...any) error }) (Vent, error) {
	var (
		v                    Vent
		context, created     string
		withdrawn, dismissed sql.NullString
		planted, sent        sql.NullString
	)
	if err := row.Scan(&v.ID, &v.ProfileID, &v.SessionID, &v.Turn, &v.Text, &context, &created, &withdrawn, &dismissed, &planted, &sent, &v.Queued); err != nil {
		return Vent{}, err
	}
	if err := json.Unmarshal([]byte(context), &v.Context); err != nil {
		return Vent{}, fmt.Errorf("vent %s context: %w", v.ID, err)
	}
	v.CreatedAt = parseStoreTime(created)
	v.WithdrawnAt = parseStoreTime(withdrawn.String)
	v.DismissedAt = parseStoreTime(dismissed.String)
	v.PlantedSeedID = planted.String
	v.SentAt = parseStoreTime(sent.String)
	return v, nil
}
