package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	UpdateRequestPending  = "pending"
	UpdateRequestStarted  = "started"
	UpdateRequestDeclined = "declined"
)

type UpdateRun struct {
	ID          string
	TargetBuild string
	InstallKind string
	Phase       string
	Outcome     string
	LogPath     string
	CreatedAt   time.Time
	FinishedAt  time.Time
	Requesters  []string
}

func (r UpdateRun) Open() bool { return r.FinishedAt.IsZero() }

type UpdateRequest struct {
	ID             string
	Requester      string
	Reason         string
	NotificationID string
}

func (s *Store) CreateUpdateRun(id, installKind, phase, logPath string, now time.Time) (UpdateRun, error) {
	run := UpdateRun{ID: id, InstallKind: installKind, Phase: phase, LogPath: logPath, CreatedAt: now.UTC()}
	if _, err := s.db.Exec(
		`INSERT INTO update_runs (id, install_kind, phase, log_path, created_at) VALUES (?, ?, ?, ?, ?)`,
		run.ID, run.InstallKind, run.Phase, run.LogPath, run.CreatedAt.Format(sortableTimeFormat),
	); err != nil {
		return UpdateRun{}, fmt.Errorf("store: create update run: %w", err)
	}
	return run, nil
}

func (s *Store) SetUpdateRunPhase(id, phase string) error {
	_, err := s.db.Exec(`UPDATE update_runs SET phase = ? WHERE id = ?`, phase, id)
	return err
}

func (s *Store) SetUpdateRunTarget(id, target string) error {
	_, err := s.db.Exec(`UPDATE update_runs SET target_build = ? WHERE id = ?`, target, id)
	return err
}

func (s *Store) FinishUpdateRun(id, phase, outcome string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE update_runs SET phase = ?, outcome = ?, finished_at = ? WHERE id = ?`,
		phase, outcome, now.UTC().Format(sortableTimeFormat), id)
	return err
}

// LatestUpdateRun returns nil when no update has ever run.
func (s *Store) LatestUpdateRun() (*UpdateRun, error) {
	var run UpdateRun
	var created, finished string
	err := s.db.QueryRow(
		`SELECT id, target_build, install_kind, phase, outcome, log_path, created_at, finished_at
		 FROM update_runs ORDER BY created_at DESC, id DESC LIMIT 1`,
	).Scan(&run.ID, &run.TargetBuild, &run.InstallKind, &run.Phase, &run.Outcome, &run.LogPath, &created, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: latest update run: %w", err)
	}
	run.CreatedAt = parseUpdateRunTime(created)
	run.FinishedAt = parseUpdateRunTime(finished)
	if run.Requesters, err = s.updateRunRequesters(run.ID); err != nil {
		return nil, err
	}
	return &run, nil
}

func parseUpdateRunTime(value string) time.Time {
	parsed, err := time.Parse(sortableTimeFormat, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func (s *Store) updateRunRequesters(runID string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT requester FROM update_requests WHERE run_id = ? AND requester != '' ORDER BY requester`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: update run requesters: %w", err)
	}
	defer rows.Close()
	var requesters []string
	for rows.Next() {
		var requester string
		if err := rows.Scan(&requester); err != nil {
			return nil, err
		}
		requesters = append(requesters, requester)
	}
	return requesters, rows.Err()
}

func (s *Store) AddUpdateRequest(request UpdateRequest, state, runID string, now time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO update_requests (id, requester, reason, state, run_id, notification_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		request.ID, request.Requester, request.Reason, state, runID, request.NotificationID, now.UTC().Format(sortableTimeFormat),
	)
	if err != nil {
		return fmt.Errorf("store: add update request: %w", err)
	}
	return nil
}

func (s *Store) PendingUpdateRequests() ([]UpdateRequest, error) {
	rows, err := s.db.Query(
		`SELECT id, requester, reason, notification_id FROM update_requests WHERE state = ? ORDER BY created_at, id`, UpdateRequestPending)
	if err != nil {
		return nil, fmt.Errorf("store: pending update requests: %w", err)
	}
	defer rows.Close()
	var requests []UpdateRequest
	for rows.Next() {
		var request UpdateRequest
		if err := rows.Scan(&request.ID, &request.Requester, &request.Reason, &request.NotificationID); err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (s *Store) AttachPendingUpdateRequests(runID string) error {
	_, err := s.db.Exec(`UPDATE update_requests SET state = ?, run_id = ? WHERE state = ?`, UpdateRequestStarted, runID, UpdateRequestPending)
	return err
}

func (s *Store) DeclinePendingUpdateRequests() error {
	_, err := s.db.Exec(`UPDATE update_requests SET state = ? WHERE state = ?`, UpdateRequestDeclined, UpdateRequestPending)
	return err
}
