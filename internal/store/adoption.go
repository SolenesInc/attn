package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type Adoption struct {
	ID           protocol.SessionID
	Parent       protocol.SessionID
	Conversation string
	Title        string
	Reopen       bool
	CanReopen    func(protocol.SessionID) bool
}

type AdoptionOutcome struct {
	Session  protocol.SessionID
	Created  bool
	Reopened bool
}

func (s *Store) CommitAdoption(a Adoption) (AdoptionOutcome, error) {
	var outcome AdoptionOutcome
	a.Conversation = strings.TrimSpace(a.Conversation)
	a.Title = strings.TrimSpace(a.Title)
	if a.ID == "" || a.Parent == "" || a.Conversation == "" {
		return outcome, errors.New("adopt server conversation: id, parent and conversation are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return outcome, errors.New("adopt server conversation needs the SQLite store")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return outcome, fmt.Errorf("adopt conversation %s: %w", a.Conversation, err)
	}
	defer tx.Rollback()

	var profileID, directory, agent, host string
	err = tx.QueryRow(`SELECT profile_id, directory, agent, host FROM sessions WHERE id = ? AND closed_at = ''`, a.Parent).
		Scan(&profileID, &directory, &agent, &host)
	if errors.Is(err, sql.ErrNoRows) {
		return outcome, fmt.Errorf("adopt under session %s: %w", a.Parent, ErrSessionClosed)
	}
	if err != nil {
		return outcome, fmt.Errorf("adopt under session %s: %w", a.Parent, err)
	}

	var existing protocol.SessionID
	err = tx.QueryRow(`SELECT id FROM sessions WHERE profile_id = ? AND agent = ? AND host = ? AND resume_session_id = ? AND closed_at = '' LIMIT 1`,
		profileID, agent, host, a.Conversation).Scan(&existing)
	if err == nil {
		outcome.Session = existing
		return outcome, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return outcome, fmt.Errorf("check existing holder of %s: %w", a.Conversation, err)
	}

	var otherProfile string
	err = tx.QueryRow(`SELECT profile_id FROM sessions WHERE profile_id != ? AND agent = ? AND host = ? AND resume_session_id = ? AND closed_at = '' LIMIT 1`,
		profileID, agent, host, a.Conversation).Scan(&otherProfile)
	if err == nil {
		return outcome, fmt.Errorf("conversation %s is open in a session of profile %s", a.Conversation, otherProfile)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return outcome, fmt.Errorf("check cross-profile holder of %s: %w", a.Conversation, err)
	}

	var closed protocol.SessionID
	err = tx.QueryRow(`SELECT id FROM sessions WHERE profile_id = ? AND agent = ? AND host = ? AND resume_session_id = ? AND closed_at <> '' ORDER BY closed_at DESC LIMIT 1`,
		profileID, agent, host, a.Conversation).Scan(&closed)
	if err == nil {
		if !a.Reopen {
			return outcome, nil
		}
		var closing int
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_teardown_tombstones WHERE session_id = ?)`, closed).Scan(&closing); err != nil {
			return outcome, fmt.Errorf("check teardown of %s: %w", closed, err)
		}
		if closing == 1 || (a.CanReopen != nil && !a.CanReopen(closed)) {
			return outcome, nil
		}
		if profileID != "" {
			if _, err := loadLiveProfile(tx, profileID); err != nil {
				return outcome, fmt.Errorf("reopen adopted session %s: %w", closed, err)
			}
		}
		if _, err := tx.Exec(`
			UPDATE sessions
			SET closed_at = '', closed_by = '', close_reason = '', lifetime = 'server',
				user_label = CASE WHEN user_label = '' THEN ? ELSE user_label END
			WHERE id = ?`, a.Title, closed); err != nil {
			return outcome, fmt.Errorf("reopen adopted session %s: %w", closed, err)
		}
		if _, err := tx.Exec(`DELETE FROM session_exit_screens WHERE session_id = ?`, closed); err != nil {
			return outcome, fmt.Errorf("clear exit screen of %s: %w", closed, err)
		}
		if err := tx.Commit(); err != nil {
			return outcome, fmt.Errorf("reopen adopted session %s: %w", closed, err)
		}
		return AdoptionOutcome{Session: closed, Reopened: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return outcome, fmt.Errorf("check closed holder of %s: %w", a.Conversation, err)
	}

	if profileID != "" {
		if _, err := loadLiveProfile(tx, profileID); err != nil {
			return outcome, fmt.Errorf("adopt conversation %s: %w", a.Conversation, err)
		}
	}
	label := a.Title
	if label == "" {
		label = DefaultSessionLabel(directory, a.ID)
	}
	at := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
		INSERT INTO sessions (
			id, user_label, agent, directory, profile_id, launched_at, context_window_cap,
			resume_session_id, launch_intent, priority, created_at, host, lifetime
		)
		SELECT ?, ?, agent, directory, profile_id, '', context_window_cap,
			?, launch_intent, priority, ?, host, 'server'
		FROM sessions WHERE id = ?`,
		a.ID, label, a.Conversation, at, a.Parent,
	); err != nil {
		return outcome, fmt.Errorf("insert adopted session %s: %w", a.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return outcome, fmt.Errorf("insert adopted session %s: %w", a.ID, err)
	}
	return AdoptionOutcome{Session: a.ID, Created: true}, nil
}
