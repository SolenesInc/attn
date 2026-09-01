package store

import (
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
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
