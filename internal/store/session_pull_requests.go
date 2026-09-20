package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

type SessionPullRequestRecord struct {
	SessionID       string
	PRID            string
	Repository      string
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

func (s *Store) SessionPullRequestSessionIDs(prID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errors.New("store has no database")
	}
	rows, err := s.db.Query(`
		SELECT session_id FROM session_pull_requests
		WHERE pr_id = ? ORDER BY session_id`, prID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessionIDs []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, err
		}
		sessionIDs = append(sessionIDs, sessionID)
	}
	return sessionIDs, rows.Err()
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

func (s *Store) WatchedSessionPullRequests() []SessionPullRequestRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`
		SELECT ` + sessionPullRequestColumns + `
		FROM session_pull_requests
		WHERE EXISTS (
			SELECT 1 FROM pull_request_watches
			WHERE pull_request_watches.pr_id = session_pull_requests.pr_id
		)
		ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	records, _ := scanSessionPullRequests(rows)
	return records
}

func (s *Store) OpenSessionPullRequestsReferencedBy(
	schema docstore.CollectionSchema, field string,
) ([]SessionPullRequestRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errors.New("store has no database")
	}
	table, err := s.documentTable(schema)
	if err != nil {
		return nil, err
	}
	found := false
	for _, spec := range schema.Fields {
		if spec.Name != field {
			continue
		}
		if spec.Type != docstore.FieldString {
			return nil, fmt.Errorf("store: %s/%s field %q is %s, want string", schema.Namespace, schema.Collection, field, spec.Type)
		}
		found = true
		break
	}
	if !found {
		return nil, fmt.Errorf("store: %s/%s has no field %q", schema.Namespace, schema.Collection, field)
	}

	rows, err := s.db.Query(openSessionPullRequestsReferencedByQuery(table, docstore.FieldColumn(field)),
		protocol.SessionStateRecoverable)
	if err != nil {
		return nil, fmt.Errorf("store: selecting refreshable session pull requests: %w", err)
	}
	defer rows.Close()
	records, err := scanSessionPullRequests(rows)
	if err != nil {
		return nil, fmt.Errorf("store: selecting refreshable session pull requests: %w", err)
	}
	return records, nil
}

func openSessionPullRequestsReferencedByQuery(table, column string) string {
	return `
		SELECT ` + sessionPullRequestColumns + `
		FROM session_pull_requests
		WHERE state NOT IN ('merged', 'closed')
		AND (
			EXISTS (
				SELECT 1 FROM sessions
				WHERE sessions.id = session_pull_requests.session_id
				AND sessions.closed_at = ''
				AND sessions.state != ?
			)
			OR EXISTS (
				SELECT 1 FROM ` + table + `
				WHERE ` + quoteIdent(column) + ` = session_pull_requests.pr_id
			)
		)
		ORDER BY created_at DESC, rowid DESC`
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

func (s *Store) UpdateSessionPullRequestSharedStatus(prID string, status SessionPullRequestStatus, at time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("store has no database")
	}

	stamp := at.Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`
		SELECT session_id FROM session_pull_requests
		WHERE pr_id = ? AND review_status <> ''
		ORDER BY session_id`, prID)
	if err != nil {
		return nil, err
	}
	var cleared []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			rows.Close()
			return nil, err
		}
		cleared = append(cleared, sessionID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		UPDATE session_pull_requests
		SET title = ?, draft = ?, state = ?, ci_status = ?, review_status = '',
			mergeable_state = ?, head_sha = ?, head_branch = ?,
			status_fetched_at = ?, status_checked_at = ?
		WHERE pr_id = ?`,
		status.Title, status.Draft, status.State, status.CIStatus,
		status.MergeableState, status.HeadSHA, status.HeadBranch, stamp, stamp, prID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return cleared, nil
}

func (s *Store) UpdateSessionPullRequestReviewStatus(sessionID, prID, reviewStatus string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("store has no database")
	}
	_, err := s.db.Exec(
		`UPDATE session_pull_requests SET review_status = ? WHERE session_id = ? AND pr_id = ?`,
		reviewStatus, sessionID, prID)
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

	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		DELETE FROM agent_mailbox_items
		WHERE recipient_session_id = ? AND kind = ? AND source_id = ? AND read_at = ''
	`, sessionID, agentmailbox.KindMaintenancePrompt, prID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(
		`DELETE FROM pull_request_watches WHERE session_id = ? AND pr_id = ?`, sessionID, prID); err != nil {
		return false, err
	}
	result, err := tx.Exec(
		`DELETE FROM session_pull_requests WHERE session_id = ? AND pr_id = ?`, sessionID, prID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	return true, tx.Commit()
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
