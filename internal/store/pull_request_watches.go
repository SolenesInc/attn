package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
)

type PullRequestWatch struct {
	SessionID           string
	PRID                string
	Reviewer            string
	CreatedAt           string
	LastHeadSHA         string
	SignalBaselineIDs   []string
	LastObservationKey  string
	LastSuccessAt       string
	LastError           string
	ErrorSince          string
	FailureCount        int
	FeedbackBaselineSet bool
	FeedbackBaselineIDs []string
}

const pullRequestWatchColumns = `session_id, pr_id, reviewer, created_at,
	last_head_sha, signal_baseline_ids, last_observation_key, last_success_at, last_error, error_since, failure_count,
	feedback_seen_at, feedback_seen_ids`

func (s *Store) WatchPullRequest(sessionID, prID, reviewer string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, errors.New("store has no database")
	}
	var current string
	err := s.db.QueryRow(`SELECT reviewer FROM pull_request_watches WHERE session_id = ? AND pr_id = ?`, sessionID, prID).Scan(&current)
	if err == nil && current == reviewer {
		return false, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
		INSERT INTO pull_request_watches (session_id, pr_id, reviewer, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id, pr_id) DO UPDATE SET
			reviewer = excluded.reviewer,
			created_at = excluded.created_at,
			last_head_sha = '',
			signal_baseline_ids = '[]',
			last_observation_key = '',
			last_success_at = '',
			last_error = '',
			error_since = '',
			failure_count = 0
	`, sessionID, prID, reviewer, at.UTC().Format(sortableTimeFormat))
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE session_pull_requests SET review_status = 'waiting' WHERE session_id = ? AND pr_id = ?`, sessionID, prID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) UnwatchPullRequest(sessionID, prID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, errors.New("store has no database")
	}
	result, err := s.db.Exec(`DELETE FROM pull_request_watches WHERE session_id = ? AND pr_id = ?`, sessionID, prID)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

func (s *Store) PullRequestWatches() []PullRequestWatch {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT ` + pullRequestWatchColumns + ` FROM pull_request_watches ORDER BY pr_id, created_at, session_id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	watches, _ := scanPullRequestWatches(rows)
	return watches
}

func (s *Store) PullRequestWatchesByPR(prID string) []PullRequestWatch {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT `+pullRequestWatchColumns+` FROM pull_request_watches WHERE pr_id = ? ORDER BY created_at, session_id`, prID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	watches, _ := scanPullRequestWatches(rows)
	return watches
}

func (s *Store) PullRequestWatch(sessionID, prID string) (PullRequestWatch, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return PullRequestWatch{}, false
	}
	watch, err := scanPullRequestWatch(s.db.QueryRow(
		`SELECT `+pullRequestWatchColumns+` FROM pull_request_watches WHERE session_id = ? AND pr_id = ?`, sessionID, prID))
	return watch, err == nil
}

func (s *Store) BeginPullRequestWatchHead(
	sessionID, prID, headSHA, coalesceKey string, signalBaselineIDs, feedbackBaselineIDs []string, at time.Time,
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	signalBaselineJSON, err := json.Marshal(signalBaselineIDs)
	if err != nil {
		return false, err
	}
	feedbackBaselineJSON, err := json.Marshal(feedbackBaselineIDs)
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentHead string
	if err := tx.QueryRow(`
		SELECT last_head_sha FROM pull_request_watches WHERE session_id = ? AND pr_id = ?
	`, sessionID, prID).Scan(&currentHead); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if currentHead == headSHA {
		return false, nil
	}
	if _, err := tx.Exec(`
		DELETE FROM agent_mailbox_items
		WHERE recipient_session_id = ? AND kind = ? AND coalesce_key = ? AND read_at = ''
	`, sessionID, agentmailbox.KindMaintenancePrompt, coalesceKey); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`
		UPDATE pull_request_watches
		SET last_head_sha = ?, signal_baseline_ids = ?, last_observation_key = '',
		    feedback_seen_at = CASE WHEN feedback_seen_at = '' THEN ? ELSE feedback_seen_at END,
		    feedback_seen_ids = CASE WHEN feedback_seen_at = '' THEN ? ELSE feedback_seen_ids END
		WHERE session_id = ? AND pr_id = ?
	`, headSHA, string(signalBaselineJSON), at.UTC().Format(sortableTimeFormat),
		string(feedbackBaselineJSON), sessionID, prID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`
		UPDATE session_pull_requests SET review_status = 'waiting'
		WHERE session_id = ? AND pr_id = ?
	`, sessionID, prID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) RecordPullRequestWatchSuccess(
	sessionID, prID, observationKey string, feedbackBaselineIDs []string, at time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	feedbackBaselineJSON, err := json.Marshal(feedbackBaselineIDs)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		UPDATE pull_request_watches
		SET last_observation_key = ?, last_success_at = ?,
		    last_error = '', error_since = '', failure_count = 0,
		    feedback_seen_at = CASE WHEN feedback_seen_at = '' THEN ? ELSE feedback_seen_at END,
		    feedback_seen_ids = ?
		WHERE session_id = ? AND pr_id = ?
	`, observationKey, at.UTC().Format(sortableTimeFormat),
		at.UTC().Format(sortableTimeFormat), string(feedbackBaselineJSON), sessionID, prID)
	return err
}

func (s *Store) RecordPullRequestWatchFailure(sessionID, prID, message string, at time.Time) (PullRequestWatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stamp := at.UTC().Format(sortableTimeFormat)
	_, err := s.db.Exec(`
		UPDATE pull_request_watches
		SET last_error = ?, error_since = CASE WHEN error_since = '' THEN ? ELSE error_since END,
		    failure_count = failure_count + 1
		WHERE session_id = ? AND pr_id = ?
	`, message, stamp, sessionID, prID)
	if err != nil {
		return PullRequestWatch{}, err
	}
	watch, err := scanPullRequestWatch(s.db.QueryRow(
		`SELECT `+pullRequestWatchColumns+` FROM pull_request_watches WHERE session_id = ? AND pr_id = ?`, sessionID, prID))
	return watch, err
}

type pullRequestWatchScanner interface {
	Scan(dest ...any) error
}

func scanPullRequestWatch(row pullRequestWatchScanner) (PullRequestWatch, error) {
	var watch PullRequestWatch
	var signalBaselineJSON, feedbackBaselineAt, feedbackBaselineJSON string
	err := row.Scan(
		&watch.SessionID, &watch.PRID, &watch.Reviewer, &watch.CreatedAt,
		&watch.LastHeadSHA, &signalBaselineJSON, &watch.LastObservationKey, &watch.LastSuccessAt,
		&watch.LastError, &watch.ErrorSince, &watch.FailureCount, &feedbackBaselineAt, &feedbackBaselineJSON,
	)
	if err != nil {
		return PullRequestWatch{}, err
	}
	if err := json.Unmarshal([]byte(signalBaselineJSON), &watch.SignalBaselineIDs); err != nil {
		return PullRequestWatch{}, err
	}
	if err := json.Unmarshal([]byte(feedbackBaselineJSON), &watch.FeedbackBaselineIDs); err != nil {
		return PullRequestWatch{}, err
	}
	watch.FeedbackBaselineSet = feedbackBaselineAt != ""
	return watch, nil
}

func scanPullRequestWatches(rows *sql.Rows) ([]PullRequestWatch, error) {
	var watches []PullRequestWatch
	for rows.Next() {
		watch, err := scanPullRequestWatch(rows)
		if err != nil {
			return nil, err
		}
		watches = append(watches, watch)
	}
	return watches, rows.Err()
}
