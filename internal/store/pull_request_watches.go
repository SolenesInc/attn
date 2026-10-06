package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/prreadiness"
)

type PullRequestWatch struct {
	To            inbox.Address
	SessionID     protocol.SessionID
	PRID          string
	Mode          prreadiness.Mode
	Reviewer      string
	CreatedAt     string
	Cursor        prreadiness.Cursor
	LastSuccessAt string
	LastError     string
	FeedbackError string
	OutageActive  bool
}

const pullRequestWatchColumns = `address, session_id, pr_id, mode, reviewer, created_at,
	cursor_json, last_success_at, last_error, feedback_error, outage_active`

func PullRequestWatchCoalesceKey(prID string) string { return "pull-request-watch:" + prID }

func PullRequestWatchOutageCoalesceKey(prID string) string { return "pull-request-outage:" + prID }

func (s *Store) WatchPullRequest(rec SessionPullRequestRecord, to inbox.Address, mode prreadiness.Mode, reviewer string, at time.Time) (bool, bool, error) {
	if err := prreadiness.ValidateConfig(mode, reviewer); err != nil {
		return false, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, false, errors.New("store has no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	recorded, err := recordSessionPullRequest(tx, rec, at)
	if err != nil {
		return false, false, err
	}

	var currentMode, currentReviewer, cursorJSON string
	err = tx.QueryRow(`SELECT mode, reviewer, cursor_json FROM pull_request_watches WHERE address=? AND pr_id=?`, to.String(), rec.PRID).Scan(&currentMode, &currentReviewer, &cursorJSON)
	if err == nil && currentMode == string(mode) && strings.EqualFold(currentReviewer, reviewer) {
		if err := tx.Commit(); err != nil {
			return false, false, err
		}
		return recorded, false, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, false, err
	}
	cursor := prreadiness.Cursor{}
	if err == nil {
		var existing prreadiness.Cursor
		if json.Unmarshal([]byte(cursorJSON), &existing) == nil {
			cursor = prreadiness.PreserveFeedbackCursor(existing)
		}
	}
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return false, false, err
	}
	stamp := at.UTC().Format(sortableTimeFormat)
	if _, err := tx.Exec(`
		INSERT INTO pull_request_watches(address,session_id,pr_id,mode,reviewer,created_at,cursor_json)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(address,pr_id) DO UPDATE SET
			mode=excluded.mode, reviewer=excluded.reviewer, created_at=excluded.created_at,
			cursor_json=excluded.cursor_json, last_success_at='', last_error='', feedback_error='', outage_active=0
	`, to.String(), rec.SessionID, rec.PRID, string(mode), strings.TrimSpace(reviewer), stamp, string(encoded)); err != nil {
		return false, false, err
	}
	if err := clearUnreadPullRequestStateItems(tx, to, rec.PRID); err != nil {
		return false, false, err
	}
	if _, err := tx.Exec(`
		UPDATE session_pull_requests SET readiness_state='', readiness_reason='', settling_until='',
			watch_health='', watch_error='', watch_last_checked_at='', status_checked_at=''
		WHERE session_id=(SELECT session_id FROM pull_request_watches WHERE address=? AND pr_id=?) AND pr_id=?
	`, to.String(), rec.PRID, rec.PRID); err != nil {
		return false, false, err
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	return recorded, true, nil
}

func (s *Store) StopPullRequestWatch(to inbox.Address, prID string) (bool, error) {
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
	var sessionID protocol.SessionID
	if err := tx.QueryRow(`SELECT session_id FROM pull_request_watches WHERE address=? AND pr_id=?`, to.String(), prID).Scan(&sessionID); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := clearUnreadPullRequestItems(tx, to, prID); err != nil {
		return false, err
	}
	result, err := tx.Exec(`DELETE FROM pull_request_watches WHERE address=? AND pr_id=?`, to.String(), prID)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return false, err
	}
	if _, err := tx.Exec(`
		UPDATE session_pull_requests SET readiness_state='', readiness_reason='', settling_until='',
			watch_health='', watch_error='', watch_last_checked_at=''
		WHERE session_id=? AND pr_id=?
	`, sessionID, prID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func clearUnreadPullRequestItems(tx *sql.Tx, to inbox.Address, prID string) error {
	_, err := tx.Exec(`
		DELETE FROM inbox_items
		WHERE address=? AND kind=? AND source_id=? AND read_at=''
	`, to.String(), inbox.Notice, prID)
	return err
}

func clearUnreadPullRequestStateItems(tx *sql.Tx, to inbox.Address, prID string) error {
	_, err := tx.Exec(`
		DELETE FROM inbox_items
		WHERE address=? AND kind=? AND source_id=? AND read_at=''
			AND coalesce_key IN (?, ?)
	`, to.String(), inbox.Notice, prID,
		PullRequestWatchCoalesceKey(prID), PullRequestWatchOutageCoalesceKey(prID))
	return err
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

func (s *Store) PullRequestWatch(to inbox.Address, prID string) (PullRequestWatch, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return PullRequestWatch{}, false
	}
	watch, err := scanPullRequestWatch(s.db.QueryRow(`SELECT `+pullRequestWatchColumns+` FROM pull_request_watches WHERE address=? AND pr_id=?`, to.String(), prID))
	return watch, err == nil
}

func (s *Store) WatchedSessionPullRequests() []SessionPullRequestRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil
	}
	rows, err := s.db.Query(`
		SELECT ` + sessionPullRequestColumns + ` FROM session_pull_requests pr
		WHERE EXISTS(SELECT 1 FROM pull_request_watches w WHERE w.session_id=pr.session_id AND w.pr_id=pr.pr_id)
		ORDER BY pr.created_at DESC, pr.rowid DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	records, _ := scanSessionPullRequests(rows)
	return records
}

func (s *Store) SessionPullRequestSessionIDs(prID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT session_id FROM session_pull_requests WHERE pr_id=? ORDER BY session_id`, prID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type PullRequestWatchReconcile struct {
	To            inbox.Address
	SessionID     protocol.SessionID
	PRID          string
	CreatedAt     string
	Mode          prreadiness.Mode
	Reviewer      string
	Cursor        prreadiness.Cursor
	Status        SessionPullRequestStatus
	Evaluation    prreadiness.Evaluation
	Health        string
	HealthError   string
	FeedbackError string
	ClearAction   bool
	Terminal      bool
	MailboxItems  []inbox.Item
	At            time.Time
}

func (s *Store) ReconcilePullRequestWatch(update PullRequestWatchReconcile) ([]InboxDelivery, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	encoded, err := json.Marshal(update.Cursor)
	if err != nil {
		return nil, false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var mode, reviewer, createdAt string
	if err := tx.QueryRow(`SELECT mode, reviewer, created_at FROM pull_request_watches WHERE address=? AND pr_id=?`, update.To.String(), update.PRID).Scan(&mode, &reviewer, &createdAt); err != nil {
		return nil, false, err
	}
	if mode != string(update.Mode) || !strings.EqualFold(reviewer, update.Reviewer) || createdAt != update.CreatedAt {
		return nil, false, sql.ErrNoRows
	}
	stamp := update.At.UTC().Format(sortableTimeFormat)
	settling := ""
	if update.Evaluation.SettlingUntil != nil {
		settling = update.Evaluation.SettlingUntil.UTC().Format(sortableTimeFormat)
	}
	var previousStatus SessionPullRequestStatus
	var previousReadinessState, previousReadinessReason, previousSettling, previousHealth, previousHealthError string
	if err := tx.QueryRow(`
		SELECT title, draft, state, ci_status, review_status, mergeable_state, head_sha, head_branch,
			readiness_state, readiness_reason, settling_until, watch_health, watch_error
		FROM session_pull_requests WHERE session_id=? AND pr_id=?
	`, update.SessionID, update.PRID).Scan(
		&previousStatus.Title, &previousStatus.Draft, &previousStatus.State, &previousStatus.CIStatus,
		&previousStatus.ReviewStatus, &previousStatus.MergeableState, &previousStatus.HeadSHA, &previousStatus.HeadBranch,
		&previousReadinessState, &previousReadinessReason, &previousSettling, &previousHealth, &previousHealthError,
	); err != nil {
		return nil, false, err
	}
	projectionChanged := previousStatus != update.Status ||
		previousReadinessState != update.Evaluation.State || previousReadinessReason != update.Evaluation.Reason ||
		previousSettling != settling || previousHealth != update.Health || previousHealthError != update.HealthError
	recovered := update.Health == "current"
	if _, err := tx.Exec(`
		UPDATE pull_request_watches SET cursor_json=?, last_success_at=?,
			last_error=CASE WHEN ? THEN '' ELSE last_error END,
			feedback_error=?, outage_active=CASE WHEN ? THEN 0 ELSE outage_active END
		WHERE address=? AND pr_id=?
	`, string(encoded), stamp, recovered, update.FeedbackError, recovered, update.To.String(), update.PRID); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(`
		UPDATE session_pull_requests SET title=?, draft=?, state=?, ci_status=?, review_status=?, mergeable_state=?,
			head_sha=?, head_branch=?, status_fetched_at=?, status_checked_at=?, readiness_state=?, readiness_reason=?,
			settling_until=?, watch_health=?, watch_error=?, watch_last_checked_at=?
		WHERE session_id=? AND pr_id=?
	`, update.Status.Title, update.Status.Draft, update.Status.State, update.Status.CIStatus, update.Status.ReviewStatus,
		update.Status.MergeableState, update.Status.HeadSHA, update.Status.HeadBranch, stamp, stamp,
		update.Evaluation.State, update.Evaluation.Reason, settling, update.Health, update.HealthError, stamp,
		update.SessionID, update.PRID); err != nil {
		return nil, false, err
	}
	if recovered {
		if _, err := tx.Exec(`
			DELETE FROM inbox_items WHERE address=? AND kind=? AND coalesce_key=? AND read_at=''
		`, update.To.String(), inbox.Notice, PullRequestWatchOutageCoalesceKey(update.PRID)); err != nil {
			return nil, false, err
		}
	}
	if update.ClearAction {
		if _, err := tx.Exec(`
			DELETE FROM inbox_items WHERE address=? AND kind=? AND coalesce_key=? AND read_at=''
		`, update.To.String(), inbox.Notice, PullRequestWatchCoalesceKey(update.PRID)); err != nil {
			return nil, false, err
		}
	}
	deliveries := make([]InboxDelivery, 0, len(update.MailboxItems))
	for _, item := range update.MailboxItems {
		if err := putInbox(tx, item, update.At); err != nil {
			return nil, false, err
		}
		deliveries = append(deliveries, InboxDelivery{Item: InboxItem{Item: item}})
	}

	if update.Terminal {
		if _, err := tx.Exec(`DELETE FROM pull_request_watches WHERE address=? AND pr_id=?`, update.To.String(), update.PRID); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return deliveries, projectionChanged, nil
}

func (s *Store) RecordPullRequestWatchFailure(to inbox.Address, sessionID protocol.SessionID, prID string, createdAt string, mode prreadiness.Mode, reviewer, message string, outage inbox.Item, at time.Time) (*InboxDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var currentMode, currentReviewer, currentCreatedAt string
	var active bool
	if err := tx.QueryRow(`SELECT mode, reviewer, created_at, outage_active FROM pull_request_watches WHERE address=? AND pr_id=?`, to.String(), prID).Scan(&currentMode, &currentReviewer, &currentCreatedAt, &active); err != nil {
		return nil, err
	}
	if currentMode != string(mode) || !strings.EqualFold(currentReviewer, reviewer) || currentCreatedAt != createdAt {
		return nil, sql.ErrNoRows
	}
	stamp := at.UTC().Format(sortableTimeFormat)
	if _, err := tx.Exec(`UPDATE pull_request_watches SET last_error=?, outage_active=1 WHERE address=? AND pr_id=?`, message, to.String(), prID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		UPDATE session_pull_requests SET watch_health='delayed', watch_error=?, watch_last_checked_at=?, status_checked_at=?
		WHERE session_id=? AND pr_id=?
	`, message, stamp, stamp, sessionID, prID); err != nil {
		return nil, err
	}
	var delivery *InboxDelivery
	if !active {
		if err := putInbox(tx, outage, at); err != nil {
			return nil, err
		}
		delivery = &InboxDelivery{Item: InboxItem{Item: outage}}

	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return delivery, nil
}

type pullRequestWatchScanner interface{ Scan(...any) error }

func scanPullRequestWatch(row pullRequestWatchScanner) (PullRequestWatch, error) {
	var watch PullRequestWatch
	var address, mode, cursorJSON string
	if err := row.Scan(&address, &watch.SessionID, &watch.PRID, &mode, &watch.Reviewer, &watch.CreatedAt,
		&cursorJSON, &watch.LastSuccessAt, &watch.LastError, &watch.FeedbackError, &watch.OutageActive); err != nil {
		return PullRequestWatch{}, err
	}
	var err error
	watch.To, err = inbox.ParseAddress(address)
	if err != nil {
		return PullRequestWatch{}, err
	}
	watch.Mode = prreadiness.Mode(mode)
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
