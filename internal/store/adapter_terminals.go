package store

import (
	"database/sql"
	"errors"
)

func (s *Store) SaveAdapterTerminal(terminal, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO adapter_terminals (terminal_id, owner) VALUES (?, ?) ON CONFLICT(terminal_id) DO UPDATE SET owner = excluded.owner`, terminal, owner)
	return err
}

func (s *Store) DeleteAdapterTerminal(terminal string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM adapter_terminals WHERE terminal_id = ?`, terminal)
	return err
}

func (s *Store) AdapterTerminalOwner(terminal string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var owner string
	err := s.db.QueryRow(`SELECT owner FROM adapter_terminals WHERE terminal_id = ?`, terminal).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return owner, err == nil, err
}

func (s *Store) AdapterTerminals() (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT terminal_id, owner FROM adapter_terminals`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := make(map[string]string)
	for rows.Next() {
		var terminal, owner string
		if err := rows.Scan(&terminal, &owner); err != nil {
			return nil, err
		}
		owners[terminal] = owner
	}
	return owners, rows.Err()
}
