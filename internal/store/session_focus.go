package store

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func (s *Store) RecordSessionFocus(id protocol.SessionID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO session_focus(session_id, focused_at) VALUES(?, ?) ON CONFLICT(session_id) DO UPDATE SET focused_at = excluded.focused_at`, id, at.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) SessionFocusTimes() (map[protocol.SessionID]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT session_id, focused_at FROM session_focus`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	focused := map[protocol.SessionID]string{}
	for rows.Next() {
		var id protocol.SessionID
		var at string
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		focused[id] = at
	}
	return focused, rows.Err()
}
