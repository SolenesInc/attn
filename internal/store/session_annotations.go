package store

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type SessionAnnotationDraft struct {
	SessionID   protocol.SessionID
	Annotations string
	Note        string
	Generation  int
	UpdatedAt   string
}

func (s *Store) GetSessionAnnotationDraft(sessionID protocol.SessionID) (*SessionAnnotationDraft, error) {
	draft, err := sessionDraftTable.get(s, string(sessionID))
	if err != nil {
		return nil, err
	}
	return &SessionAnnotationDraft{
		SessionID:   sessionID,
		Annotations: draft.Annotations,
		Note:        draft.Note,
		Generation:  draft.Generation,
		UpdatedAt:   draft.UpdatedAt,
	}, nil
}

func (s *Store) SaveSessionAnnotationDraft(sessionID protocol.SessionID, annotationsJSON string, note string, generation int, now time.Time) error {
	return sessionDraftTable.save(s, string(sessionID), annotationsJSON, note, generation, now)
}

func (s *Store) ClearSessionAnnotationDraft(sessionID protocol.SessionID, generation int, now time.Time) error {
	return sessionDraftTable.clear(s, string(sessionID), generation, now)
}

func (s *Store) DeleteSessionAnnotationDraft(sessionID protocol.SessionID) error {
	return sessionDraftTable.delete(s, string(sessionID))
}
