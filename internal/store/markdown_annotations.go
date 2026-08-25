package store

import (
	"time"
)

// ErrStaleMarkdownAnnotationSave marks a benignly stale save. Same value as
// ErrStaleAnnotationSave, so errors.Is against either name matches.
var ErrStaleMarkdownAnnotationSave = ErrStaleAnnotationSave

type MarkdownAnnotationDraft struct {
	Path        string
	Annotations string
	Generation  int
	UpdatedAt   string
}

func (s *Store) GetMarkdownAnnotationDraft(path string) (*MarkdownAnnotationDraft, error) {
	draft, err := markdownDraftTable.get(s, path)
	if err != nil {
		return nil, err
	}
	return &MarkdownAnnotationDraft{
		Path:        path,
		Annotations: draft.Annotations,
		Generation:  draft.Generation,
		UpdatedAt:   draft.UpdatedAt,
	}, nil
}

func (s *Store) SaveMarkdownAnnotationDraft(path, annotationsJSON string, generation int, now time.Time) error {
	return markdownDraftTable.save(s, path, annotationsJSON, "", generation, now)
}

func (s *Store) ClearMarkdownAnnotationDraft(path string, generation int, now time.Time) error {
	return markdownDraftTable.clear(s, path, generation, now)
}
