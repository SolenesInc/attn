package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/prreadiness"
)

type PullRequestWatch struct {
	SessionID     string
	PRID          string
	Reviewer      string
	CreatedAt     string
	Cursor        prreadiness.Cursor
	LastSuccessAt string
	LastError     string
	ErrorSince    string
	FailureCount  int
}

const pullRequestWatchColumns = `session_id, pr_id, reviewer, created_at,
	cursor_json, last_success_at, last_error, error_since, failure_count`

func (s *Store) WatchPullRequest(sessionID, prID, reviewer string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, errors.New("store has no database")
	}
	reviewer = prreadiness.NormalizeActor(reviewer)
	var current string
	err := s.db.QueryRow(`SELECT reviewer FROM pull_request_watches WHERE session_id = ? AND pr_id = ?`, sessionID, prID).Scan(&current)
	if err == nil && prreadiness.SameActor(current, reviewer) {
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
			cursor_json = '{}',
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
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`DELETE FROM pull_request_watches WHERE session_id = ? AND pr_id = ?`, sessionID, prID)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return false, err
	}
	if _, err := tx.Exec(`
		UPDATE session_pull_requests SET review_status = '', status_checked_at = ''
		WHERE session_id = ? AND pr_id = ?
	`, sessionID, prID); err != nil {
		return false, err
	}
	return true, tx.Commit()
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

func (s *Store) ApplyPullRequestWatchBaseline(
	sessionID, prID, coalesceKey string,
	cursor prreadiness.Cursor,
	clearAction bool,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cursorJSON, err := json.Marshal(cursor)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if clearAction {
		if _, err := tx.Exec(`
			DELETE FROM agent_mailbox_items
			WHERE recipient_session_id = ? AND kind = ? AND coalesce_key = ? AND read_at = ''
		`, sessionID, agentmailbox.KindMaintenancePrompt, coalesceKey); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			UPDATE session_pull_requests SET review_status = 'waiting'
			WHERE session_id = ? AND pr_id = ?
		`, sessionID, prID); err != nil {
			return err
		}
	}
	result, err := tx.Exec(`
		UPDATE pull_request_watches SET cursor_json = ? WHERE session_id = ? AND pr_id = ?
	`, string(cursorJSON), sessionID, prID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil || changed == 0 {
		if err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) RecordPullRequestWatchSuccess(
	sessionID, prID string,
	cursor prreadiness.Cursor,
	reviewStatus prreadiness.ReviewState,
	at time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cursorJSON, err := json.Marshal(cursor)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`
		UPDATE pull_request_watches
		SET cursor_json = ?, last_success_at = ?, last_error = '', error_since = '', failure_count = 0
		WHERE session_id = ? AND pr_id = ?
	`, string(cursorJSON), at.UTC().Format(sortableTimeFormat), sessionID, prID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		return sql.ErrNoRows
	}
	if _, err := tx.Exec(`
		UPDATE session_pull_requests SET review_status = ? WHERE session_id = ? AND pr_id = ?
	`, string(reviewStatus), sessionID, prID); err != nil {
		return err
	}
	return tx.Commit()
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
	var cursorJSON string
	err := row.Scan(
		&watch.SessionID, &watch.PRID, &watch.Reviewer, &watch.CreatedAt,
		&cursorJSON, &watch.LastSuccessAt, &watch.LastError, &watch.ErrorSince, &watch.FailureCount,
	)
	if err != nil {
		return PullRequestWatch{}, err
	}
	if err := json.Unmarshal([]byte(cursorJSON), &watch.Cursor); err != nil {
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
