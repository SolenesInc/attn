package store

import (
	"time"
)

type SessionAnnotationDraft struct {
	SessionID   string
	Annotations string
	Note        string
	Generation  int
	UpdatedAt   string
}

func (s *Store) GetSessionAnnotationDraft(sessionID string) (*SessionAnnotationDraft, error) {
	draft, err := sessionDraftTable.get(s, sessionID)
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

func (s *Store) SaveSessionAnnotationDraft(sessionID, annotationsJSON, note string, generation int, now time.Time) error {
	return sessionDraftTable.save(s, sessionID, annotationsJSON, note, generation, now)
}

func (s *Store) ClearSessionAnnotationDraft(sessionID string, generation int, now time.Time) error {
	return sessionDraftTable.clear(s, sessionID, generation, now)
}

func (s *Store) DeleteSessionAnnotationDraft(sessionID string) error {
	return sessionDraftTable.delete(s, sessionID)
}
