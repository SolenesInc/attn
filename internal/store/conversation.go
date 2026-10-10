package store

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/victorarias/attn/internal/who"
	"log"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type SessionConversation struct {
	NativeID       string
	TranscriptPath string
}

var ErrConversationClaimed = errors.New("conversation is bound to another open session")

func (s *Store) TransitionSessionConversation(sessionID protocol.SessionID, nativeID string, transcriptPath string) (bool, error) {
	return s.transitionSessionConversation(sessionID, nativeID, transcriptPath, true, false)
}

func (s *Store) ClaimSessionConversation(sessionID protocol.SessionID, nativeID string, transcriptPath string) (bool, error) {
	return s.transitionSessionConversation(sessionID, nativeID, transcriptPath, true, true)
}

func (s *Store) TransitionSessionResumeID(sessionID protocol.SessionID, nativeID string) (bool, error) {
	return s.transitionSessionConversation(sessionID, nativeID, "", false, false)
}

func (s *Store) transitionSessionConversation(sessionID protocol.SessionID, nativeID string, transcriptPath string, pathRequired, exclusive bool) (bool, error) {
	sessionID = protocol.TrimID(sessionID)
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
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit conversation transition for session %s: %w", sessionID, err)
	}
	return true, nil
}

func (s *Store) GetSessionConversation(sessionID protocol.SessionID) SessionConversation {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return SessionConversation{}
	}

	var binding SessionConversation
	if err := s.db.QueryRow(
		`SELECT resume_session_id, transcript_path FROM sessions WHERE id = ?`, protocol.TrimID(sessionID),
	).Scan(&binding.NativeID, &binding.TranscriptPath); err != nil {
		return SessionConversation{}
	}
	binding.NativeID = strings.TrimSpace(binding.NativeID)
	binding.TranscriptPath = strings.TrimSpace(binding.TranscriptPath)
	return binding
}

// ConversationBoundToOtherSession reports whether an open session other than sessionID holds nativeID.
func (s *Store) ConversationBoundToOtherSession(sessionID protocol.SessionID, nativeID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return false
	}
	var bound bool
	if err := s.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM sessions WHERE resume_session_id = ? AND id != ? AND closed_at = '')`,
		strings.TrimSpace(nativeID), protocol.TrimID(sessionID),
	).Scan(&bound); err != nil {
		return false
	}
	return bound
}

// ConversationOwner names the session other than sessionID that holds nativeID: an open one first,
// else the one closed last. It is empty when none does.
func (s *Store) ConversationOwner(sessionID protocol.SessionID, nativeID string) protocol.SessionID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return ""
	}
	var owner protocol.SessionID
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE resume_session_id = ? AND id != ?
		AND profile_id = (SELECT profile_id FROM sessions WHERE id = ?)
		ORDER BY closed_at = '' DESC, closed_at DESC LIMIT 1`,
		strings.TrimSpace(nativeID), protocol.TrimID(sessionID), protocol.TrimID(sessionID)).Scan(&owner); err != nil {
		return ""
	}
	return owner
}

func (s *Store) SetSessionLaunchedAt(sessionID protocol.SessionID, launchedAt time.Time, member who.MemberKey) {
	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !member.IsZero() {
			if s.latestMemberSessions == nil {
				s.latestMemberSessions = map[who.MemberKey]protocol.SessionID{}
			}
			s.latestMemberSessions[member] = sessionID
		}
		return
	}
	err := s.profilesTx(func(tx *sql.Tx, _ string) error {
		if _, err := tx.Exec("UPDATE sessions SET launched_at = ? WHERE id = ? AND closed_at = ''", launchedAt.UTC().Format(time.RFC3339Nano), sessionID); err != nil {
			return err
		}
		if member.IsZero() {
			return nil
		}
		_, err := tx.Exec("UPDATE crew_members SET latest_session = ? WHERE member_key = ? AND EXISTS(SELECT 1 FROM sessions WHERE id = ? AND member_key = ? AND closed_at = '')", sessionID, member, sessionID, member)
		return err
	})
	if err != nil {
		log.Printf("[store] SetSessionLaunchedAt: failed for session %s: %v", sessionID, err)
	}
}

func (s *Store) SessionLaunchedAt(sessionID protocol.SessionID) time.Time {
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
