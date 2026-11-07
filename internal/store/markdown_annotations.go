package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

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

// ChangeMarkdownAnnotations applies one reader's changes to whatever the draft
// holds now, so marks the reader did not change stay. It returns the draft as it
// then stands.
func (s *Store) ChangeMarkdownAnnotations(path string, upserts []protocol.MarkdownAnnotation, removedIDs []string, now time.Time) ([]protocol.MarkdownAnnotation, int, error) {
	return s.rewriteMarkdownDraft(path, now, func(marks []protocol.MarkdownAnnotation) []protocol.MarkdownAnnotation {
		return withUpserts(withoutIDs(marks, removedIDs), upserts)
	})
}

// SpendMarkdownAnnotations removes the marks a send delivered from whatever the
// draft holds now, so marks saved while the send was under way stay. It returns
// the draft as it then stands.
func (s *Store) SpendMarkdownAnnotations(path string, sent []protocol.MarkdownAnnotation, now time.Time) ([]protocol.MarkdownAnnotation, int, error) {
	return s.rewriteMarkdownDraft(path, now, func(marks []protocol.MarkdownAnnotation) []protocol.MarkdownAnnotation {
		return withoutDelivered(marks, sent)
	})
}

func (s *Store) rewriteMarkdownDraft(path string, now time.Time, rewrite func([]protocol.MarkdownAnnotation) []protocol.MarkdownAnnotation) ([]protocol.MarkdownAnnotation, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to begin annotation draft change: %w", err)
	}
	defer tx.Rollback()

	generation, tombstone, err := markdownDraftTable.readGenerations(tx, path)
	if err != nil {
		return nil, 0, err
	}
	current := max(generation, tombstone)
	stored := "[]"
	err = tx.QueryRow("SELECT annotations_json FROM markdown_annotation_drafts WHERE path = ?", path).Scan(&stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, 0, fmt.Errorf("failed to read annotation draft: %w", err)
	}
	marks := []protocol.MarkdownAnnotation{}
	if err := json.Unmarshal([]byte(stored), &marks); err != nil {
		return nil, 0, fmt.Errorf("stored annotation draft is corrupt: %w", err)
	}
	rewritten := rewrite(marks)
	if reflect.DeepEqual(marks, rewritten) {
		return marks, current, nil
	}
	rewrittenJSON, err := json.Marshal(rewritten)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to encode annotation draft: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO markdown_annotation_drafts (path, annotations_json, generation, tombstone_generation, updated_at)
		VALUES (?, ?, ?, 0, ?)
		ON CONFLICT(path) DO UPDATE SET
			annotations_json = excluded.annotations_json,
			generation = excluded.generation,
			updated_at = excluded.updated_at
	`, path, string(rewrittenJSON), current+1, now.UTC().Format(time.RFC3339)); err != nil {
		return nil, 0, fmt.Errorf("failed to save annotation draft: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("failed to commit annotation draft change: %w", err)
	}
	return rewritten, current + 1, nil
}

func withoutIDs(marks []protocol.MarkdownAnnotation, ids []string) []protocol.MarkdownAnnotation {
	kept := make([]protocol.MarkdownAnnotation, 0, len(marks))
	for _, mark := range marks {
		if !slices.Contains(ids, mark.ID) {
			kept = append(kept, mark)
		}
	}
	return kept
}

func withUpserts(marks, upserts []protocol.MarkdownAnnotation) []protocol.MarkdownAnnotation {
	for _, upsert := range upserts {
		at := slices.IndexFunc(marks, func(mark protocol.MarkdownAnnotation) bool { return mark.ID == upsert.ID })
		if at >= 0 {
			marks[at] = upsert
		} else {
			marks = append(marks, upsert)
		}
	}
	return marks
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
