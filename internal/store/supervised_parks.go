package store

import (
	"database/sql"
	"time"
)

// The park's timestamp must not become the moment it was restored: everything after
// Child mirrors the supervisor's snapshot so a restored park answers as the original did.
type SupervisedPark struct {
	Child          string
	ParkedAt       time.Time
	RestartAttempt int
	ExitAt         time.Time
	ExitCode       *int
	ExitSignal     string
	ExitError      string
}

func (s *Store) SaveSupervisedPark(park SupervisedPark) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	exitAt := ""
	if !park.ExitAt.IsZero() {
		exitAt = park.ExitAt.UTC().Format(sortableTimeFormat)
	}
	var exitCode any
	if park.ExitCode != nil {
		exitCode = *park.ExitCode
	}
	_, err := s.db.Exec(`
		INSERT INTO supervised_parks (child, parked_at, restart_attempt, exit_at, exit_code, exit_signal, exit_error)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(child) DO UPDATE SET
			parked_at = excluded.parked_at,
			restart_attempt = excluded.restart_attempt,
			exit_at = excluded.exit_at,
			exit_code = excluded.exit_code,
			exit_signal = excluded.exit_signal,
			exit_error = excluded.exit_error
	`, park.Child, park.ParkedAt.UTC().Format(sortableTimeFormat), park.RestartAttempt,
		exitAt, exitCode, park.ExitSignal, park.ExitError)
	return err
}

func (s *Store) GetSupervisedPark(child string) (SupervisedPark, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return SupervisedPark{}, false, nil
	}
	var (
		park     SupervisedPark
		parkedAt string
		exitAt   string
		exitCode sql.NullInt64
	)
	err := s.db.QueryRow(`
		SELECT child, parked_at, restart_attempt, exit_at, exit_code, exit_signal, exit_error
		FROM supervised_parks WHERE child = ?
	`, child).Scan(&park.Child, &parkedAt, &park.RestartAttempt, &exitAt, &exitCode,
		&park.ExitSignal, &park.ExitError)
	switch err {
	case nil:
	case sql.ErrNoRows:
		return SupervisedPark{}, false, nil
	default:
		return SupervisedPark{}, false, err
	}
	park.ParkedAt = parseStoreTime(parkedAt)
	if exitAt != "" {
		park.ExitAt = parseStoreTime(exitAt)
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		park.ExitCode = &code
	}
	return park, true, nil
}

func (s *Store) ClearSupervisedPark(child string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return false, nil
	}
	result, err := s.db.Exec(`DELETE FROM supervised_parks WHERE child = ?`, child)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}
