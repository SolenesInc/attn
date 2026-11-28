package store

import (
	"time"
)

// StartedViewCommand is a command of a view that the user started: its line
// and the sha256 of the files it names, as they were when started.
type StartedViewCommand struct {
	Line         string
	ScriptSHA256 string
}

// StartViewCommands records commands as the started commands of the view on
// machineID in the profile, in place of those started before.
func (s *Store) StartViewCommands(profileID, view, machineID string, commands []StartedViewCommand, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM view_started WHERE profile_id = ? AND view = ? AND machine_id = ?`, profileID, view, machineID); err != nil {
		return err
	}
	for _, command := range commands {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO view_started (profile_id, view, machine_id, line, script_sha256, started_at) VALUES (?, ?, ?, ?, ?, ?)`,
			profileID, view, machineID, command.Line, command.ScriptSHA256, at.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// StartedViewCommands is the commands started for the view on machineID in
// any of the profiles.
func (s *Store) StartedViewCommands(view, machineID string, profileIDs []string) ([]StartedViewCommand, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var started []StartedViewCommand
	for _, profileID := range profileIDs {
		rows, err := s.db.Query(`SELECT line, script_sha256 FROM view_started WHERE profile_id = ? AND view = ? AND machine_id = ? ORDER BY line`, profileID, view, machineID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var command StartedViewCommand
			if err := rows.Scan(&command.Line, &command.ScriptSHA256); err != nil {
				rows.Close()
				return nil, err
			}
			started = append(started, command)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return started, nil
}
