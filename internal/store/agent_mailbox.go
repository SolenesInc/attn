package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
)

func (s *Store) EnqueueMaintenancePrompt(id, recipientSessionID, prompt string, at time.Time) (agentmailbox.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := agentmailbox.Item{
		ID: id, RecipientSessionID: recipientSessionID,
		Kind: agentmailbox.KindMaintenancePrompt, Prompt: prompt,
		CreatedAt: at.UTC().Format(sortableTimeFormat),
	}
	if err := insertAgentMailboxItem(s.db, item); err != nil {
		return agentmailbox.Delivery{}, err
	}
	return agentmailbox.Delivery{Item: item}, nil
}

func (s *Store) EnqueueMaintenancePromptOnce(
	id, recipientSessionID, sourceID, coalesceKey, prompt string,
	at time.Time,
) (agentmailbox.Delivery, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := agentmailbox.Item{
		ID: id, RecipientSessionID: recipientSessionID,
		Kind: agentmailbox.KindMaintenancePrompt, SourceID: sourceID,
		CoalesceKey: coalesceKey, Prompt: prompt,
		CreatedAt: at.UTC().Format(sortableTimeFormat),
	}
	tx, err := s.db.Begin()
	if err != nil {
		return agentmailbox.Delivery{}, false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO agent_mailbox_items
			(id, recipient_session_id, kind, source_id, coalesce_key, prompt, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, item.ID, item.RecipientSessionID, item.Kind, item.SourceID, item.CoalesceKey,
		item.Prompt, item.CreatedAt)
	if err != nil {
		return agentmailbox.Delivery{}, false, fmt.Errorf("enqueue maintenance mailbox item: %w", err)
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return agentmailbox.Delivery{}, false, err
	}
	if inserted == 1 {
		if err := tx.Commit(); err != nil {
			return agentmailbox.Delivery{}, false, err
		}
		return agentmailbox.Delivery{Item: item}, true, nil
	}

	existing, found, err := maintenanceMailboxItemByID(tx, id)
	if err != nil {
		return agentmailbox.Delivery{}, false, err
	}
	if !found && coalesceKey != "" {
		existing, found, err = unreadMaintenanceMailboxItemByCoalesceKey(tx, recipientSessionID, coalesceKey)
		if err != nil {
			return agentmailbox.Delivery{}, false, err
		}
	}
	if !found {
		return agentmailbox.Delivery{}, false, fmt.Errorf("enqueue maintenance mailbox item %s: ignored without an existing item", id)
	}
	if existing.Kind != agentmailbox.KindMaintenancePrompt ||
		existing.RecipientSessionID != recipientSessionID || existing.CoalesceKey != coalesceKey {
		return agentmailbox.Delivery{}, false, fmt.Errorf("enqueue maintenance mailbox item %s: id already belongs to another item", id)
	}
	if existing.ReadAt == "" && coalesceKey != "" {
		if _, err := tx.Exec(`
			UPDATE agent_mailbox_items SET source_id = ?, prompt = ?
			WHERE id = ? AND read_at = ''
		`, sourceID, prompt, existing.ID); err != nil {
			return agentmailbox.Delivery{}, false, fmt.Errorf("refresh coalesced maintenance mailbox item: %w", err)
		}
		existing.SourceID = sourceID
		existing.Prompt = prompt
	}
	if err := tx.Commit(); err != nil {
		return agentmailbox.Delivery{}, false, err
	}
	return agentmailbox.Delivery{Item: existing}, false, nil
}

type mailboxItemQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

func scanMailboxItem(row *sql.Row) (agentmailbox.Item, bool, error) {
	var item agentmailbox.Item
	var kind string
	err := row.Scan(
		&item.ID, &item.RecipientSessionID, &kind, &item.SourceID,
		&item.CoalesceKey, &item.Hint, &item.Prompt, &item.CreatedAt,
		&item.NotifiedAt, &item.ReadAt,
	)
	if err == sql.ErrNoRows {
		return agentmailbox.Item{}, false, nil
	}
	if err != nil {
		return agentmailbox.Item{}, false, err
	}
	item.Kind = agentmailbox.Kind(kind)
	return item, true, nil
}

func maintenanceMailboxItemByID(queryer mailboxItemQueryer, id string) (agentmailbox.Item, bool, error) {
	return scanMailboxItem(queryer.QueryRow(`
		SELECT id, recipient_session_id, kind, source_id, coalesce_key,
		       hint, prompt, created_at, notified_at, read_at
		FROM agent_mailbox_items WHERE id = ?
	`, id))
}

func unreadMaintenanceMailboxItemByCoalesceKey(
	queryer mailboxItemQueryer, recipientSessionID, coalesceKey string,
) (agentmailbox.Item, bool, error) {
	return scanMailboxItem(queryer.QueryRow(`
		SELECT id, recipient_session_id, kind, source_id, coalesce_key,
		       hint, prompt, created_at, notified_at, read_at
		FROM agent_mailbox_items
		WHERE recipient_session_id = ? AND kind = ? AND coalesce_key = ? AND read_at = ''
	`, recipientSessionID, agentmailbox.KindMaintenancePrompt, coalesceKey))
}

type mailboxItemExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func insertAgentMailboxItem(exec mailboxItemExecer, item agentmailbox.Item) error {
	_, err := exec.Exec(`
		INSERT INTO agent_mailbox_items
			(id, recipient_session_id, kind, source_id, coalesce_key, hint, prompt, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, item.ID, item.RecipientSessionID, string(item.Kind), item.SourceID, item.CoalesceKey,
		item.Hint, item.Prompt, item.CreatedAt)
	if err != nil {
		return fmt.Errorf("enqueue agent mailbox item: %w", err)
	}
	return nil
}

type mailboxDeliveryScanner interface {
	Scan(dest ...any) error
}

func scanAgentMailboxDelivery(row mailboxDeliveryScanner) (agentmailbox.Delivery, error) {
	var (
		delivery                     agentmailbox.Delivery
		kind                         string
		peerID, peerSender, body, at string
	)
	if err := row.Scan(
		&delivery.Item.ID, &delivery.Item.RecipientSessionID, &kind,
		&delivery.Item.SourceID, &delivery.Item.CoalesceKey, &delivery.Item.Hint,
		&delivery.Item.Prompt, &delivery.Item.CreatedAt, &delivery.Item.NotifiedAt,
		&delivery.Item.ReadAt, &peerID, &peerSender, &body, &at,
	); err != nil {
		return agentmailbox.Delivery{}, err
	}
	delivery.Item.Kind = agentmailbox.Kind(kind)
	if delivery.Item.Kind == agentmailbox.KindPeerMessage {
		if peerID == "" {
			return agentmailbox.Delivery{}, fmt.Errorf("peer mailbox item %s has no peer message", delivery.Item.ID)
		}
		delivery.Peer = &agentmailbox.PeerMessage{
			ID: peerID, SenderSessionID: peerSender, Body: body, CreatedAt: at,
		}
	}
	return delivery, nil
}

func normalizeAgentInboxLimit(limit int) int {
	if limit <= 0 {
		return agentmailbox.DefaultInboxLimit
	}
	if limit > agentmailbox.MaxInboxLimit {
		return agentmailbox.MaxInboxLimit
	}
	return limit
}

func (s *Store) UnreadAgentMailboxDeliveries(recipientSessionID string) ([]agentmailbox.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT i.id, i.recipient_session_id, i.kind, i.source_id, i.coalesce_key,
		       i.hint, i.prompt, i.created_at, i.notified_at, i.read_at,
		       COALESCE(p.id, ''), COALESCE(p.sender_session_id, ''),
		       COALESCE(p.body, ''), COALESCE(p.created_at, '')
		FROM agent_mailbox_items i
		LEFT JOIN peer_messages p ON i.kind = ? AND p.id = i.source_id
		WHERE i.recipient_session_id = ? AND i.read_at = ''
		ORDER BY i.created_at, i.id
	`, agentmailbox.KindPeerMessage, recipientSessionID)
	if err != nil {
		return nil, fmt.Errorf("list unread agent mailbox items: %w", err)
	}
	defer rows.Close()

	deliveries := []agentmailbox.Delivery{}
	for rows.Next() {
		delivery, err := scanAgentMailboxDelivery(rows)
		if err != nil {
			return nil, fmt.Errorf("scan unread agent mailbox item: %w", err)
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

func (s *Store) ReadAgentMailbox(recipientSessionID string, limit int, at time.Time) ([]agentmailbox.Delivery, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`
		SELECT i.id, i.recipient_session_id, i.kind, i.source_id, i.coalesce_key,
		       i.hint, i.prompt, i.created_at, i.notified_at, i.read_at,
		       COALESCE(p.id, ''), COALESCE(p.sender_session_id, ''),
		       COALESCE(p.body, ''), COALESCE(p.created_at, '')
		FROM agent_mailbox_items i
		LEFT JOIN peer_messages p ON i.kind = ? AND p.id = i.source_id
		WHERE i.recipient_session_id = ? AND i.read_at = ''
		ORDER BY i.created_at, i.id
		LIMIT ?
	`, agentmailbox.KindPeerMessage, recipientSessionID, normalizeAgentInboxLimit(limit))
	if err != nil {
		return nil, 0, fmt.Errorf("list unread agent mailbox items: %w", err)
	}
	deliveries := []agentmailbox.Delivery{}
	for rows.Next() {
		delivery, scanErr := scanAgentMailboxDelivery(rows)
		if scanErr != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("scan unread agent mailbox item: %w", scanErr)
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, fmt.Errorf("list unread agent mailbox items: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("close unread agent mailbox items: %w", err)
	}

	stamp := at.UTC().Format(sortableTimeFormat)
	for i := range deliveries {
		res, err := tx.Exec(`
			UPDATE agent_mailbox_items
			SET notified_at = CASE WHEN notified_at = '' THEN ? ELSE notified_at END,
			    read_at = ?
			WHERE id = ? AND recipient_session_id = ? AND read_at = ''
		`, stamp, stamp, deliveries[i].Item.ID, recipientSessionID)
		if err != nil {
			return nil, 0, fmt.Errorf("read agent mailbox item %s: %w", deliveries[i].Item.ID, err)
		}
		changed, err := res.RowsAffected()
		if err != nil {
			return nil, 0, err
		}
		if changed != 1 {
			return nil, 0, fmt.Errorf("read agent mailbox item %s: receipt was not written", deliveries[i].Item.ID)
		}
		if deliveries[i].Item.NotifiedAt == "" {
			deliveries[i].Item.NotifiedAt = stamp
		}
		deliveries[i].Item.ReadAt = stamp
	}

	var remaining int
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM agent_mailbox_items
		WHERE recipient_session_id = ? AND read_at = ''
	`, recipientSessionID).Scan(&remaining); err != nil {
		return nil, 0, fmt.Errorf("count unread agent mailbox items: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return deliveries, remaining, nil
}

func (s *Store) MarkAgentMailboxNotified(recipientSessionID string, at time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`
		UPDATE agent_mailbox_items SET notified_at = ?
		WHERE recipient_session_id = ? AND read_at = '' AND notified_at = ''
	`, at.UTC().Format(sortableTimeFormat), recipientSessionID)
	if err != nil {
		return 0, fmt.Errorf("mark agent mailbox notified: %w", err)
	}
	return res.RowsAffected()
}

func (s *Store) HasUnreadAgentMailboxItems(recipientSessionID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var unread bool
	if err := s.db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM agent_mailbox_items
			WHERE recipient_session_id = ? AND read_at = ''
		)
	`, recipientSessionID).Scan(&unread); err != nil {
		return false, fmt.Errorf("check unread agent mailbox items: %w", err)
	}
	return unread, nil
}

func (s *Store) TargetsWithUnreadAgentMailboxItems() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT DISTINCT recipient_session_id
		FROM agent_mailbox_items
		WHERE read_at = ''
		ORDER BY recipient_session_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list unread agent mailbox recipients: %w", err)
	}
	defer rows.Close()

	var recipients []string
	for rows.Next() {
		var recipient string
		if err := rows.Scan(&recipient); err != nil {
			return nil, fmt.Errorf("scan unread agent mailbox recipient: %w", err)
		}
		recipients = append(recipients, recipient)
	}
	return recipients, rows.Err()
}

func (s *Store) ReadAgentMailboxItems(
	recipientSessionID string, kind agentmailbox.Kind, coalesceKey string, at time.Time,
) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	stamp := at.UTC().Format(sortableTimeFormat)
	res, err := tx.Exec(`
		UPDATE agent_mailbox_items
		SET notified_at = CASE WHEN notified_at = '' THEN ? ELSE notified_at END,
		    read_at = ?
		WHERE recipient_session_id = ? AND kind = ? AND coalesce_key = ? AND read_at = ''
	`, stamp, stamp, recipientSessionID, kind, coalesceKey)
	if err != nil {
		return 0, 0, fmt.Errorf("read agent mailbox items: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	var remaining int
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM agent_mailbox_items
		WHERE recipient_session_id = ? AND read_at = ''
	`, recipientSessionID).Scan(&remaining); err != nil {
		return 0, 0, fmt.Errorf("count unread agent mailbox items: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return int(changed), remaining, nil
}

func (s *Store) ReadGardenSeedMailboxItems(recipientSessionID, seedID string, at time.Time) (bool, int, error) {
	read, remaining, err := s.ReadAgentMailboxItems(
		recipientSessionID, agentmailbox.KindGardenSeed, seedID, at,
	)
	return read > 0, remaining, err
}
