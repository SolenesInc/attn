package store

import (
	"fmt"
	"time"

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

func (s *Store) OtherOpenSessionHolding(nativeID string, exceptSessionID protocol.SessionID) protocol.SessionID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil || nativeID == "" {
		return ""
	}
	var id protocol.SessionID
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE resume_session_id = ? AND closed_at = '' AND id != ? LIMIT 1`, nativeID, exceptSessionID).Scan(&id); err != nil {
		return ""
	}
	return id
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
