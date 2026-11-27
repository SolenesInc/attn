package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

type TerminalView struct {
	TerminalID string
	Link       string
	ProfileID  string
}

func (s *Store) SaveTerminalView(v TerminalView) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	if _, err := s.db.Exec(`INSERT INTO terminal_views (terminal_id, link, profile_id, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(terminal_id) DO UPDATE SET link = excluded.link, profile_id = excluded.profile_id`,
		v.TerminalID, v.Link, v.ProfileID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("save the %s view of terminal %s: %w", v.Link, v.TerminalID, err)
	}
	return nil
}

func (s *Store) DeleteTerminalView(terminalID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM terminal_views WHERE terminal_id = ?`, terminalID); err != nil {
		return fmt.Errorf("forget the view of terminal %s: %w", terminalID, err)
	}
	return nil
}

func (s *Store) TerminalViews(link string) ([]TerminalView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT terminal_id, link, profile_id FROM terminal_views WHERE link = ? ORDER BY created_at`, link)
	if err != nil {
		return nil, fmt.Errorf("list %s terminal views: %w", link, err)
	}
	defer rows.Close()
	var views []TerminalView
	for rows.Next() {
		var v TerminalView
		if err := rows.Scan(&v.TerminalID, &v.Link, &v.ProfileID); err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, rows.Err()
}

func (s *Store) SessionLifetime(id protocol.SessionID) (agent string, lifetime harness.Lifetime) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return "", harness.InTerminal
	}
	var stored string
	if err := s.db.QueryRow(`SELECT agent, lifetime FROM sessions WHERE id = ?`, id).Scan(&agent, &stored); err != nil {
		return "", harness.InTerminal
	}
	return agent, harness.Lifetime(stored)
}

func (s *Store) SetSessionLifetime(id protocol.SessionID, lifetime harness.Lifetime) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	if _, err := s.db.Exec(`UPDATE sessions SET lifetime = ? WHERE id = ?`, string(lifetime), id); err != nil {
		return fmt.Errorf("record session %s's conversation as running in its %s: %w", id, lifetime, err)
	}
	return nil
}

func routeThroughTerminalTx(tx *sql.Tx, here string, terminal protocol.TerminalID, from, to protocol.SessionID) error {
	_, err := tx.Exec(`
		UPDATE sessions
		SET lifetime = CASE
			WHEN EXISTS (SELECT 1 FROM terminal_views WHERE terminal_id = ?) THEN 'server'
			WHEN (SELECT host FROM sessions WHERE id = ?) != ? THEN COALESCE((SELECT lifetime FROM sessions WHERE id = ?), 'terminal')
			ELSE 'terminal'
		END
		WHERE id = ?`, terminal, from, here, from, to)
	return err
}

func (s *Store) OpenSessionHoldingOutside(profileID, nativeID string) protocol.SessionID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil || nativeID == "" {
		return ""
	}
	var id protocol.SessionID
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE profile_id != ? AND resume_session_id = ? AND closed_at = '' LIMIT 1`, profileID, nativeID).Scan(&id); err != nil {
		return ""
	}
	return id
}

func (s *Store) OpenSessionHoldingOnHost(profileID, host, nativeID string) protocol.SessionID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil || nativeID == "" {
		return ""
	}
	var id protocol.SessionID
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE profile_id = ? AND host = ? AND resume_session_id = ? AND closed_at = '' LIMIT 1`, profileID, host, nativeID).Scan(&id); err != nil {
		return ""
	}
	return id
}

func (s *Store) OpenSessionsHoldingOnHost(host, nativeID string) []protocol.SessionID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil || nativeID == "" {
		return nil
	}
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE host = ? AND resume_session_id = ? AND closed_at = '' ORDER BY id`, host, nativeID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []protocol.SessionID
	for rows.Next() {
		var id protocol.SessionID
		if err := rows.Scan(&id); err != nil {
			return nil
		}
		ids = append(ids, id)
	}
	return ids
}

func (s *Store) OpenSessionHolding(profileID, nativeID string) protocol.SessionID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil || nativeID == "" {
		return ""
	}
	var id protocol.SessionID
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE profile_id = ? AND resume_session_id = ? AND closed_at = '' LIMIT 1`, profileID, nativeID).Scan(&id); err != nil {
		return ""
	}
	return id
}

func (s *Store) OpenSessionsHolding(nativeID string) []protocol.SessionID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil || nativeID == "" {
		return nil
	}
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE resume_session_id = ? AND closed_at = '' ORDER BY id`, nativeID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []protocol.SessionID
	for rows.Next() {
		var id protocol.SessionID
		if err := rows.Scan(&id); err != nil {
			return nil
		}
		ids = append(ids, id)
	}
	return ids
}
