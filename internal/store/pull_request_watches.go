package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type PullRequestWatch struct {
	SessionID          string
	PRID               string
	Reviewer           string
	CreatedAt          string
	LastHeadSHA        string
	HeadObservedAt     string
	LastObservationKey string
	LastSuccessAt      string
	LastError          string
	ErrorSince         string
	FailureCount       int
	FeedbackSeenAt     string
	FeedbackSeenIDs    []string
}

const pullRequestWatchColumns = `session_id, pr_id, reviewer, created_at,
	last_head_sha, head_observed_at, last_observation_key, last_success_at, last_error, error_since, failure_count,
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
	_, err = s.db.Exec(`
		INSERT INTO pull_request_watches (session_id, pr_id, reviewer, created_at, feedback_seen_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id, pr_id) DO UPDATE SET
			reviewer = excluded.reviewer,
			created_at = excluded.created_at,
			last_head_sha = '',
			head_observed_at = '',
			last_observation_key = '',
			last_success_at = '',
			last_error = '',
			error_since = '',
			failure_count = 0,
			feedback_seen_at = excluded.feedback_seen_at,
			feedback_seen_ids = '[]'
	`, sessionID, prID, reviewer, at.UTC().Format(sortableTimeFormat), at.UTC().Format(sortableTimeFormat))
	if err != nil {
		return false, err
	}
	return true, err
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

func (s *Store) RecordPullRequestWatchSuccess(
	sessionID, prID, headSHA, observationKey string, feedbackSeenAt time.Time, feedbackSeenIDs []string, at time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	feedbackIDsJSON, err := json.Marshal(feedbackSeenIDs)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		UPDATE pull_request_watches
		SET head_observed_at = CASE WHEN last_head_sha != ? OR head_observed_at = '' THEN ? ELSE head_observed_at END,
		    last_head_sha = ?, last_observation_key = ?, last_success_at = ?,
		    last_error = '', error_since = '', failure_count = 0,
		    feedback_seen_at = ?, feedback_seen_ids = ?
		WHERE session_id = ? AND pr_id = ?
	`, headSHA, at.UTC().Format(sortableTimeFormat), headSHA, observationKey,
		at.UTC().Format(sortableTimeFormat), feedbackSeenAt.UTC().Format(sortableTimeFormat), string(feedbackIDsJSON), sessionID, prID)
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
	var feedbackSeenIDs string
	err := row.Scan(
		&watch.SessionID, &watch.PRID, &watch.Reviewer, &watch.CreatedAt,
		&watch.LastHeadSHA, &watch.HeadObservedAt, &watch.LastObservationKey, &watch.LastSuccessAt,
		&watch.LastError, &watch.ErrorSince, &watch.FailureCount, &watch.FeedbackSeenAt, &feedbackSeenIDs,
	)
	if err != nil {
		return PullRequestWatch{}, err
	}
	if err := json.Unmarshal([]byte(feedbackSeenIDs), &watch.FeedbackSeenIDs); err != nil {
		return PullRequestWatch{}, err
	}
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
