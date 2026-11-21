package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func quickCaptureSubmission(msg protocol.QuickCaptureSendMessage) string {
	msg.RequestID = nil
	msg.ProfileID = nil
	msg.SenderToken = nil
	msg.Cmd = protocol.CmdQuickCaptureSend
	b, _ := json.Marshal(msg)
	return string(b)
}

func (s *Store) QuickCaptureReplay(profileID string, msg protocol.QuickCaptureSendMessage) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var submission string
	err := s.db.QueryRow(`SELECT submission FROM quick_captures WHERE profile_id=? AND id=?`, profileID, msg.CaptureID).Scan(&submission)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if submission != quickCaptureSubmission(msg) {
		return true, fmt.Errorf("capture_id %s already saved with a different payload", msg.CaptureID)
	}
	return true, nil
}

func (s *Store) SaveQuickCapture(profileID string, msg protocol.QuickCaptureSendMessage, to who.Address, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var prior string
	err = tx.QueryRow(`SELECT submission FROM quick_captures WHERE profile_id=? AND id=?`, profileID, msg.CaptureID).Scan(&prior)
	if err == nil {
		if prior != quickCaptureSubmission(msg) {
			return fmt.Errorf("capture_id %s already saved with a different payload", msg.CaptureID)
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	if err := commitAttachments(tx, profileID, protocol.AttachmentOwner{Kind: protocol.AttachmentOwnerKindQuickCapture, ID: msg.CaptureID}, msg.AttachmentIds); err != nil {
		return err
	}
	inboxID := QuickCaptureInboxID(profileID, msg.CaptureID)
	_, err = tx.Exec(`INSERT INTO quick_captures(profile_id,id,inbox_item_id,submission,mailbox_kind,mailbox_member_id,mailbox_session_id,body,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, profileID, msg.CaptureID, inboxID, quickCaptureSubmission(msg), msg.Mailbox.Kind, protocol.Deref(msg.Mailbox.MemberID), protocol.Deref(msg.Mailbox.SessionID), msg.Content, at.UTC().Format(sortableTimeFormat))
	if err != nil {
		return err
	}
	if err := putInbox(tx, inbox.Item{ID: inboxID, To: to, Kind: inbox.QuickCapture, Text: msg.Content, Source: msg.CaptureID}, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) QuickCapture(profileID, id string) (*protocol.QuickCaptureRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return quickCaptureRecord(s.db, profileID, id)
}

type quickCaptureQuery interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

var ErrQuickCaptureNotFound = errors.New("quick capture not found")

const quickCaptureSelect = `SELECT u.id,u.mailbox_kind,u.mailbox_member_id,u.mailbox_session_id,u.body,u.created_at,COALESCE(i.read_at,'')
 FROM quick_captures u LEFT JOIN inbox_items i ON i.id=u.inbox_item_id AND i.kind='quick_capture'`

func scanQuickCapture(row inboxDeliveryScanner) (*protocol.QuickCaptureRecord, error) {
	var r protocol.QuickCaptureRecord
	var member, session, read string
	err := row.Scan(&r.ID, &r.Mailbox.Kind, &member, &session, &r.Content, &r.CreatedAt, &read)
	if err == sql.ErrNoRows {
		return nil, ErrQuickCaptureNotFound
	}
	if err != nil {
		return nil, err
	}
	if member != "" {
		r.Mailbox.MemberID = protocol.Ptr(member)
	}
	if session != "" {
		r.Mailbox.SessionID = protocol.Ptr(protocol.SessionID(session))
	}
	if read != "" {
		r.ReadAt = protocol.Ptr(read)
	}
	return &r, nil
}

func quickCaptureRecord(q quickCaptureQuery, profileID, id string) (*protocol.QuickCaptureRecord, error) {
	r, err := scanQuickCapture(q.QueryRow(quickCaptureSelect+` WHERE u.profile_id=? AND u.id=?`, profileID, id))
	if err != nil {
		return nil, err
	}
	r.Attachments, err = committedAttachments(q, profileID, protocol.AttachmentOwner{Kind: protocol.AttachmentOwnerKindQuickCapture, ID: id})
	return r, err
}

func (s *Store) QuickCaptures(profileID string, limit int, cursor string) ([]protocol.QuickCaptureRecord, *string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		return nil, nil, fmt.Errorf("quick_capture_list limit must be positive, asked for %d", limit)
	}
	where := " WHERE profile_id=? AND mailbox_kind!='session'"
	args := []any{profileID}
	if cursor != "" {
		var created string
		if err := s.db.QueryRow(`SELECT created_at FROM quick_captures WHERE profile_id=? AND id=?`, profileID, cursor).Scan(&created); err != nil {
			return nil, nil, fmt.Errorf("invalid quick capture cursor %q: %w", cursor, err)
		}
		where += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, created, created, cursor)
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT id FROM quick_captures`+where+` ORDER BY created_at DESC,id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	result := []protocol.QuickCaptureRecord{}
	for _, id := range ids {
		r, err := quickCaptureRecord(s.db, profileID, id)
		if err != nil {
			return nil, nil, err
		}
		result = append(result, *r)
	}
	var next *string
	if len(result) == limit {
		last := result[len(result)-1]
		var more bool
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM quick_captures WHERE profile_id=? AND (created_at < ? OR (created_at = ? AND id < ?)))`, profileID, last.CreatedAt, last.CreatedAt, last.ID).Scan(&more); err != nil {
			return nil, nil, err
		}
		if more {
			next = protocol.Ptr(last.ID)
		}
	}
	return result, next, nil
}

func (s *Store) QuickCaptureDraftAssets(profileID string) ([]protocol.QuickCaptureDraftAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT owner_id,attachment_id,name,state,byte_count FROM attachments WHERE profile_id=? AND owner_kind='quick_capture' AND state IN ('staged','ready') ORDER BY owner_id,attachment_id`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []protocol.QuickCaptureDraftAsset{}
	for rows.Next() {
		var a protocol.QuickCaptureDraftAsset
		if err := rows.Scan(&a.CaptureID, &a.AttachmentID, &a.Name, &a.State, &a.NextOffset); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func QuickCaptureInboxID(profileID, captureID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(profileID+":"+captureID)).String()
}
