package store

import (
	"fmt"
	"time"
)

// CodexTerminal is a terminal whose Codex reaches its profile's shared app-server through attn.
type CodexTerminal struct {
	TerminalID string
	ProfileID  string
}

func (s *Store) SaveCodexTerminal(terminalID, profileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	if _, err := s.db.Exec(`INSERT INTO codex_terminals (terminal_id, profile_id, created_at) VALUES (?, ?, ?)
		ON CONFLICT(terminal_id) DO UPDATE SET profile_id = excluded.profile_id`,
		terminalID, profileID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("save shared Codex terminal %s: %w", terminalID, err)
	}
	return nil
}

func (s *Store) DeleteCodexTerminal(terminalID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM codex_terminals WHERE terminal_id = ?`, terminalID); err != nil {
		return fmt.Errorf("forget shared Codex terminal %s: %w", terminalID, err)
	}
	return nil
}

func (s *Store) CodexTerminals() ([]CodexTerminal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT terminal_id, profile_id FROM codex_terminals ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list shared Codex terminals: %w", err)
	}
	defer rows.Close()
	var terminals []CodexTerminal
	for rows.Next() {
		var t CodexTerminal
		if err := rows.Scan(&t.TerminalID, &t.ProfileID); err != nil {
			return nil, err
		}
		terminals = append(terminals, t)
	}
	return terminals, rows.Err()
}

// OpenSessionHolding names the open session whose conversation is nativeID, or "".
func (s *Store) OpenSessionHolding(nativeID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil || nativeID == "" {
		return ""
	}
	var id string
	if err := s.db.QueryRow(`SELECT id FROM sessions WHERE resume_session_id = ? AND closed_at = '' LIMIT 1`, nativeID).Scan(&id); err != nil {
		return ""
	}
	return id
}
