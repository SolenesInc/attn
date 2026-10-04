package store

import (
	"log"

	"github.com/victorarias/attn/internal/protocol"
)

func (s *Store) SetSessionContextWindowCap(id string, cap int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cap < 0 {
		cap = 0
	}

	if s.db == nil {
		session, ok := s.sessions[id]
		if !ok {
			return false
		}
		if cap == 0 {
			session.ContextWindowCap = nil
		} else {
			session.ContextWindowCap = protocol.Ptr(cap)
		}
		return true
	}

	result, err := s.db.Exec(`UPDATE sessions SET context_window_cap = ? WHERE id = ? AND closed_at = ''`, cap, id)
	if err != nil {
		log.Printf("[store] SetSessionContextWindowCap: failed for session %s: %v", id, err)
		return false
	}
	updated, err := result.RowsAffected()
	return err == nil && updated == 1
}
