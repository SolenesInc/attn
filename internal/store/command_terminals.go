package store

import "github.com/victorarias/attn/internal/protocol"

func (s *Store) UnfinishedOutpostTerminals(outpostID string) ([]protocol.SessionID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT s.id
 FROM sessions s LEFT JOIN session_exit_screens e ON e.session_id = s.id
 WHERE s.closed_at = '' AND s.command_terminal = 1 AND e.session_id IS NULL
 AND json_extract(s.launch_intent, '$.command.outpost_id') = ?`, outpostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []protocol.SessionID
	for rows.Next() {
		var session protocol.SessionID
		if err := rows.Scan(&session); err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	return result, rows.Err()
}
