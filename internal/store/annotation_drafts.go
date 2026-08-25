package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// A save is accepted only above both the stored generation and the tombstone, so no
// in-flight save resurrects cleared marks. Clearing is a tombstone, not a delete.

var ErrStaleAnnotationSave = errors.New("stale annotation save")

type annotationDraftTable struct {
	table string
	key   string
	// Markdown drafts have no note column: they read/write the empty string so both
	// tables keep one query shape.
	note bool
}

var (
	markdownDraftTable = annotationDraftTable{table: "markdown_annotation_drafts", key: "path"}
	sessionDraftTable  = annotationDraftTable{table: "session_annotation_drafts", key: "session_id", note: true}
)

type annotationDraft struct {
	Annotations string
	Note        string
	Generation  int // max(generation, tombstone_generation)
	UpdatedAt   string
}

func (t annotationDraftTable) noteColumn() string {
	if t.note {
		return "note"
	}
	return "''"
}

// The generation includes any tombstone, so a re-mounting client seeds past a clear.
func (t annotationDraftTable) get(s *Store, key string) (annotationDraft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var annotations, note, updatedAt string
	var generation, tombstone int
	query := fmt.Sprintf(
		"SELECT annotations_json, %s, generation, tombstone_generation, updated_at FROM %s WHERE %s = ?",
		t.noteColumn(), t.table, t.key,
	)
	err := s.db.QueryRow(query, key).Scan(&annotations, &note, &generation, &tombstone, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return annotationDraft{Annotations: "[]", Generation: 0}, nil
	}
	if err != nil {
		return annotationDraft{}, fmt.Errorf("failed to get %s draft: %w", t.table, err)
	}
	return annotationDraft{
		Annotations: annotations,
		Note:        note,
		Generation:  max(generation, tombstone),
		UpdatedAt:   updatedAt,
	}, nil
}

func (t annotationDraftTable) save(s *Store, key, annotationsJSON, note string, generation int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin %s save: %w", t.table, err)
	}
	defer tx.Rollback()

	storedGeneration, tombstone, err := t.readGenerations(tx, key)
	if err != nil {
		return err
	}
	if generation <= storedGeneration || generation <= tombstone {
		return ErrStaleAnnotationSave
	}

	columns, values := "annotations_json", "?"
	updates := "annotations_json = excluded.annotations_json"
	args := []any{key, annotationsJSON}
	if t.note {
		columns, values = columns+", note", values+", ?"
		updates += ", note = excluded.note"
		args = append(args, note)
	}
	args = append(args, generation, now.UTC().Format(time.RFC3339))

	query := fmt.Sprintf(`
		INSERT INTO %s (%s, %s, generation, tombstone_generation, updated_at)
		VALUES (?, %s, ?, 0, ?)
		ON CONFLICT(%s) DO UPDATE SET
			%s,
			generation = excluded.generation,
			updated_at = excluded.updated_at
	`, t.table, t.key, columns, values, t.key, updates)
	if _, err := tx.Exec(query, args...); err != nil {
		return fmt.Errorf("failed to save %s draft: %w", t.table, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit %s save: %w", t.table, err)
	}
	return nil
}

// Works on a missing row — the tombstone IS the row.
func (t annotationDraftTable) clear(s *Store, key string, generation int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin %s clear: %w", t.table, err)
	}
	defer tx.Rollback()

	storedGeneration, tombstone, err := t.readGenerations(tx, key)
	if err != nil {
		return err
	}
	newTombstone := max(generation, max(storedGeneration, tombstone))

	// Left behind, the note would front the next turn's annotations.
	noteColumns, noteValues, noteUpdates := "", "", ""
	if t.note {
		noteColumns, noteValues, noteUpdates = ", note", ", ''", "note = '',\n\t\t\t"
	}
	query := fmt.Sprintf(`
		INSERT INTO %s (%s, annotations_json%s, generation, tombstone_generation, updated_at)
		VALUES (?, '[]'%s, ?, ?, ?)
		ON CONFLICT(%s) DO UPDATE SET
			annotations_json = '[]',
			%stombstone_generation = excluded.tombstone_generation,
			updated_at = excluded.updated_at
	`, t.table, t.key, noteColumns, noteValues, t.key, noteUpdates)
	if _, err := tx.Exec(query, key, storedGeneration, newTombstone, now.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("failed to clear %s draft: %w", t.table, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit %s clear: %w", t.table, err)
	}
	return nil
}

func (t annotationDraftTable) delete(s *Store, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := fmt.Sprintf("DELETE FROM %s WHERE %s = ?", t.table, t.key)
	if _, err := s.db.Exec(query, key); err != nil {
		return fmt.Errorf("failed to delete %s draft: %w", t.table, err)
	}
	return nil
}

func (t annotationDraftTable) readGenerations(tx *sql.Tx, key string) (generation, tombstone int, err error) {
	query := fmt.Sprintf("SELECT generation, tombstone_generation FROM %s WHERE %s = ?", t.table, t.key)
	err = tx.QueryRow(query, key).Scan(&generation, &tombstone)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, fmt.Errorf("failed to read %s generation: %w", t.table, err)
	}
	return generation, tombstone, nil
}
