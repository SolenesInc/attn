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
	Host        string
	Path        string
	Annotations string
	Generation  int
	UpdatedAt   string
}

func (s *Store) GetMarkdownAnnotationDraft(key HostPath) (*MarkdownAnnotationDraft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	draft := &MarkdownAnnotationDraft{Host: key.Host, Path: key.Path, Annotations: "[]"}
	var generation, tombstone int
	err := s.db.QueryRow("SELECT annotations_json, generation, tombstone_generation, updated_at FROM markdown_annotation_drafts WHERE host = ? AND path = ?", key.Host, key.Path).
		Scan(&draft.Annotations, &generation, &tombstone, &draft.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return draft, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get markdown_annotation_drafts draft: %w", err)
	}
	draft.Generation = max(generation, tombstone)
	return draft, nil
}

// ChangeMarkdownAnnotations applies one reader's changes to whatever the draft
// holds now, so marks the reader did not change stay. It returns the draft as it
// then stands.
func (s *Store) ChangeMarkdownAnnotations(key HostPath, upserts []protocol.MarkdownAnnotation, removedIDs []string, now time.Time) ([]protocol.MarkdownAnnotation, int, error) {
	return s.rewriteMarkdownDraft(key, now, func(marks []protocol.MarkdownAnnotation) []protocol.MarkdownAnnotation {
		return withUpserts(withoutIDs(marks, removedIDs), upserts)
	})
}

// SpendMarkdownAnnotations removes the marks a send delivered from whatever the
// draft holds now, so marks saved while the send was under way stay. It returns
// the draft as it then stands.
func (s *Store) SpendMarkdownAnnotations(key HostPath, sent []protocol.MarkdownAnnotation, now time.Time) ([]protocol.MarkdownAnnotation, int, error) {
	return s.rewriteMarkdownDraft(key, now, func(marks []protocol.MarkdownAnnotation) []protocol.MarkdownAnnotation {
		return withoutDelivered(marks, sent)
	})
}

// ErrNoQuestion is returned when an answer names a mark the draft does not
// hold as a question.
var ErrNoQuestion = errors.New("no such question")

// AnswerMarkdownQuestion stores an answer on a question in the draft and
// returns the draft's generation after it.
func (s *Store) AnswerMarkdownQuestion(key HostPath, id, answer string, now time.Time) (int, error) {
	asked := false
	_, generation, err := s.rewriteMarkdownDraft(key, now, func(marks []protocol.MarkdownAnnotation) []protocol.MarkdownAnnotation {
		at := slices.IndexFunc(marks, func(mark protocol.MarkdownAnnotation) bool {
			return mark.ID == id && mark.Type == protocol.MarkdownAnnotationQuestion
		})
		if at < 0 {
			return marks
		}
		asked = true
		answered := slices.Clone(marks)
		answered[at].Answer = protocol.Ptr(answer)
		return answered
	})
	if err != nil {
		return 0, err
	}
	if !asked {
		return 0, ErrNoQuestion
	}
	return generation, nil
}

func (s *Store) rewriteMarkdownDraft(key HostPath, now time.Time, rewrite func([]protocol.MarkdownAnnotation) []protocol.MarkdownAnnotation) ([]protocol.MarkdownAnnotation, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to begin annotation draft change: %w", err)
	}
	defer tx.Rollback()

	var generation, tombstone int
	stored := "[]"
	err = tx.QueryRow("SELECT annotations_json, generation, tombstone_generation FROM markdown_annotation_drafts WHERE host = ? AND path = ?", key.Host, key.Path).
		Scan(&stored, &generation, &tombstone)
	current := max(generation, tombstone)
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
		INSERT INTO markdown_annotation_drafts (host, path, annotations_json, generation, tombstone_generation, updated_at)
		VALUES (?, ?, ?, ?, 0, ?)
		ON CONFLICT(host, path) DO UPDATE SET
			annotations_json = excluded.annotations_json,
			generation = excluded.generation,
			updated_at = excluded.updated_at
	`, key.Host, key.Path, string(rewrittenJSON), current+1, now.UTC().Format(time.RFC3339)); err != nil {
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
		upsert.Answer = nil
		at := slices.IndexFunc(marks, func(mark protocol.MarkdownAnnotation) bool { return mark.ID == upsert.ID })
		if at >= 0 {
			upsert.Answer = marks[at].Answer
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
		protocol.Deref(a.QuickLabelID) == protocol.Deref(b.QuickLabelID) &&
		protocol.Deref(a.Answer) == protocol.Deref(b.Answer) &&
		protocol.Deref(a.EditWas) == protocol.Deref(b.EditWas) &&
		protocol.Deref(a.EditAfterHead) == protocol.Deref(b.EditAfterHead) && protocol.Deref(a.EditBeforeHead) == protocol.Deref(b.EditBeforeHead) &&
		protocol.Deref(a.EditNow) == protocol.Deref(b.EditNow)
}
