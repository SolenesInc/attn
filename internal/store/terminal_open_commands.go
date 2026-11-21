package store

import (
	"time"

	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
)

type TerminalOpenCommand struct {
	Terminal protocol.TerminalID
	Opener   protocol.SessionID
	Command  string
}

func (s *Store) RecordTerminalOpenCommand(command TerminalOpenCommand, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO terminal_open_commands (terminal_id, opener_session_id, command, created_at) VALUES (?, ?, ?, ?)`,
		command.Terminal, command.Opener, command.Command, at.UTC().Format(sortableTimeFormat))
	return err
}

func (s *Store) TerminalOpenCommands() ([]TerminalOpenCommand, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT terminal_id, opener_session_id, command FROM terminal_open_commands ORDER BY created_at, terminal_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var commands []TerminalOpenCommand
	for rows.Next() {
		var command TerminalOpenCommand
		if err := rows.Scan(&command.Terminal, &command.Opener, &command.Command); err != nil {
			return nil, err
		}
		commands = append(commands, command)
	}
	return commands, rows.Err()
}

func (s *Store) ForgetTerminalOpenCommand(terminal protocol.TerminalID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM terminal_open_commands WHERE terminal_id = ?`, terminal)
	return err
}

func (s *Store) NoticeTerminalOpenCommandNotTyped(terminal protocol.TerminalID, notice inbox.Item, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := putInbox(tx, notice, at); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM terminal_open_commands WHERE terminal_id = ?`, terminal); err != nil {
		return err
	}
	return tx.Commit()
}
