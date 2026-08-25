package store

import (
	"fmt"
	"strings"
	"time"
)

const ticketMemberIdentityPrefix = "member:"

func TicketMemberIdentity(memberID string) string {
	memberID = strings.TrimSpace(memberID)
	if memberID == "" {
		return ""
	}
	return ticketMemberIdentityPrefix + memberID
}

func ParseTicketMemberIdentity(identity string) (string, bool) {
	identity = strings.TrimSpace(identity)
	memberID, ok := strings.CutPrefix(identity, ticketMemberIdentityPrefix)
	if !ok || memberID == "" || memberID != strings.TrimSpace(memberID) {
		return "", false
	}
	return memberID, true
}

func (s *Store) MigrateTicketIdentity(from, to string, now time.Time) error {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return fmt.Errorf("ticket identity migration requires non-empty source and target identities")
	}
	if from == to {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin ticket identity migration: %w", err)
	}
	defer tx.Rollback()

	stamp := formatTicketTime(now)
	if _, err := tx.Exec(`
		INSERT INTO ticket_subscriptions (identity, ticket_id, created_at)
		SELECT ?, ticket_id, ? FROM ticket_participants WHERE identity = ?
		ON CONFLICT(identity, ticket_id) DO NOTHING
	`, to, stamp, from); err != nil {
		return fmt.Errorf("carry ticket participation: %w", err)
	}
	if _, err := tx.Exec(`UPDATE ticket_events SET author = ? WHERE author = ?`, to, from); err != nil {
		return fmt.Errorf("carry ticket event authorship: %w", err)
	}
	if _, err := tx.Exec(`UPDATE ticket_activity SET author = ? WHERE author = ?`, to, from); err != nil {
		return fmt.Errorf("carry ticket activity authorship: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO ticket_event_cursors (identity, ticket_id, cursor, updated_at)
		SELECT ?, ticket_id, cursor, ? FROM ticket_event_cursors WHERE identity = ?
		ON CONFLICT(identity, ticket_id) DO UPDATE SET
			cursor = MAX(ticket_event_cursors.cursor, excluded.cursor),
			updated_at = MAX(ticket_event_cursors.updated_at, excluded.updated_at)
	`, to, stamp, from); err != nil {
		return fmt.Errorf("carry ticket cursors: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO ticket_delivery_attention (observer_key, last_attention_at, delivered_through_seq)
		SELECT ?, last_attention_at, delivered_through_seq FROM ticket_delivery_attention WHERE observer_key = ?
		ON CONFLICT(observer_key) DO UPDATE SET
			last_attention_at = MAX(ticket_delivery_attention.last_attention_at, excluded.last_attention_at),
			delivered_through_seq = MAX(ticket_delivery_attention.delivered_through_seq, excluded.delivered_through_seq)
	`, to, from); err != nil {
		return fmt.Errorf("carry ticket delivery attention: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM ticket_subscriptions WHERE identity = ?`, from); err != nil {
		return fmt.Errorf("remove source ticket subscriptions: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM ticket_event_cursors WHERE identity = ?`, from); err != nil {
		return fmt.Errorf("remove source ticket cursors: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM ticket_delivery_attention WHERE observer_key = ?`, from); err != nil {
		return fmt.Errorf("remove source ticket delivery attention: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ticket identity migration: %w", err)
	}
	return nil
}
