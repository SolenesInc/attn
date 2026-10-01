package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/victorarias/attn/internal/protocol"
)

func (s *Store) ExternalProcess(id string) (*protocol.ExternalProcess, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		if s.sessions[id] == nil {
			return nil, nil
		}
		if process := s.externalProcesses[id]; process != nil {
			copy := *process
			return &copy, nil
		}
		return nil, nil
	}
	var raw string
	err := s.db.QueryRow("SELECT external_process FROM sessions WHERE id = ? AND closed_at = ''", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var process protocol.ExternalProcess
	if err := json.Unmarshal([]byte(raw), &process); err != nil {
		return nil, err
	}
	return &process, nil
}

func (s *Store) ClearExternalProcess(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		delete(s.externalProcesses, id)
		return nil
	}
	_, err := s.db.Exec("UPDATE sessions SET external_process = '' WHERE id = ?", id)
	return err
}
