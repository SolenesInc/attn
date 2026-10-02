package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
)

type CaptureAsset struct {
	CaptureID  string
	Attachment protocol.CaptureAttachment
	State      string
}

func captureSubmission(msg protocol.CaptureSendMessage) string {
	msg.RequestID = nil
	msg.Cmd = protocol.CmdCaptureSend
	b, _ := json.Marshal(msg)
	return string(b)
}

func (s *Store) CaptureReplay(msg protocol.CaptureSendMessage) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var submission string
	err := s.db.QueryRow(`SELECT submission FROM user_messages WHERE id=?`, msg.CaptureID).Scan(&submission)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if submission != captureSubmission(msg) {
		return true, fmt.Errorf("capture_id %s already saved with a different payload", msg.CaptureID)
	}
	return true, nil
}

func (s *Store) SaveCapture(msg protocol.CaptureSendMessage, to inbox.Address, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var prior string
	err = tx.QueryRow(`SELECT submission FROM user_messages WHERE id=?`, msg.CaptureID).Scan(&prior)
	if err == nil {
		if prior != captureSubmission(msg) {
			return fmt.Errorf("capture_id %s already saved with a different payload", msg.CaptureID)
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	for position, id := range msg.AttachmentIds {
		var state string
		if err := tx.QueryRow(`SELECT state FROM capture_attachments WHERE capture_id=? AND attachment_id=?`, msg.CaptureID, id).Scan(&state); err != nil {
			return fmt.Errorf("attachment %s is not ready: %w", id, err)
		}
		if state != "ready" {
			return fmt.Errorf("attachment %s is %s, expected ready", id, state)
		}
		if _, err := tx.Exec(`UPDATE capture_attachments SET state='committed',position=? WHERE capture_id=? AND attachment_id=?`, position, msg.CaptureID, id); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO user_messages(id,submission,target_kind,target_member_id,body,created_at) VALUES(?,?,?,?,?,?)`, msg.CaptureID, captureSubmission(msg), msg.Target.Kind, protocol.Deref(msg.Target.MemberID), msg.Content, at.UTC().Format(sortableTimeFormat))
	if err != nil {
		return err
	}
	if err := putInbox(tx, inbox.Item{ID: msg.CaptureID, To: to, Kind: inbox.UserMessage, Text: msg.Content, Source: msg.CaptureID}, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Capture(id string) (*protocol.CaptureRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return captureRecord(s.db, id)
}

type captureQuery interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

var ErrCaptureNotFound = errors.New("capture not found")

const captureSelect = `SELECT u.id,u.target_kind,u.target_member_id,u.body,u.created_at,COALESCE(i.read_at,'')
 FROM user_messages u LEFT JOIN inbox_items i ON i.id=u.id AND i.kind='user_message'`

func scanCapture(row inboxDeliveryScanner) (*protocol.CaptureRecord, error) {
	var r protocol.CaptureRecord
	var member, read string
	err := row.Scan(&r.ID, &r.Target.Kind, &member, &r.Content, &r.CreatedAt, &read)
	if err == sql.ErrNoRows {
		return nil, ErrCaptureNotFound
	}
	if err != nil {
		return nil, err
	}
	if member != "" {
		r.Target.MemberID = protocol.Ptr(member)
	}
	if read != "" {
		r.ReadAt = protocol.Ptr(read)
	}
	return &r, nil
}

func captureRecord(q captureQuery, id string) (*protocol.CaptureRecord, error) {
	r, err := scanCapture(q.QueryRow(captureSelect+` WHERE u.id=?`, id))
	if err != nil {
		return nil, err
	}
	r.Attachments = []protocol.CaptureAttachment{}
	rows, err := q.Query(`SELECT attachment_id,name,media_type,byte_count FROM capture_attachments WHERE capture_id=? AND state='committed' ORDER BY position,attachment_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a protocol.CaptureAttachment
		if err := rows.Scan(&a.ID, &a.Name, &a.MediaType, &a.Bytes); err != nil {
			return nil, err
		}
		r.Attachments = append(r.Attachments, a)
	}
	return r, rows.Err()
}

func (s *Store) Captures(limit int, cursor string) ([]protocol.CaptureRecord, *string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		return nil, nil, fmt.Errorf("capture_list limit must be positive, asked for %d", limit)
	}
	where := ""
	args := []any{}
	if cursor != "" {
		var created string
		if err := s.db.QueryRow(`SELECT created_at FROM user_messages WHERE id=?`, cursor).Scan(&created); err != nil {
			return nil, nil, fmt.Errorf("invalid capture cursor %q: %w", cursor, err)
		}
		where = ` WHERE created_at < ? OR (created_at = ? AND id < ?)`
		args = append(args, created, created, cursor)
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT id FROM user_messages`+where+` ORDER BY created_at DESC,id DESC LIMIT ?`, args...)
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
	result := []protocol.CaptureRecord{}
	for _, id := range ids {
		r, err := captureRecord(s.db, id)
		if err != nil {
			return nil, nil, err
		}
		result = append(result, *r)
	}
	var next *string
	if len(result) == limit {
		last := result[len(result)-1]
		var more bool
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_messages WHERE created_at < ? OR (created_at = ? AND id < ?))`, last.CreatedAt, last.CreatedAt, last.ID).Scan(&more); err != nil {
			return nil, nil, err
		}
		if more {
			next = protocol.Ptr(last.ID)
		}
	}
	return result, next, nil
}

func (s *Store) CaptureAsset(capture, id string) (*CaptureAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a := &CaptureAsset{CaptureID: capture}
	a.Attachment.ID = id
	err := s.db.QueryRow(`SELECT name,media_type,byte_count,state FROM capture_attachments WHERE capture_id=? AND attachment_id=?`, capture, id).Scan(&a.Attachment.Name, &a.Attachment.MediaType, &a.Attachment.Bytes, &a.State)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}
func (s *Store) SaveCaptureAsset(a CaptureAsset) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO capture_attachments(capture_id,attachment_id,name,media_type,byte_count,state) VALUES(?,?,?,?,?,?) ON CONFLICT(capture_id,attachment_id) DO UPDATE SET media_type=excluded.media_type,byte_count=excluded.byte_count,state=excluded.state WHERE capture_attachments.state!='committed'`, a.CaptureID, a.Attachment.ID, a.Attachment.Name, a.Attachment.MediaType, a.Attachment.Bytes, a.State)
	return err
}
func (s *Store) CaptureDraftAssets() ([]protocol.CaptureDraftAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT capture_id,attachment_id,name,state,byte_count FROM capture_attachments WHERE state IN ('staged','ready') ORDER BY capture_id,attachment_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []protocol.CaptureDraftAsset{}
	for rows.Next() {
		var a protocol.CaptureDraftAsset
		if err := rows.Scan(&a.CaptureID, &a.AttachmentID, &a.Name, &a.State, &a.NextOffset); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (s *Store) CaptureDiscardedAssets() ([]protocol.CaptureDraftAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT capture_id,attachment_id,name,state,byte_count FROM capture_attachments WHERE state='discarded'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []protocol.CaptureDraftAsset{}
	for rows.Next() {
		var a protocol.CaptureDraftAsset
		if err := rows.Scan(&a.CaptureID, &a.AttachmentID, &a.Name, &a.State, &a.NextOffset); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
