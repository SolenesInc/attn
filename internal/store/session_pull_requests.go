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
}

const sessionPullRequestColumns = `session_id, pr_id, repository, number, url, created_at, title, draft,
	state, ci_status, review_status, mergeable_state, head_sha, head_branch, status_fetched_at, last_activity_at`

// Reporting the same pull request twice is normal: a hook and a manual `attn pr
// record` both fire. The second keeps the first row, and returns false to say so.
func (s *Store) RecordSessionPullRequest(rec SessionPullRequestRecord, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, errors.New("store has no database")
	}

	result, err := s.db.Exec(`
		INSERT OR IGNORE INTO session_pull_requests
			(session_id, pr_id, repository, number, url, created_at, state)
		VALUES (?, ?, ?, ?, ?, ?, 'open')`,
		rec.SessionID, rec.PRID, rec.Repository, rec.Number, rec.URL, now.Format(time.RFC3339Nano))
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

// One query for every session, so a broadcast decorating hundreds of sessions
// costs the same as decorating one.
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
		); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}
