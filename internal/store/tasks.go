package store

import (
	"fmt"
	"time"
)

type LegacyTaskRecord struct {
	ID            string
	Kind          string
	Subject       string
	State         string
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	MetaJSON      string
	Requeued      bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ONE transaction: splitting the read, write and delete looks equivalent and is not —
// a crash between the halves loses owed work.
func (s *Store) MigrateLegacyTasks(translate func(LegacyTaskRecord) JobRecord) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: migrate legacy tasks: %w", err)
	}
	defer tx.Rollback()

	// The rows are collected before anything is written: SQLite will not accept a
	// write on a connection with an open cursor on the same transaction.
	rows, err := tx.Query(
		`SELECT id, kind, subject, state, attempts, next_attempt_at, last_error, meta_json, requeued, created_at, updated_at
		 FROM tasks WHERE state <> 'done'`)
	if err != nil {
		return 0, fmt.Errorf("store: migrate legacy tasks: %w", err)
	}
	var owed []LegacyTaskRecord
	for rows.Next() {
		var (
			rec                             LegacyTaskRecord
			nextStr, createdStr, updatedStr string
			requeued                        int
		)
		if err := rows.Scan(&rec.ID, &rec.Kind, &rec.Subject, &rec.State, &rec.Attempts,
			&nextStr, &rec.LastError, &rec.MetaJSON, &requeued, &createdStr, &updatedStr); err != nil {
			rows.Close()
			return 0, fmt.Errorf("store: scan legacy task: %w", err)
		}
		rec.Requeued = requeued != 0
		rec.NextAttemptAt = parseStoreTime(nextStr)
		rec.CreatedAt = parseStoreTime(createdStr)
		rec.UpdatedAt = parseStoreTime(updatedStr)
		owed = append(owed, rec)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: migrate legacy tasks: %w", err)
	}

	for _, rec := range owed {
		if err := upsertJob(tx, translate(rec)); err != nil {
			return 0, fmt.Errorf("store: migrate legacy task %s: %w", rec.ID, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM tasks`); err != nil {
		return 0, fmt.Errorf("store: clear legacy tasks: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit legacy task handover: %w", err)
	}
	return len(owed), nil
}
