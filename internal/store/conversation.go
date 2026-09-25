package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

type SessionConversation struct {
	NativeID       string
	TranscriptPath string
}

var ErrConversationClaimed = errors.New("conversation is bound to another open session")

func (s *Store) TransitionSessionConversation(sessionID, nativeID, transcriptPath string) (bool, error) {
	return s.transitionSessionConversation(sessionID, nativeID, transcriptPath, true, false)
}

func (s *Store) ClaimSessionConversation(sessionID, nativeID, transcriptPath string) (bool, error) {
	return s.transitionSessionConversation(sessionID, nativeID, transcriptPath, true, true)
}

func (s *Store) TransitionSessionResumeID(sessionID, nativeID string) (bool, error) {
	return s.transitionSessionConversation(sessionID, nativeID, "", false, false)
}

func (s *Store) transitionSessionConversation(sessionID, nativeID, transcriptPath string, pathRequired, exclusive bool) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	nativeID = strings.TrimSpace(nativeID)
	transcriptPath = strings.TrimSpace(transcriptPath)
	if sessionID == "" || nativeID == "" || (pathRequired && transcriptPath == "") {
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

	var current SessionConversation
	err = tx.QueryRow(`SELECT resume_session_id, transcript_path FROM sessions WHERE id = ?`, sessionID).Scan(
		&current.NativeID,
		&current.TranscriptPath,
	)
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
	current.NativeID = strings.TrimSpace(current.NativeID)
	current.TranscriptPath = strings.TrimSpace(current.TranscriptPath)
	if exclusive {
		var claimed bool
		if err := tx.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM sessions WHERE resume_session_id = ? AND id != ? AND closed_at = '')`,
			nativeID,
			sessionID,
		).Scan(&claimed); err != nil {
			return false, fmt.Errorf("check conversation claim for session %s: %w", sessionID, err)
		}
		if claimed {
			return false, ErrConversationClaimed
		}
	}
	if pathRequired {
		if current.NativeID == nativeID && current.TranscriptPath == transcriptPath {
			return false, nil
		}
	} else {
		if current.NativeID == nativeID {
			return false, nil
		}
		transcriptPath = ""
	}

	query := `UPDATE sessions SET resume_session_id = ?, transcript_path = ? WHERE id = ? AND closed_at = ''`
	if current.NativeID != "" && current.NativeID != nativeID {
		query = `
			UPDATE sessions
			SET resume_session_id = ?, transcript_path = ?, activity = '', activity_at = '', activity_cursor = ''
			WHERE id = ? AND closed_at = ''
		`
	}
	if _, err := tx.Exec(query, nativeID, transcriptPath, sessionID); err != nil {
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

func (s *Store) GetSessionConversation(sessionID string) SessionConversation {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return SessionConversation{}
	}

	var binding SessionConversation
	if err := s.db.QueryRow(
		`SELECT resume_session_id, transcript_path FROM sessions WHERE id = ?`,
		strings.TrimSpace(sessionID),
	).Scan(&binding.NativeID, &binding.TranscriptPath); err != nil {
		return SessionConversation{}
	}
	binding.NativeID = strings.TrimSpace(binding.NativeID)
	binding.TranscriptPath = strings.TrimSpace(binding.TranscriptPath)
	return binding
}

func (s *Store) ConversationBoundToOtherSession(sessionID, nativeID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return false
	}
	var bound bool
	if err := s.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM sessions WHERE resume_session_id = ? AND id != ? AND closed_at = '')`,
		strings.TrimSpace(nativeID),
		strings.TrimSpace(sessionID),
	).Scan(&bound); err != nil {
		return false
	}
	return bound
}

func (s *Store) SetSessionLaunchedAt(sessionID string, launchedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return
	}
	if _, err := s.db.Exec(
		`UPDATE sessions SET launched_at = ? WHERE id = ? AND closed_at = ''`,
		launchedAt.UTC().Format(time.RFC3339Nano),
		sessionID,
	); err != nil {
		log.Printf("[store] SetSessionLaunchedAt: failed for session %s: %v", sessionID, err)
	}
}

func (s *Store) SessionLaunchedAt(sessionID string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return time.Time{}
	}
	var launchedAt string
	if err := s.db.QueryRow(`SELECT launched_at FROM sessions WHERE id = ?`, sessionID).Scan(&launchedAt); err != nil {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, launchedAt)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
