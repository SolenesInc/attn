package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

var (
	ErrPeerMessageNotFound    = errors.New("peer message not found")
	ErrPeerMessageNotNotified = errors.New("peer message has not been notified")
)

type peerRecordScanner interface {
	Scan(dest ...any) error
}

func scanPeerRecord(row peerRecordScanner) (inbox.PeerRecord, error) {
	var record inbox.PeerRecord
	var address string
	err := row.Scan(
		&record.Message.ID, &record.Message.Sender, &record.Message.Body,
		&record.Message.CreatedAt, &address,
		&record.NotifiedAt, &record.ReadAt, &record.ReadBy,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return inbox.PeerRecord{}, ErrPeerMessageNotFound
	}
	if err == nil {
		record.To, err = who.ParseAddress(address)
	}
	return record, err
}

func peerMessageRecord(queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}, id string) (inbox.PeerRecord, error) {
	record, err := scanPeerRecord(queryer.QueryRow(`
		SELECT p.id, p.sender, p.body, p.created_at,
		       i.address, i.notified_at, i.read_at, i.read_by
		FROM peer_messages p
		JOIN inbox_items i ON i.kind = ? AND i.source_id = p.id
		WHERE p.id = ?
	`, inbox.PeerMessage, id))
	if err != nil && !errors.Is(err, ErrPeerMessageNotFound) {
		return inbox.PeerRecord{}, fmt.Errorf("read peer message: %w", err)
	}
	return record, err
}

func (s *Store) PeerMessageRecord(id string) (inbox.PeerRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return peerMessageRecord(s.db, id)
}

func (s *Store) ReadPeerMessage(id string, readBy protocol.SessionID, addresses []who.Address, at time.Time) (inbox.PeerRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return inbox.PeerRecord{}, false, err
	}
	defer tx.Rollback()
	record, err := peerMessageRecord(tx, id)
	if err != nil {
		return inbox.PeerRecord{}, false, err
	}
	allowed := false
	for _, a := range addresses {
		if record.To == a {
			allowed = true
		}
	}
	if !allowed {
		return inbox.PeerRecord{}, false, ErrPeerMessageNotFound
	}
	if record.NotifiedAt == "" {
		return inbox.PeerRecord{}, false, ErrPeerMessageNotNotified
	}
	if record.ReadAt != "" {
		if err := tx.Commit(); err != nil {
			return inbox.PeerRecord{}, false, err
		}
		return record, false, nil
	}
	stamp := at.UTC().Format(sortableTimeFormat)
	res, err := tx.Exec(`
		UPDATE inbox_items SET read_at = ?, read_by = ?
		WHERE id = ? AND kind = ? AND notified_at != '' AND read_at = ''
	`, stamp, readBy, id, inbox.PeerMessage)
	if err != nil {
		return inbox.PeerRecord{}, false, fmt.Errorf("read peer message: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return inbox.PeerRecord{}, false, err
	}
	if changed == 0 {
		return inbox.PeerRecord{}, false, ErrPeerMessageNotNotified
	}
	if err := acknowledgeInbox(tx, record.To); err != nil {
		return inbox.PeerRecord{}, false, err
	}
	record.ReadAt = stamp
	record.ReadBy = readBy
	if err := tx.Commit(); err != nil {
		return inbox.PeerRecord{}, false, err
	}
	return record, true, nil
}

func (s *Store) PeerMessageGuardCounts(sender who.Party, to who.Address, body string, dedupeSince, rateSince time.Time) (inbox.PeerGuardCounts, error) {
	recipient := to.String()
	s.mu.Lock()
	defer s.mu.Unlock()

	var counts inbox.PeerGuardCounts
	err := s.db.QueryRow(`
		SELECT
			EXISTS (
				SELECT 1 FROM peer_messages p
				JOIN inbox_items i ON i.kind = ? AND i.source_id = p.id
				WHERE p.sender = ? AND i.address = ?
					AND p.body = ? AND p.created_at >= ?
			),
			(
				SELECT COUNT(*) FROM peer_messages p
				JOIN inbox_items i ON i.kind = ? AND i.source_id = p.id
				WHERE p.sender = ? AND i.address = ? AND p.created_at >= ?
			),
			(
				SELECT COUNT(*) FROM inbox_items
				WHERE address = ? AND kind = ? AND read_at = ''
			)
	`,
		inbox.PeerMessage, sender, recipient, body, dedupeSince.UTC().Format(sortableTimeFormat),
		inbox.PeerMessage, sender, recipient, rateSince.UTC().Format(sortableTimeFormat),
		recipient, inbox.PeerMessage,
	).Scan(&counts.DuplicateFromSender, &counts.FromSenderInWindow, &counts.UnreadForRecipient)
	if err != nil {
		return inbox.PeerGuardCounts{}, fmt.Errorf("read peer message guard counts: %w", err)
	}
	return counts, nil
}

func (s *Store) PutPeerMessage(message inbox.Message, to who.Address) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, err := time.Parse(time.RFC3339Nano, message.CreatedAt)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO peer_messages(id,sender,body,created_at) VALUES(?,?,?,?)", message.ID, message.Sender, message.Body, at.UTC().Format(sortableTimeFormat)); err != nil {
		return err
	}
	if err := putInbox(tx, inbox.Item{ID: message.ID, To: to, Kind: inbox.PeerMessage, Source: message.ID}, at); err != nil {
		return err
	}
	return tx.Commit()
}
