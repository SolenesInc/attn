package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

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

// SpendMarkdownAnnotations removes the marks a send delivered from whatever the
// draft holds now, so marks saved while the send was under way stay. It keeps the
// draft's generation unless nothing is left, so the sending client's next save
// is still accepted. It returns the generation the draft stands at.
func (s *Store) SpendMarkdownAnnotations(path string, sent []protocol.MarkdownAnnotation, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin annotation spend: %w", err)
	}
	defer tx.Rollback()

	generation, tombstone, err := markdownDraftTable.readGenerations(tx, path)
	if err != nil {
		return 0, err
	}
	var stored string
	err = tx.QueryRow("SELECT annotations_json FROM markdown_annotation_drafts WHERE path = ?", path).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		stored = "[]"
	} else if err != nil {
		return 0, fmt.Errorf("failed to read annotation draft: %w", err)
	}
	var marks []protocol.MarkdownAnnotation
	if err := json.Unmarshal([]byte(stored), &marks); err != nil {
		return 0, fmt.Errorf("stored annotation draft is corrupt: %w", err)
	}
	kept := withoutDelivered(marks, sent)
	if len(kept) == 0 {
		if err := markdownDraftTable.emptyWithin(tx, path, generation, max(generation, tombstone), now); err != nil {
			return 0, err
		}
	} else {
		keptJSON, err := json.Marshal(kept)
		if err != nil {
			return 0, fmt.Errorf("failed to encode kept annotations: %w", err)
		}
		if _, err := tx.Exec("UPDATE markdown_annotation_drafts SET annotations_json = ?, updated_at = ? WHERE path = ?",
			string(keptJSON), now.UTC().Format(time.RFC3339), path); err != nil {
			return 0, fmt.Errorf("failed to save kept annotations: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit annotation spend: %w", err)
	}
	return max(generation, tombstone), nil
}

func withoutDelivered(marks, sent []protocol.MarkdownAnnotation) []protocol.MarkdownAnnotation {
	sentByID := make(map[string]protocol.MarkdownAnnotation, len(sent))
	for _, mark := range sent {
		sentByID[mark.ID] = mark
	}
	kept := make([]protocol.MarkdownAnnotation, 0, len(marks))
	for _, mark := range marks {
		delivered, wasSent := sentByID[mark.ID]
		if wasSent && sameFeedback(mark, delivered) {
			continue
		}
		kept = append(kept, mark)
	}
	return kept
}

func sameFeedback(a, b protocol.MarkdownAnnotation) bool {
	return a.Type == b.Type &&
		protocol.Deref(a.Text) == protocol.Deref(b.Text) &&
		protocol.Deref(a.QuickLabelID) == protocol.Deref(b.QuickLabelID)
}
