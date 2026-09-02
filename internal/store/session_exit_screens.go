package store

import (
	"database/sql"
	"errors"
	"log"
	"time"
)

// The viewport a session showed when its process exited, kept so the exit can
// be read after the worker is gone. Text is the rendered viewport, not scrollback.
type SessionExitScreen struct {
	SessionID  string
	Text       string
	Cols       int
	Rows       int
	ExitCode   int
	ExitSignal string
	ExitedAt   string
}

func (s *Store) SaveSessionExitScreen(rec SessionExitScreen, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("store has no database")
	}
	_, err := s.db.Exec(`
		INSERT INTO session_exit_screens
			(session_id, text, cols, rows, exit_code, exit_signal, exited_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			text = excluded.text, cols = excluded.cols, rows = excluded.rows,
			exit_code = excluded.exit_code, exit_signal = excluded.exit_signal,
			exited_at = excluded.exited_at`,
		rec.SessionID, rec.Text, rec.Cols, rec.Rows, rec.ExitCode, rec.ExitSignal, now.Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetSessionExitScreen(sessionID string) *SessionExitScreen {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}
	rec := SessionExitScreen{SessionID: sessionID}
	err := s.db.QueryRow(`
		SELECT text, cols, rows, exit_code, exit_signal, exited_at
		FROM session_exit_screens WHERE session_id = ?`, sessionID).
		Scan(&rec.Text, &rec.Cols, &rec.Rows, &rec.ExitCode, &rec.ExitSignal, &rec.ExitedAt)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("[store] GetSessionExitScreen %s: %v", sessionID, err)
		}
		return nil
	}
	return &rec
}

func (s *Store) DeleteSessionExitScreen(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM session_exit_screens WHERE session_id = ?`, sessionID)
	return err
}
