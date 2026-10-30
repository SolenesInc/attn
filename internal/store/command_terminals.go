package store

import "github.com/victorarias/attn/internal/protocol"

type CommandTerminal struct {
	SessionID protocol.SessionID
	OutpostID string
}

func (s *Store) UnfinishedCommandTerminals(outpost string) ([]CommandTerminal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, nil
	}
	query := `SELECT s.id, json_extract(s.launch_intent, '$.command.outpost_id')
 FROM sessions s LEFT JOIN session_exit_screens e ON e.session_id = s.id
 WHERE s.closed_at = '' AND s.command_terminal = 1 AND e.session_id IS NULL`
	var args []any
	if outpost != "" {
		query += ` AND json_extract(s.launch_intent, '$.command.outpost_id') = ?`
		args = append(args, outpost)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CommandTerminal
	for rows.Next() {
		var command CommandTerminal
		if err := rows.Scan(&command.SessionID, &command.OutpostID); err != nil {
			return nil, err
		}
		result = append(result, command)
	}
	return result, rows.Err()
}
