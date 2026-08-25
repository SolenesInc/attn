package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) AddTicketSubscription(identity, ticketID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM tickets WHERE id = ?`, ticketID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %q", ErrTicketNotFound, ticketID)
	}
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO ticket_subscriptions (identity, ticket_id, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT(identity, ticket_id) DO NOTHING
	`, identity, ticketID, formatTicketTime(now))
	return err
}

func (s *Store) RemoveTicketSubscription(identity, ticketID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(
		`DELETE FROM ticket_subscriptions WHERE identity = ? AND ticket_id = ?`,
		identity, ticketID,
	)
	return err
}

func (s *Store) IsTicketSubscribed(identity, ticketID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return false, nil
	}
	var one int
	err := s.db.QueryRow(
		`SELECT 1 FROM ticket_subscriptions WHERE identity = ? AND ticket_id = ?`,
		identity, ticketID,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
