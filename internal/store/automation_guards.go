package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

type AutomationGuardCheck struct {
	Guard           string
	CheckedAt       time.Time
	Outcome         string
	Detail          string
	ChecksSinceRun  int
	HandledOutput   string
	HandledRunID    string
	HandledRunState string
}

func (s *Store) GetAutomationGuardCheck(definitionID int) (*AutomationGuardCheck, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errors.New("automation persistence unavailable")
	}
	var check AutomationGuardCheck
	var checkedAt string
	err := s.db.QueryRow(`SELECT c.guard,c.checked_at,c.outcome,c.detail,c.checks_since_run,c.handled_output,c.handled_run_id,COALESCE(r.state,'') FROM automation_guard_checks c LEFT JOIN automation_runs r ON r.id=c.handled_run_id WHERE c.definition_id=?`, definitionID).Scan(&check.Guard, &checkedAt, &check.Outcome, &check.Detail, &check.ChecksSinceRun, &check.HandledOutput, &check.HandledRunID, &check.HandledRunState)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	check.CheckedAt, err = docstore.ParseTime(checkedAt)
	return &check, err
}

func (s *Store) RecordAutomationGuardSkip(definitionID int, guard, outcome, detail string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("automation persistence unavailable")
	}
	_, err := s.db.Exec(`INSERT INTO automation_guard_checks(definition_id,guard,checked_at,outcome,detail,checks_since_run) VALUES(?,?,?,?,?,1)
 ON CONFLICT(definition_id) DO UPDATE SET
 handled_output=CASE WHEN excluded.outcome='empty' OR guard!=excluded.guard THEN '' ELSE handled_output END,
 handled_run_id=CASE WHEN excluded.outcome='empty' OR guard!=excluded.guard THEN '' ELSE handled_run_id END,
 guard=excluded.guard,checked_at=excluded.checked_at,outcome=excluded.outcome,detail=excluded.detail,checks_since_run=checks_since_run+1`, definitionID, guard, formatStoredTime(at), outcome, detail)
	return err
}

func recordAutomationGuardLaunchTx(tx *sql.Tx, definitionID int, check *AutomationGuardCheck, runID string, at time.Time) error {
	if check == nil {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO automation_guard_checks(definition_id,guard,checked_at,outcome,handled_output,handled_run_id) VALUES(?,?,?,'launched',?,?)
 ON CONFLICT(definition_id) DO UPDATE SET guard=excluded.guard,checked_at=excluded.checked_at,outcome=excluded.outcome,detail='',checks_since_run=0,handled_output=excluded.handled_output,handled_run_id=excluded.handled_run_id`, definitionID, check.Guard, formatStoredTime(at), check.HandledOutput, runID)
	return err
}
