package store

import (
	"database/sql"
	"errors"
	"time"
)

// One pull request an agent opened from inside a session. Everything below
// CreatedAt is status the refresh job fills in; a fresh row carries only `open`.
type SessionPullRequestRecord struct {
	SessionID       string
	PRID            string // host:owner/repo#number, the inbox's id format
	Repository      string // host/owner/repository
	Number          int
	URL             string
	CreatedAt       string
	Title           string
	Draft           bool
	State           string
	CIStatus        string
	ReviewStatus    string
	MergeableState  string
	HeadSHA         string
	HeadBranch      string
	StatusFetchedAt string
	LastActivityAt  string
	// Pacing cursor, moved on every attempt; StatusFetchedAt is the last status that landed.
	StatusCheckedAt string
}

type SessionPullRequestStatus struct {
	Title          string
	Draft          bool
	State          string
	CIStatus       string
	ReviewStatus   string
	MergeableState string
	HeadSHA        string
	HeadBranch     string
}

const sessionPullRequestColumns = `session_id, pr_id, repository, number, url, created_at, title, draft,
	state, ci_status, review_status, mergeable_state, head_sha, head_branch, status_fetched_at, last_activity_at,
	status_checked_at`

// Reports false when the row was already there; a hook and a manual `attn pr
// record` reporting the same pull request is normal.
func (s *Store) RecordSessionPullRequest(rec SessionPullRequestRecord, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, errors.New("store has no database")
	}

	stamp := now.Format(time.RFC3339Nano)
	result, err := s.db.Exec(`
		INSERT OR IGNORE INTO session_pull_requests
			(session_id, pr_id, repository, number, url, created_at, state, last_activity_at)
		VALUES (?, ?, ?, ?, ?, ?, 'open', ?)`,
		rec.SessionID, rec.PRID, rec.Repository, rec.Number, rec.URL, stamp, stamp)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *Store) ListSessionPullRequests(sessionID string) []SessionPullRequestRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`
		SELECT `+sessionPullRequestColumns+`
		FROM session_pull_requests WHERE session_id = ?
		ORDER BY created_at DESC, rowid DESC`, sessionID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	records, _ := scanSessionPullRequests(rows)
	return records
}

// One query for every session, so decorating a whole broadcast costs one round trip.
func (s *Store) ListSessionPullRequestsBySession() map[string][]SessionPullRequestRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`
		SELECT ` + sessionPullRequestColumns + `
		FROM session_pull_requests
		ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	records, err := scanSessionPullRequests(rows)
	if err != nil {
		return nil
	}
	bySession := make(map[string][]SessionPullRequestRecord)
	for _, rec := range records {
		bySession[rec.SessionID] = append(bySession[rec.SessionID], rec)
	}
	return bySession
}

func (s *Store) OpenSessionPullRequests() []SessionPullRequestRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`
		SELECT ` + sessionPullRequestColumns + `
		FROM session_pull_requests
		WHERE state NOT IN ('merged', 'closed')
		ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	records, _ := scanSessionPullRequests(rows)
	return records
}

func (s *Store) SessionPullRequestByID(prID string) (SessionPullRequestRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return SessionPullRequestRecord{}, false
	}

	rows, err := s.db.Query(`
		SELECT `+sessionPullRequestColumns+`
		FROM session_pull_requests WHERE pr_id = ?
		ORDER BY status_fetched_at DESC, last_activity_at DESC, rowid DESC
		LIMIT 1`, prID)
	if err != nil {
		return SessionPullRequestRecord{}, false
	}
	defer rows.Close()
	records, err := scanSessionPullRequests(rows)
	if err != nil || len(records) == 0 {
		return SessionPullRequestRecord{}, false
	}
	return records[0], true
}

func (s *Store) UpdateSessionPullRequestStatus(prID string, status SessionPullRequestStatus, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("store has no database")
	}

	stamp := at.Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		UPDATE session_pull_requests
		SET title = ?, draft = ?, state = ?, ci_status = ?, review_status = ?,
			mergeable_state = ?, head_sha = ?, head_branch = ?,
			status_fetched_at = ?, status_checked_at = ?
		WHERE pr_id = ?`,
		status.Title, status.Draft, status.State, status.CIStatus, status.ReviewStatus,
		status.MergeableState, status.HeadSHA, status.HeadBranch, stamp, stamp, prID)
	return err
}

func (s *Store) MarkSessionPullRequestChecked(prID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("store has no database")
	}

	_, err := s.db.Exec(
		`UPDATE session_pull_requests SET status_checked_at = ? WHERE pr_id = ?`,
		at.Format(time.RFC3339Nano), prID)
	return err
}

func (s *Store) TouchSessionPullRequestActivity(prID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("store has no database")
	}

	_, err := s.db.Exec(
		`UPDATE session_pull_requests SET last_activity_at = ? WHERE pr_id = ?`,
		at.Format(time.RFC3339Nano), prID)
	return err
}

func (s *Store) ForgetSessionPullRequest(sessionID, prID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, errors.New("store has no database")
	}

	result, err := s.db.Exec(
		`DELETE FROM session_pull_requests WHERE session_id = ? AND pr_id = ?`, sessionID, prID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func scanSessionPullRequests(rows *sql.Rows) ([]SessionPullRequestRecord, error) {
	var records []SessionPullRequestRecord
	for rows.Next() {
		var rec SessionPullRequestRecord
		if err := rows.Scan(
			&rec.SessionID, &rec.PRID, &rec.Repository, &rec.Number, &rec.URL, &rec.CreatedAt,
			&rec.Title, &rec.Draft, &rec.State, &rec.CIStatus, &rec.ReviewStatus,
			&rec.MergeableState, &rec.HeadSHA, &rec.HeadBranch, &rec.StatusFetchedAt, &rec.LastActivityAt,
			&rec.StatusCheckedAt,
		); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}
