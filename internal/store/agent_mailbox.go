package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
)

func (s *Store) EnqueueMaintenancePrompt(item agentmailbox.Item) (agentmailbox.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item.Kind = agentmailbox.KindMaintenancePrompt
	item.SourceID = ""
	item.CoalesceKey = ""
	if err := insertAgentMailboxItem(s.db, item); err != nil {
		return agentmailbox.Delivery{}, err
	}
	return agentmailbox.Delivery{Item: item}, nil
}

type mailboxItemExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func insertAgentMailboxItem(exec mailboxItemExecer, item agentmailbox.Item) error {
	_, err := exec.Exec(`
		INSERT INTO agent_mailbox_items
			(id, recipient_session_id, kind, source_id, coalesce_key, hint, prompt, created_at, notified_at, read_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, item.ID, item.RecipientSessionID, string(item.Kind), item.SourceID, item.CoalesceKey,
		item.Hint, item.Prompt, item.CreatedAt, item.NotifiedAt, item.ReadAt)
	if err != nil {
		return fmt.Errorf("enqueue agent mailbox item: %w", err)
	}
	return nil
}

func (s *Store) QueuedAgentMailboxDeliveries(recipientSessionID string) ([]agentmailbox.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT i.id, i.recipient_session_id, i.kind, i.source_id, i.coalesce_key,
		       i.hint, i.prompt, i.created_at, i.notified_at, i.read_at,
		       COALESCE(p.id, ''), COALESCE(p.sender_session_id, ''),
		       COALESCE(p.body, ''), COALESCE(p.created_at, '')
		FROM agent_mailbox_items i
		LEFT JOIN peer_messages p ON i.kind = ? AND p.id = i.source_id
		WHERE i.recipient_session_id = ? AND i.notified_at = ''
		ORDER BY i.created_at, i.id
	`, agentmailbox.KindPeerMessage, recipientSessionID)
	if err != nil {
		return nil, fmt.Errorf("list queued agent mailbox items: %w", err)
	}
	defer rows.Close()

	deliveries := []agentmailbox.Delivery{}
	for rows.Next() {
		var (
			delivery                     agentmailbox.Delivery
			kind                         string
			peerID, peerSender, body, at string
		)
		if err := rows.Scan(
			&delivery.Item.ID, &delivery.Item.RecipientSessionID, &kind,
			&delivery.Item.SourceID, &delivery.Item.CoalesceKey, &delivery.Item.Hint,
			&delivery.Item.Prompt, &delivery.Item.CreatedAt, &delivery.Item.NotifiedAt,
			&delivery.Item.ReadAt, &peerID, &peerSender, &body, &at,
		); err != nil {
			return nil, fmt.Errorf("scan queued agent mailbox item: %w", err)
		}
		delivery.Item.Kind = agentmailbox.Kind(kind)
		if delivery.Item.Kind == agentmailbox.KindPeerMessage {
			if peerID == "" {
				return nil, fmt.Errorf("peer mailbox item %s has no peer message", delivery.Item.ID)
			}
			delivery.Peer = &agentmailbox.PeerMessage{
				ID: peerID, SenderSessionID: peerSender, Body: body, CreatedAt: at,
			}
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

func (s *Store) AgentMailboxItemQueued(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var queued bool
	if err := s.db.QueryRow(`
		SELECT EXISTS (SELECT 1 FROM agent_mailbox_items WHERE id = ? AND notified_at = '')
	`, id).Scan(&queued); err != nil {
		return false, fmt.Errorf("check queued agent mailbox item: %w", err)
	}
	return queued, nil
}

func (s *Store) TargetsWithQueuedAgentMailboxItems() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT DISTINCT recipient_session_id FROM agent_mailbox_items WHERE notified_at = ''
	`)
	if err != nil {
		return nil, fmt.Errorf("list queued agent mailbox recipients: %w", err)
	}
	defer rows.Close()

	var recipients []string
	for rows.Next() {
		var recipient string
		if err := rows.Scan(&recipient); err != nil {
			return nil, fmt.Errorf("scan queued agent mailbox recipient: %w", err)
		}
		recipients = append(recipients, recipient)
	}
	return recipients, rows.Err()
}

func (s *Store) MarkAgentMailboxItemNotified(id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		UPDATE agent_mailbox_items SET notified_at = ?
		WHERE id = ? AND notified_at = ''
	`, at.UTC().Format(sortableTimeFormat), id)
	if err != nil {
		return fmt.Errorf("mark agent mailbox item notified: %w", err)
	}
	return nil
}

func (s *Store) MarkAgentMailboxItemHandled(id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stamp := at.UTC().Format(sortableTimeFormat)
	_, err := s.db.Exec(`
		UPDATE agent_mailbox_items
		SET notified_at = CASE WHEN notified_at = '' THEN ? ELSE notified_at END,
		    read_at = CASE WHEN read_at = '' THEN ? ELSE read_at END
		WHERE id = ?
	`, stamp, stamp, id)
	if err != nil {
		return fmt.Errorf("mark agent mailbox item handled: %w", err)
	}
	return nil
}

func (s *Store) ReadGardenSeedMailboxItems(recipientSessionID, seedID string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stamp := at.UTC().Format(sortableTimeFormat)
	res, err := s.db.Exec(`
		UPDATE agent_mailbox_items
		SET notified_at = CASE WHEN notified_at = '' THEN ? ELSE notified_at END,
		    read_at = ?
		WHERE recipient_session_id = ? AND kind = ? AND source_id = ? AND read_at = ''
	`, stamp, stamp, recipientSessionID, agentmailbox.KindGardenSeed, seedID)
	if err != nil {
		return false, fmt.Errorf("read Garden seed mailbox item: %w", err)
	}
	changed, err := res.RowsAffected()
	return changed > 0, err
}
