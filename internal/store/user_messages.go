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

type UserMessageAsset struct {
	ProfileID  string
	MessageID  string
	Attachment protocol.UserMessageAttachment
	State      string
}

func userMessageSubmission(msg protocol.UserMessageSendMessage) string {
	msg.RequestID = nil
	msg.ProfileID = nil
	msg.Cmd = protocol.CmdUserMessageSend
	b, _ := json.Marshal(msg)
	return string(b)
}

func (s *Store) UserMessageReplay(profileID string, msg protocol.UserMessageSendMessage) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var submission string
	err := s.db.QueryRow(`SELECT submission FROM user_messages WHERE profile_id=? AND id=?`, profileID, msg.MessageID).Scan(&submission)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if submission != userMessageSubmission(msg) {
		return true, fmt.Errorf("message_id %s already saved with a different payload", msg.MessageID)
	}
	return true, nil
}

func (s *Store) SaveUserMessage(profileID string, msg protocol.UserMessageSendMessage, to inbox.Address, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var prior string
	err = tx.QueryRow(`SELECT submission FROM user_messages WHERE profile_id=? AND id=?`, profileID, msg.MessageID).Scan(&prior)
	if err == nil {
		if prior != userMessageSubmission(msg) {
			return fmt.Errorf("message_id %s already saved with a different payload", msg.MessageID)
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	for position, id := range msg.AttachmentIds {
		var state string
		if err := tx.QueryRow(`SELECT state FROM user_message_attachments WHERE profile_id=? AND message_id=? AND attachment_id=?`, profileID, msg.MessageID, id).Scan(&state); err != nil {
			return fmt.Errorf("attachment %s is not ready: %w", id, err)
		}
		if state != "ready" {
			return fmt.Errorf("attachment %s is %s, expected ready", id, state)
		}
		if _, err := tx.Exec(`UPDATE user_message_attachments SET state='committed',position=? WHERE profile_id=? AND message_id=? AND attachment_id=?`, position, profileID, msg.MessageID, id); err != nil {
			return err
		}
	}
	inboxID := UserMessageInboxID(profileID, msg.MessageID)
	_, err = tx.Exec(`INSERT INTO user_messages(profile_id,id,inbox_item_id,submission,target_kind,target_member_id,body,created_at) VALUES(?,?,?,?,?,?,?,?)`, profileID, msg.MessageID, inboxID, userMessageSubmission(msg), msg.Target.Kind, protocol.Deref(msg.Target.MemberID), msg.Content, at.UTC().Format(sortableTimeFormat))
	if err != nil {
		return err
	}
	if err := putInbox(tx, inbox.Item{ID: inboxID, To: to, Kind: inbox.UserMessage, Text: msg.Content, Source: msg.MessageID}, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UserMessage(profileID, id string) (*protocol.UserMessageRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return userMessageRecord(s.db, profileID, id)
}

type userMessageQuery interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

var ErrUserMessageNotFound = errors.New("user message not found")

const userMessageSelect = `SELECT u.id,u.target_kind,u.target_member_id,u.body,u.created_at,COALESCE(i.read_at,'')
 FROM user_messages u LEFT JOIN inbox_items i ON i.id=u.inbox_item_id AND i.kind='user_message'`

func scanUserMessage(row inboxDeliveryScanner) (*protocol.UserMessageRecord, error) {
	var r protocol.UserMessageRecord
	var member, read string
	err := row.Scan(&r.ID, &r.Target.Kind, &member, &r.Content, &r.CreatedAt, &read)
	if err == sql.ErrNoRows {
		return nil, ErrUserMessageNotFound
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

func userMessageRecord(q userMessageQuery, profileID, id string) (*protocol.UserMessageRecord, error) {
	r, err := scanUserMessage(q.QueryRow(userMessageSelect+` WHERE u.profile_id=? AND u.id=?`, profileID, id))
	if err != nil {
		return nil, err
	}
	r.Attachments = []protocol.UserMessageAttachment{}
	rows, err := q.Query(`SELECT attachment_id,name,media_type,byte_count FROM user_message_attachments WHERE profile_id=? AND message_id=? AND state='committed' ORDER BY position,attachment_id`, profileID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a protocol.UserMessageAttachment
		if err := rows.Scan(&a.ID, &a.Name, &a.MediaType, &a.Bytes); err != nil {
			return nil, err
		}
		r.Attachments = append(r.Attachments, a)
	}
	return r, rows.Err()
}

func (s *Store) UserMessages(profileID string, limit int, cursor string) ([]protocol.UserMessageRecord, *string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		return nil, nil, fmt.Errorf("user_message_list limit must be positive, asked for %d", limit)
	}
	where := " WHERE profile_id=?"
	args := []any{profileID}
	if cursor != "" {
		var created string
		if err := s.db.QueryRow(`SELECT created_at FROM user_messages WHERE profile_id=? AND id=?`, profileID, cursor).Scan(&created); err != nil {
			return nil, nil, fmt.Errorf("invalid user message cursor %q: %w", cursor, err)
		}
		where += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
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
	result := []protocol.UserMessageRecord{}
	for _, id := range ids {
		r, err := userMessageRecord(s.db, profileID, id)
		if err != nil {
			return nil, nil, err
		}
		result = append(result, *r)
	}
	var next *string
	if len(result) == limit {
		last := result[len(result)-1]
		var more bool
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_messages WHERE profile_id=? AND (created_at < ? OR (created_at = ? AND id < ?)))`, profileID, last.CreatedAt, last.CreatedAt, last.ID).Scan(&more); err != nil {
			return nil, nil, err
		}
		if more {
			next = protocol.Ptr(last.ID)
		}
	}
	return result, next, nil
}

func (s *Store) UserMessageAsset(profileID, userMessage, id string) (*UserMessageAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a := &UserMessageAsset{ProfileID: profileID, MessageID: userMessage}
	a.Attachment.ID = id
	err := s.db.QueryRow(`SELECT name,media_type,byte_count,state FROM user_message_attachments WHERE profile_id=? AND message_id=? AND attachment_id=?`, profileID, userMessage, id).Scan(&a.Attachment.Name, &a.Attachment.MediaType, &a.Attachment.Bytes, &a.State)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}
func (s *Store) SaveUserMessageAsset(a UserMessageAsset) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO user_message_attachments(profile_id,message_id,attachment_id,name,media_type,byte_count,state) VALUES(?,?,?,?,?,?,?) ON CONFLICT(profile_id,message_id,attachment_id) DO UPDATE SET media_type=excluded.media_type,byte_count=excluded.byte_count,state=excluded.state WHERE user_message_attachments.state!='committed'`, a.ProfileID, a.MessageID, a.Attachment.ID, a.Attachment.Name, a.Attachment.MediaType, a.Attachment.Bytes, a.State)
	return err
}
func (s *Store) UserMessageDraftAssets(profileID string) ([]protocol.UserMessageDraftAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT message_id,attachment_id,name,state,byte_count FROM user_message_attachments WHERE profile_id=? AND state IN ('staged','ready') ORDER BY message_id,attachment_id`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []protocol.UserMessageDraftAsset{}
	for rows.Next() {
		var a protocol.UserMessageDraftAsset
		if err := rows.Scan(&a.MessageID, &a.AttachmentID, &a.Name, &a.State, &a.NextOffset); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (s *Store) UserMessageDiscardedAssets(profileID string) ([]protocol.UserMessageDraftAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT message_id,attachment_id,name,state,byte_count FROM user_message_attachments WHERE profile_id=? AND state='discarded'`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []protocol.UserMessageDraftAsset{}
	for rows.Next() {
		var a protocol.UserMessageDraftAsset
		if err := rows.Scan(&a.MessageID, &a.AttachmentID, &a.Name, &a.State, &a.NextOffset); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func UserMessageInboxID(profileID, messageID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(profileID+":"+messageID)).String()
}
