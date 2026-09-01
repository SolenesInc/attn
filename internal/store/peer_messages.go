package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
)

var (
	ErrPeerMessageNotFound    = errors.New("peer message not found")
	ErrPeerMessageNotNotified = errors.New("peer message has not been notified")
)

func (s *Store) EnqueuePeerMessage(message agentmailbox.PeerMessage, recipientSessionID string) (agentmailbox.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := agentmailbox.Item{
		ID: message.ID, RecipientSessionID: recipientSessionID,
		Kind: agentmailbox.KindPeerMessage, SourceID: message.ID, CreatedAt: message.CreatedAt,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return agentmailbox.Delivery{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO peer_messages (id, sender_session_id, body, created_at)
		VALUES (?, ?, ?, ?)
	`, message.ID, message.SenderSessionID, message.Body, message.CreatedAt); err != nil {
		return agentmailbox.Delivery{}, fmt.Errorf("enqueue peer message: %w", err)
	}
	if err := insertAgentMailboxItem(tx, item); err != nil {
		return agentmailbox.Delivery{}, err
	}
	if err := tx.Commit(); err != nil {
		return agentmailbox.Delivery{}, err
	}
	return agentmailbox.Delivery{Item: item, Peer: &message}, nil
}

func (s *Store) DeleteQueuedPeerMessage(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`
		DELETE FROM agent_mailbox_items
		WHERE id = ? AND kind = ? AND notified_at = ''
	`, id, agentmailbox.KindPeerMessage)
	if err != nil {
		return fmt.Errorf("delete queued peer mailbox item: %w", err)
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if removed > 0 {
		if _, err := tx.Exec(`DELETE FROM peer_messages WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete queued peer message: %w", err)
		}
	}
	return tx.Commit()
}

type peerRecordScanner interface {
	Scan(dest ...any) error
}

func scanPeerRecord(row peerRecordScanner) (agentmailbox.PeerRecord, error) {
	var record agentmailbox.PeerRecord
	err := row.Scan(
		&record.Message.ID, &record.Message.SenderSessionID, &record.Message.Body,
		&record.Message.CreatedAt, &record.RecipientSessionID,
		&record.NotifiedAt, &record.ReadAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return agentmailbox.PeerRecord{}, ErrPeerMessageNotFound
	}
	return record, err
}

func peerMessageRecord(queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}, id string) (agentmailbox.PeerRecord, error) {
	record, err := scanPeerRecord(queryer.QueryRow(`
		SELECT p.id, p.sender_session_id, p.body, p.created_at,
		       i.recipient_session_id, i.notified_at, i.read_at
		FROM peer_messages p
		JOIN agent_mailbox_items i ON i.kind = ? AND i.source_id = p.id
		WHERE p.id = ?
	`, agentmailbox.KindPeerMessage, id))
	if err != nil && !errors.Is(err, ErrPeerMessageNotFound) {
		return agentmailbox.PeerRecord{}, fmt.Errorf("read peer message: %w", err)
	}
	return record, err
}

func (s *Store) PeerMessageRecord(id string) (agentmailbox.PeerRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return peerMessageRecord(s.db, id)
}

func (s *Store) ReadPeerMessage(id, recipientSessionID string, at time.Time) (agentmailbox.PeerRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return agentmailbox.PeerRecord{}, false, err
	}
	defer tx.Rollback()
	record, err := peerMessageRecord(tx, id)
	if err != nil {
		return agentmailbox.PeerRecord{}, false, err
	}
	if record.RecipientSessionID != recipientSessionID {
		return agentmailbox.PeerRecord{}, false, ErrPeerMessageNotFound
	}
	if record.NotifiedAt == "" {
		return agentmailbox.PeerRecord{}, false, ErrPeerMessageNotNotified
	}
	if record.ReadAt != "" {
		if err := tx.Commit(); err != nil {
			return agentmailbox.PeerRecord{}, false, err
		}
		return record, false, nil
	}
	stamp := at.UTC().Format(sortableTimeFormat)
	res, err := tx.Exec(`
		UPDATE agent_mailbox_items SET read_at = ?
		WHERE id = ? AND kind = ? AND notified_at != '' AND read_at = ''
	`, stamp, id, agentmailbox.KindPeerMessage)
	if err != nil {
		return agentmailbox.PeerRecord{}, false, fmt.Errorf("read peer message: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return agentmailbox.PeerRecord{}, false, err
	}
	if changed == 0 {
		return agentmailbox.PeerRecord{}, false, ErrPeerMessageNotNotified
	}
	record.ReadAt = stamp
	if err := tx.Commit(); err != nil {
		return agentmailbox.PeerRecord{}, false, err
	}
	return record, true, nil
}

func (s *Store) PeerMessageGuardCounts(sender, recipient, body string, dedupeSince, rateSince time.Time) (agentmailbox.PeerGuardCounts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var counts agentmailbox.PeerGuardCounts
	err := s.db.QueryRow(`
		SELECT
			EXISTS (
				SELECT 1 FROM peer_messages p
				JOIN agent_mailbox_items i ON i.kind = ? AND i.source_id = p.id
				WHERE p.sender_session_id = ? AND i.recipient_session_id = ?
					AND p.body = ? AND p.created_at >= ?
			),
			(
				SELECT COUNT(*) FROM peer_messages p
				JOIN agent_mailbox_items i ON i.kind = ? AND i.source_id = p.id
				WHERE p.sender_session_id = ? AND i.recipient_session_id = ? AND p.created_at >= ?
			),
			(
				SELECT COUNT(*) FROM agent_mailbox_items
				WHERE recipient_session_id = ? AND kind = ? AND read_at = ''
			)
	`,
		agentmailbox.KindPeerMessage, sender, recipient, body, dedupeSince.UTC().Format(sortableTimeFormat),
		agentmailbox.KindPeerMessage, sender, recipient, rateSince.UTC().Format(sortableTimeFormat),
		recipient, agentmailbox.KindPeerMessage,
	).Scan(&counts.DuplicateFromSender, &counts.FromSenderInWindow, &counts.UnreadForRecipient)
	if err != nil {
		return agentmailbox.PeerGuardCounts{}, fmt.Errorf("read peer message guard counts: %w", err)
	}
	return counts, nil
}
