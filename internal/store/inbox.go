package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/inbox"
)

func (s *Store) CommitDocumentWriteWithInbox(write DocumentWrite, fact BusEvent, item inbox.Item, at time.Time) (DocumentWriteResult, error) {
	table, err := s.documentTable(write.Schema)
	if err != nil {
		return DocumentWriteResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return DocumentWriteResult{}, err
	}
	defer tx.Rollback()
	results, _, err := commitDocumentWritesWith(tx, []DocumentCommit{{Write: write, Fact: fact}}, []string{table}, at)
	if err != nil {
		return DocumentWriteResult{}, err
	}
	if err := putInbox(tx, item, at); err != nil {
		return DocumentWriteResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return DocumentWriteResult{}, err
	}
	return results[0], nil
}
func (s *Store) InboxItem(id string) (inbox.Item, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return inboxItemByID(s.db, id)
}

type inboxItemQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

func scanInboxItem(row *sql.Row) (inbox.Item, bool, error) {
	var item inbox.Item
	var address string
	var kind string
	err := row.Scan(
		&item.ID, &address, &kind, &item.Source,
		&item.Key, &item.Hint, &item.Text, &item.CreatedAt,
		&item.NotifiedAt, &item.ReadAt,
	)
	if err == sql.ErrNoRows {
		return inbox.Item{}, false, nil
	}
	if err != nil {
		return inbox.Item{}, false, err
	}
	item.Kind = inbox.Kind(kind)
	item.To, err = inbox.ParseAddress(address)
	if err != nil {
		return inbox.Item{}, false, err
	}
	return item, true, nil
}

func inboxItemByID(queryer inboxItemQueryer, id string) (inbox.Item, bool, error) {
	return scanInboxItem(queryer.QueryRow(`
		SELECT id, address, kind, source_id, coalesce_key,
		       hint, text, created_at, notified_at, read_at
		FROM inbox_items WHERE id = ?
	`, id))
}

type inboxDeliveryScanner interface {
	Scan(dest ...any) error
}

func scanInboxDelivery(row inboxDeliveryScanner) (inbox.Delivery, error) {
	var (
		delivery                     inbox.Delivery
		kind                         string
		address                      string
		peerID, peerSender, body, at string
	)
	if err := row.Scan(
		&delivery.Item.ID, &address, &kind,
		&delivery.Item.Source, &delivery.Item.Key, &delivery.Item.Hint,
		&delivery.Item.Text, &delivery.Item.CreatedAt, &delivery.Item.NotifiedAt,
		&delivery.Item.ReadAt, &peerID, &peerSender, &body, &at,
	); err != nil {
		return inbox.Delivery{}, err
	}
	delivery.Item.Kind = inbox.Kind(kind)
	var err error
	delivery.Item.To, err = inbox.ParseAddress(address)
	if err != nil {
		return inbox.Delivery{}, err
	}
	if delivery.Item.Kind == inbox.PeerMessage {
		if peerID == "" {
			return inbox.Delivery{}, fmt.Errorf("peer mailbox item %s has no peer message", delivery.Item.ID)
		}
		delivery.Peer = &inbox.Message{
			ID: peerID, SenderSessionID: peerSender, Body: body, CreatedAt: at,
		}
	}
	return delivery, nil
}

func normalizeAgentInboxLimit(limit int) int {
	if limit <= 0 {
		return inbox.DefaultInboxLimit
	}
	if limit > inbox.MaxInboxLimit {
		return inbox.MaxInboxLimit
	}
	return limit
}

func (s *Store) UnreadInboxDeliveries(to inbox.Address) ([]inbox.Delivery, error) {
	recipientSessionID := to.String()
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT i.id, i.address, i.kind, i.source_id, i.coalesce_key,
		       i.hint, i.text, i.created_at, i.notified_at, i.read_at,
		       COALESCE(p.id, ''), COALESCE(p.sender_session_id, ''),
		       COALESCE(p.body, ''), COALESCE(p.created_at, '')
		FROM inbox_items i
		LEFT JOIN peer_messages p ON i.kind = ? AND p.id = i.source_id
		WHERE i.address = ? AND i.read_at = ''
		ORDER BY i.created_at, i.id
	`, inbox.PeerMessage, recipientSessionID)
	if err != nil {
		return nil, fmt.Errorf("list unread inbox items: %w", err)
	}
	defer rows.Close()

	deliveries := []inbox.Delivery{}
	for rows.Next() {
		delivery, err := scanInboxDelivery(rows)
		if err != nil {
			return nil, fmt.Errorf("scan unread inbox item: %w", err)
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

func (s *Store) ReadInbox(addresses []inbox.Address, readBy string, limit int, at time.Time) ([]inbox.Delivery, int, error) {
	addressJSON := inboxAddressJSON(addresses)
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`
		SELECT i.id, i.address, i.kind, i.source_id, i.coalesce_key,
		       i.hint, i.text, i.created_at, i.notified_at, i.read_at,
		       COALESCE(p.id, ''), COALESCE(p.sender_session_id, ''),
		       COALESCE(p.body, ''), COALESCE(p.created_at, '')
		FROM inbox_items i
		LEFT JOIN peer_messages p ON i.kind = ? AND p.id = i.source_id
		WHERE i.address IN (SELECT value FROM json_each(?)) AND i.read_at = ''
		ORDER BY i.created_at, i.id
		LIMIT ?
	`, inbox.PeerMessage, addressJSON, normalizeAgentInboxLimit(limit))
	if err != nil {
		return nil, 0, fmt.Errorf("list unread inbox items: %w", err)
	}
	deliveries := []inbox.Delivery{}
	for rows.Next() {
		delivery, scanErr := scanInboxDelivery(rows)
		if scanErr != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("scan unread inbox item: %w", scanErr)
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, fmt.Errorf("list unread inbox items: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("close unread inbox items: %w", err)
	}

	stamp := at.UTC().Format(sortableTimeFormat)
	for i := range deliveries {
		res, err := tx.Exec(`
			UPDATE inbox_items
			SET notified_at = CASE WHEN notified_at = '' THEN ? ELSE notified_at END,
			    read_at = ?, read_by = ?
			WHERE id = ? AND address IN (SELECT value FROM json_each(?)) AND read_at = ''
		`, stamp, stamp, readBy, deliveries[i].Item.ID, addressJSON)
		if err != nil {
			return nil, 0, fmt.Errorf("read inbox item %s: %w", deliveries[i].Item.ID, err)
		}
		changed, err := res.RowsAffected()
		if err != nil {
			return nil, 0, err
		}
		if changed != 1 {
			return nil, 0, fmt.Errorf("read inbox item %s: receipt was not written", deliveries[i].Item.ID)
		}
		if deliveries[i].Item.NotifiedAt == "" {
			deliveries[i].Item.NotifiedAt = stamp
		}
		deliveries[i].Item.ReadAt = stamp
	}

	var remaining int
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM inbox_items
		WHERE address IN (SELECT value FROM json_each(?)) AND read_at = ''
	`, addressJSON).Scan(&remaining); err != nil {
		return nil, 0, fmt.Errorf("count unread inbox items: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return deliveries, remaining, nil
}

func (s *Store) ReadGardenSeedInboxItems(to inbox.Address, readBy, seedID string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stamp := at.UTC().Format(sortableTimeFormat)
	result, err := s.db.Exec(`UPDATE inbox_items SET notified_at=CASE WHEN notified_at='' THEN ? ELSE notified_at END, read_at=?, read_by=? WHERE address=? AND kind=? AND coalesce_key=? AND read_at=''`, stamp, stamp, readBy, to.String(), inbox.SeedUpdate, seedID)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

func inboxAddressJSON(addresses []inbox.Address) string {
	values := make([]string, len(addresses))
	for i, a := range addresses {
		values[i] = a.String()
	}
	data, _ := json.Marshal(values)
	return string(data)
}

func (s *Store) PutInbox(item inbox.Item, at time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := putInbox(tx, item, at); err != nil {
		return "", err
	}
	return item.ID, tx.Commit()
}
func putInbox(tx *sql.Tx, item inbox.Item, at time.Time) error {
	if _, err := inbox.ParseAddress(item.To.String()); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM inbox_items WHERE id=?)", item.ID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	var last string
	// Replacements get a fresh budget while preserving the outstanding ring.
	if item.Key != "" {
		if err := tx.QueryRow("SELECT COALESCE(MAX(attempted_at),'') FROM inbox_items WHERE address=? AND kind=? AND coalesce_key=? AND read_at=''", item.To.String(), item.Kind, item.Key).Scan(&last); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM inbox_items WHERE address=? AND kind=? AND coalesce_key=? AND read_at=''", item.To.String(), item.Kind, item.Key); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`INSERT INTO inbox_items(id,address,kind,source_id,coalesce_key,hint,text,bell_name,created_at,attempted_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, item.ID, item.To.String(), item.Kind, item.Source, item.Key, item.Hint, item.Text, item.BellName, at.UTC().Format(sortableTimeFormat), last)
	return err
}
func (s *Store) WithdrawInbox(to inbox.Address, kind inbox.Kind, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM inbox_items WHERE address=? AND kind=? AND coalesce_key=? AND read_at=''", to.String(), kind, key)
	return err
}

type InboxAttempt struct {
	LiveIDs    []string
	Unread     int
	Last, Read time.Time
}

func (s *Store) InboxAttempt(to inbox.Address) (InboxAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result InboxAttempt
	var last, read string
	if err := s.db.QueryRow(`SELECT
 (SELECT COALESCE(MAX(attempted_at),'') FROM inbox_items WHERE address=?),
 (SELECT COALESCE(MAX(read_at),'') FROM inbox_items WHERE address=?)`, to.String(), to.String()).Scan(&last, &read); err != nil {
		return result, err
	}
	result.Last, _ = time.Parse(time.RFC3339Nano, last)
	result.Read, _ = time.Parse(time.RFC3339Nano, read)
	rows, err := s.db.Query("SELECT id,attempts FROM inbox_items WHERE address=? AND read_at=''", to.String())
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var attempts int
		if err := rows.Scan(&id, &attempts); err != nil {
			return result, err
		}
		result.Unread++
		if attempts < inbox.MaxAttempts {
			result.LiveIDs = append(result.LiveIDs, id)
		}
	}
	return result, rows.Err()
}
func (s *Store) StampInboxAttempt(ids []string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	encoded, _ := json.Marshal(ids)
	_, err := s.db.Exec(`UPDATE inbox_items SET attempts=attempts+1,attempted_at=? WHERE id IN (SELECT value FROM json_each(?)) AND read_at='' AND attempts<?`, at.UTC().Format(sortableTimeFormat), string(encoded), inbox.MaxAttempts)
	return err
}
func (s *Store) RingInbox(to inbox.Address, wakeIDs []string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stamp := at.UTC().Format(sortableTimeFormat)
	if wakeIDs == nil {
		wakeIDs = []string{}
	}
	encoded, _ := json.Marshal(wakeIDs)
	// A ring covers the unread batch at commit, including a concurrent replacement.
	_, err := s.db.Exec(`UPDATE inbox_items SET
 attempts=attempts+CASE WHEN attempts<? AND id NOT IN (SELECT value FROM json_each(?)) THEN 1 ELSE 0 END,
 attempted_at=?, notified_at=CASE WHEN notified_at='' THEN ? ELSE notified_at END
 WHERE address=? AND read_at=''`, inbox.MaxAttempts, string(encoded), stamp, stamp, to.String())
	return err
}
func (s *Store) UnreadInboxAddresses() ([]inbox.Address, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT DISTINCT address FROM inbox_items WHERE read_at='' ORDER BY address")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []inbox.Address
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		a, err := inbox.ParseAddress(value)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
