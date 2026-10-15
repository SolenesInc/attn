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
)

type QuickCaptureAssetState string

const (
	QuickCaptureAssetStaged    QuickCaptureAssetState = "staged"
	QuickCaptureAssetReady     QuickCaptureAssetState = "ready"
	QuickCaptureAssetCommitted QuickCaptureAssetState = "committed"
	QuickCaptureAssetDiscarded QuickCaptureAssetState = "discarded"
)

type QuickCaptureAsset struct {
	ProfileID  string
	CaptureID  string
	Attachment protocol.QuickCaptureAttachment
	State      QuickCaptureAssetState
}

func quickCaptureSubmission(msg protocol.QuickCaptureSendMessage) string {
	msg.RequestID = nil
	msg.ProfileID = nil
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

func (s *Store) SaveQuickCapture(profileID string, msg protocol.QuickCaptureSendMessage, to inbox.Address, at time.Time) error {
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
	for position, id := range msg.AttachmentIds {
		var state string
		if err := tx.QueryRow(`SELECT state FROM quick_capture_attachments WHERE profile_id=? AND capture_id=? AND attachment_id=?`, profileID, msg.CaptureID, id).Scan(&state); err != nil {
			return fmt.Errorf("attachment %s is not ready: %w", id, err)
		}
		if state != "ready" {
			return fmt.Errorf("attachment %s is %s, expected ready", id, state)
		}
		if _, err := tx.Exec(`UPDATE quick_capture_attachments SET state='committed',position=? WHERE profile_id=? AND capture_id=? AND attachment_id=?`, position, profileID, msg.CaptureID, id); err != nil {
			return err
		}
	}
	inboxID := QuickCaptureInboxID(profileID, msg.CaptureID)
	_, err = tx.Exec(`INSERT INTO quick_captures(profile_id,id,inbox_item_id,submission,mailbox_kind,mailbox_member_id,body,created_at) VALUES(?,?,?,?,?,?,?,?)`, profileID, msg.CaptureID, inboxID, quickCaptureSubmission(msg), msg.Mailbox.Kind, protocol.Deref(msg.Mailbox.MemberID), msg.Content, at.UTC().Format(sortableTimeFormat))
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

const quickCaptureSelect = `SELECT u.id,u.mailbox_kind,u.mailbox_member_id,u.body,u.created_at,COALESCE(i.read_at,'')
 FROM quick_captures u LEFT JOIN inbox_items i ON i.id=u.inbox_item_id AND i.kind='quick_capture'`

func scanQuickCapture(row inboxDeliveryScanner) (*protocol.QuickCaptureRecord, error) {
	var r protocol.QuickCaptureRecord
	var member, read string
	err := row.Scan(&r.ID, &r.Mailbox.Kind, &member, &r.Content, &r.CreatedAt, &read)
	if err == sql.ErrNoRows {
		return nil, ErrQuickCaptureNotFound
	}
	if err != nil {
		return nil, err
	}
	if member != "" {
		r.Mailbox.MemberID = protocol.Ptr(member)
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
	r.Attachments = []protocol.QuickCaptureAttachment{}
	rows, err := q.Query(`SELECT attachment_id,name,media_type,byte_count FROM quick_capture_attachments WHERE profile_id=? AND capture_id=? AND state='committed' ORDER BY position,attachment_id`, profileID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a protocol.QuickCaptureAttachment
		if err := rows.Scan(&a.ID, &a.Name, &a.MediaType, &a.Bytes); err != nil {
			return nil, err
		}
		r.Attachments = append(r.Attachments, a)
	}
	return r, rows.Err()
}

func (s *Store) QuickCaptures(profileID string, limit int, cursor string) ([]protocol.QuickCaptureRecord, *string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		return nil, nil, fmt.Errorf("quick_capture_list limit must be positive, asked for %d", limit)
	}
	where := " WHERE profile_id=?"
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

func (s *Store) QuickCaptureAsset(profileID, quickCapture, id string) (*QuickCaptureAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a := &QuickCaptureAsset{ProfileID: profileID, CaptureID: quickCapture}
	a.Attachment.ID = id
	err := s.db.QueryRow(`SELECT name,media_type,byte_count,state FROM quick_capture_attachments WHERE profile_id=? AND capture_id=? AND attachment_id=?`, profileID, quickCapture, id).Scan(&a.Attachment.Name, &a.Attachment.MediaType, &a.Attachment.Bytes, &a.State)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}
func (s *Store) SaveQuickCaptureAsset(a QuickCaptureAsset) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO quick_capture_attachments(profile_id,capture_id,attachment_id,name,media_type,byte_count,state) VALUES(?,?,?,?,?,?,?) ON CONFLICT(profile_id,capture_id,attachment_id) DO UPDATE SET media_type=excluded.media_type,byte_count=excluded.byte_count,state=excluded.state WHERE quick_capture_attachments.state!='committed'`, a.ProfileID, a.CaptureID, a.Attachment.ID, a.Attachment.Name, a.Attachment.MediaType, a.Attachment.Bytes, a.State)
	return err
}
func (s *Store) QuickCaptureDraftAssets(profileID string) ([]protocol.QuickCaptureDraftAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT capture_id,attachment_id,name,state,byte_count FROM quick_capture_attachments WHERE profile_id=? AND state IN ('staged','ready') ORDER BY capture_id,attachment_id`, profileID)
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

func (s *Store) QuickCaptureDiscardedAssets(profileID string) ([]protocol.QuickCaptureDraftAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT capture_id,attachment_id,name,state,byte_count FROM quick_capture_attachments WHERE profile_id=? AND state='discarded'`, profileID)
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
