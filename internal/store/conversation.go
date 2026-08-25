package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// Repeated observations are no-ops. When the live session row is already gone the ticket
// mirror is still updated: ticket Resume captures this binding after close.
func (s *Store) TransitionSessionConversation(sessionID, nativeID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	nativeID = strings.TrimSpace(nativeID)
	if sessionID == "" || nativeID == "" {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return false, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("begin conversation transition: %w", err)
	}
	defer tx.Rollback()

	var current string
	err = tx.QueryRow(`SELECT resume_session_id FROM sessions WHERE id = ?`, sessionID).Scan(&current)
	if err == sql.ErrNoRows {
		if _, err := tx.Exec(
			`UPDATE tickets SET resume_session_id = ? WHERE assignee = ?`,
			nativeID,
			sessionID,
		); err != nil {
			return false, fmt.Errorf("mirror closed-session conversation binding for %s: %w", sessionID, err)
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit closed-session conversation binding for %s: %w", sessionID, err)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read conversation binding for session %s: %w", sessionID, err)
	}
	if strings.TrimSpace(current) == nativeID {
		return false, nil
	}

	query := `UPDATE sessions SET resume_session_id = ? WHERE id = ?`
	if strings.TrimSpace(current) != "" {
		query = `
			UPDATE sessions
			SET resume_session_id = ?, activity = '', activity_at = '', activity_cursor = ''
			WHERE id = ?
		`
	}
	if _, err := tx.Exec(query, nativeID, sessionID); err != nil {
		return false, fmt.Errorf("update conversation binding for session %s: %w", sessionID, err)
	}
	if _, err := tx.Exec(
		`UPDATE tickets SET resume_session_id = ? WHERE assignee = ?`,
		nativeID,
		sessionID,
	); err != nil {
		return false, fmt.Errorf("mirror conversation binding for session %s: %w", sessionID, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit conversation transition for session %s: %w", sessionID, err)
	}
	return true, nil
}
