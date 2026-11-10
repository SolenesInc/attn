package store

import "github.com/victorarias/attn/internal/protocol"

func (s *Store) RecordCodeNavSetup(host, repository, language string, session protocol.SessionID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO code_nav_setups (host, repository, language, session_id) VALUES (?, ?, ?, ?)
		ON CONFLICT (host, repository, language) DO UPDATE SET session_id = excluded.session_id`, host, repository, language, string(session))
	return err
}

func (s *Store) CodeNavSetups(host, repository string) (map[string]protocol.SessionID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT language, session_id FROM code_nav_setups WHERE host = ? AND repository = ?`, host, repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	setups := map[string]protocol.SessionID{}
	for rows.Next() {
		var language, session string
		if err := rows.Scan(&language, &session); err != nil {
			return nil, err
		}
		setups[language] = protocol.SessionID(session)
	}
	return setups, rows.Err()
}
