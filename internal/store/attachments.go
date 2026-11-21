package store

import (
	"database/sql"

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
	_, err := s.db.Exec(`INSERT INTO attachments(attachment_id,profile_id,owner_kind,owner_id,name,media_type,byte_count,state) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(attachment_id) DO UPDATE SET media_type=excluded.media_type,byte_count=excluded.byte_count,state=excluded.state WHERE attachments.state!='committed'`, a.Attachment.ID, a.ProfileID, a.Owner.Kind, a.Owner.ID, a.Attachment.Name, a.Attachment.MediaType, a.Attachment.Bytes, a.State)
	return err
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
