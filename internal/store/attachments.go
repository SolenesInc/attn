package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

type AttachmentState string

const (
	AttachmentStaged    AttachmentState = "staged"
	AttachmentReady     AttachmentState = "ready"
	AttachmentCommitted AttachmentState = "committed"
	AttachmentDiscarded AttachmentState = "discarded"
)

type StoredAttachment struct {
	ProfileID  string
	Owner      protocol.AttachmentOwner
	Attachment protocol.Attachment
	State      AttachmentState
}

var ErrAttachmentNotSaved = errors.New("the attachment is discarded, committed, or its ask is gone")

const attachmentSelect = `SELECT attachment_id,profile_id,owner_kind,owner_id,name,media_type,byte_count,state FROM attachments`

func scanAttachment(row inboxDeliveryScanner) (StoredAttachment, error) {
	var a StoredAttachment
	err := row.Scan(&a.Attachment.ID, &a.ProfileID, &a.Owner.Kind, &a.Owner.ID, &a.Attachment.Name, &a.Attachment.MediaType, &a.Attachment.Bytes, &a.State)
	return a, err
}

func (s *Store) Attachment(id string) (*StoredAttachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, err := scanAttachment(s.db.QueryRow(attachmentSelect+` WHERE attachment_id=?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) SaveAttachment(a StoredAttachment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`INSERT INTO attachments(attachment_id,profile_id,owner_kind,owner_id,name,media_type,byte_count,state)
SELECT ?,?,?,?,?,?,?,?
WHERE ?!='ask' OR EXISTS(SELECT 1 FROM asks WHERE id=? AND answered_at='') OR EXISTS(SELECT 1 FROM attachments WHERE attachment_id=?)
ON CONFLICT(attachment_id) DO UPDATE SET media_type=excluded.media_type,byte_count=excluded.byte_count,state=excluded.state WHERE attachments.state NOT IN ('committed','discarded')`,
		a.Attachment.ID, a.ProfileID, a.Owner.Kind, a.Owner.ID, a.Attachment.Name, a.Attachment.MediaType, a.Attachment.Bytes, a.State,
		a.Owner.Kind, a.Owner.ID, a.Attachment.ID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrAttachmentNotSaved
	}
	return nil
}

func (s *Store) AttachmentsIn(states ...AttachmentState) ([]StoredAttachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []StoredAttachment{}
	for _, state := range states {
		rows, err := s.db.Query(attachmentSelect+` WHERE state=? ORDER BY attachment_id`, state)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			a, err := scanAttachment(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			result = append(result, a)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Store) DiscardedAttachmentsOf(kind protocol.AttachmentOwnerKind, ownerIDs []string) ([]StoredAttachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []StoredAttachment{}
	for _, ownerID := range ownerIDs {
		rows, err := s.db.Query(attachmentSelect+` WHERE owner_kind=? AND owner_id=? AND state=?`, kind, ownerID, AttachmentDiscarded)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			a, err := scanAttachment(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			result = append(result, a)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func commitAttachments(tx *sql.Tx, profileID string, owner protocol.AttachmentOwner, ids []string) error {
	for position, id := range ids {
		var name string
		var state AttachmentState
		err := tx.QueryRow(`SELECT name,state FROM attachments WHERE attachment_id=? AND profile_id=? AND owner_kind=? AND owner_id=?`, id, profileID, owner.Kind, owner.ID).Scan(&name, &state)
		if err == sql.ErrNoRows {
			return fmt.Errorf("attachment %s does not belong to %s %s", id, owner.Kind, owner.ID)
		}
		if err != nil {
			return err
		}
		if state != AttachmentReady {
			return fmt.Errorf("attachment %s (%q) is %s, expected ready", id, name, state)
		}
		if _, err := tx.Exec(`UPDATE attachments SET state='committed',position=? WHERE attachment_id=?`, position, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) OwnedAttachments(profileID string, owner protocol.AttachmentOwner) ([]protocol.Attachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return committedAttachments(s.db, profileID, owner)
}

func committedAttachments(q quickCaptureQuery, profileID string, owner protocol.AttachmentOwner) ([]protocol.Attachment, error) {
	rows, err := q.Query(`SELECT attachment_id,name,media_type,byte_count FROM attachments WHERE profile_id=? AND owner_kind=? AND owner_id=? AND state='committed' ORDER BY position,attachment_id`, profileID, owner.Kind, owner.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attachments := []protocol.Attachment{}
	for rows.Next() {
		var a protocol.Attachment
		if err := rows.Scan(&a.ID, &a.Name, &a.MediaType, &a.Bytes); err != nil {
			return nil, err
		}
		attachments = append(attachments, a)
	}
	return attachments, rows.Err()
}
