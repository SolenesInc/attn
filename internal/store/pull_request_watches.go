package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/prreadiness"
)

type PullRequestWatch struct {
	SessionID     string
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

const pullRequestWatchColumns = `session_id, pr_id, mode, reviewer, created_at,
	cursor_json, last_success_at, last_error, feedback_error, outage_active`

func PullRequestWatchCoalesceKey(prID string) string { return "pull-request-watch:" + prID }

func PullRequestWatchOutageCoalesceKey(prID string) string { return "pull-request-outage:" + prID }

func (s *Store) WatchPullRequest(rec SessionPullRequestRecord, mode prreadiness.Mode, reviewer string, at time.Time) (bool, bool, error) {
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
	err = tx.QueryRow(`SELECT mode, reviewer, cursor_json FROM pull_request_watches WHERE session_id=? AND pr_id=?`, rec.SessionID, rec.PRID).Scan(&currentMode, &currentReviewer, &cursorJSON)
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
		INSERT INTO pull_request_watches(session_id,pr_id,mode,reviewer,created_at,cursor_json)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(session_id,pr_id) DO UPDATE SET
			mode=excluded.mode, reviewer=excluded.reviewer, created_at=excluded.created_at,
			cursor_json=excluded.cursor_json, last_success_at='', last_error='', feedback_error='', outage_active=0
	`, rec.SessionID, rec.PRID, string(mode), strings.TrimSpace(reviewer), stamp, string(encoded)); err != nil {
		return false, false, err
	}
	if err := clearUnreadPullRequestStateItems(tx, rec.SessionID, rec.PRID); err != nil {
		return false, false, err
	}
	if _, err := tx.Exec(`
		UPDATE session_pull_requests SET readiness_state='', readiness_reason='', settling_until='',
			watch_health='', watch_error='', watch_last_checked_at='', status_checked_at=''
		WHERE session_id=? AND pr_id=?
	`, rec.SessionID, rec.PRID); err != nil {
		return false, false, err
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	return recorded, true, nil
}

func (s *Store) StopPullRequestWatch(sessionID, prID string) (bool, error) {
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
	if err := clearUnreadPullRequestItems(tx, sessionID, prID); err != nil {
		return false, err
	}
	result, err := tx.Exec(`DELETE FROM pull_request_watches WHERE session_id=? AND pr_id=?`, sessionID, prID)
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

func clearUnreadPullRequestItems(tx *sql.Tx, sessionID, prID string) error {
	_, err := tx.Exec(`
		DELETE FROM agent_mailbox_items
		WHERE recipient_session_id=? AND kind=? AND source_id=? AND read_at=''
	`, sessionID, agentmailbox.KindMaintenancePrompt, prID)
	return err
}

func clearUnreadPullRequestStateItems(tx *sql.Tx, sessionID, prID string) error {
	_, err := tx.Exec(`
		DELETE FROM agent_mailbox_items
		WHERE recipient_session_id=? AND kind=? AND source_id=? AND read_at=''
			AND coalesce_key IN (?, ?)
	`, sessionID, agentmailbox.KindMaintenancePrompt, prID,
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

func (s *Store) PullRequestWatch(sessionID, prID string) (PullRequestWatch, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return PullRequestWatch{}, false
	}
	watch, err := scanPullRequestWatch(s.db.QueryRow(`SELECT `+pullRequestWatchColumns+` FROM pull_request_watches WHERE session_id=? AND pr_id=?`, sessionID, prID))
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
	SessionID     string
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
	MailboxItems  []agentmailbox.Item
	At            time.Time
}

func (s *Store) ReconcilePullRequestWatch(update PullRequestWatchReconcile) ([]agentmailbox.Delivery, bool, error) {
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
	if err := tx.QueryRow(`SELECT mode, reviewer, created_at FROM pull_request_watches WHERE session_id=? AND pr_id=?`, update.SessionID, update.PRID).Scan(&mode, &reviewer, &createdAt); err != nil {
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
		WHERE session_id=? AND pr_id=?
	`, string(encoded), stamp, recovered, update.FeedbackError, recovered, update.SessionID, update.PRID); err != nil {
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
			DELETE FROM agent_mailbox_items WHERE recipient_session_id=? AND kind=? AND coalesce_key=? AND read_at=''
		`, update.SessionID, agentmailbox.KindMaintenancePrompt, PullRequestWatchOutageCoalesceKey(update.PRID)); err != nil {
			return nil, false, err
		}
	}
	if update.ClearAction {
		if _, err := tx.Exec(`
			DELETE FROM agent_mailbox_items WHERE recipient_session_id=? AND kind=? AND coalesce_key=? AND read_at=''
		`, update.SessionID, agentmailbox.KindMaintenancePrompt, PullRequestWatchCoalesceKey(update.PRID)); err != nil {
			return nil, false, err
		}
	}
	deliveries := make([]agentmailbox.Delivery, 0, len(update.MailboxItems))
	for _, item := range update.MailboxItems {
		if item.CoalesceKey != "" {
			if _, err := tx.Exec(`
				DELETE FROM agent_mailbox_items
				WHERE recipient_session_id=? AND kind=? AND coalesce_key=? AND id<>? AND read_at=''
			`, item.RecipientSessionID, item.Kind, item.CoalesceKey, item.ID); err != nil {
				return nil, false, err
			}
		}
		inserted, err := insertPullRequestMailboxItem(tx, item)
		if err != nil {
			return nil, false, err
		}
		if inserted {
			deliveries = append(deliveries, agentmailbox.Delivery{Item: item})
		}
	}
	if update.Terminal {
		if _, err := tx.Exec(`DELETE FROM pull_request_watches WHERE session_id=? AND pr_id=?`, update.SessionID, update.PRID); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return deliveries, projectionChanged, nil
}

func (s *Store) RecordPullRequestWatchFailure(sessionID, prID, createdAt string, mode prreadiness.Mode, reviewer, message string, outage agentmailbox.Item, at time.Time) (*agentmailbox.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var currentMode, currentReviewer, currentCreatedAt string
	var active bool
	if err := tx.QueryRow(`SELECT mode, reviewer, created_at, outage_active FROM pull_request_watches WHERE session_id=? AND pr_id=?`, sessionID, prID).Scan(&currentMode, &currentReviewer, &currentCreatedAt, &active); err != nil {
		return nil, err
	}
	if currentMode != string(mode) || !strings.EqualFold(currentReviewer, reviewer) || currentCreatedAt != createdAt {
		return nil, sql.ErrNoRows
	}
	stamp := at.UTC().Format(sortableTimeFormat)
	if _, err := tx.Exec(`UPDATE pull_request_watches SET last_error=?, outage_active=1 WHERE session_id=? AND pr_id=?`, message, sessionID, prID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		UPDATE session_pull_requests SET watch_health='delayed', watch_error=?, watch_last_checked_at=?, status_checked_at=?
		WHERE session_id=? AND pr_id=?
	`, message, stamp, stamp, sessionID, prID); err != nil {
		return nil, err
	}
	var delivery *agentmailbox.Delivery
	if !active {
		inserted, err := insertPullRequestMailboxItem(tx, outage)
		if err != nil {
			return nil, err
		}
		if inserted {
			delivery = &agentmailbox.Delivery{Item: outage}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return delivery, nil
}

func insertPullRequestMailboxItem(tx *sql.Tx, item agentmailbox.Item) (bool, error) {
	result, err := tx.Exec(`
		INSERT OR IGNORE INTO agent_mailbox_items
			(id, recipient_session_id, kind, source_id, coalesce_key, hint, prompt, created_at)
		VALUES(?,?,?,?,?,?,?,?)
	`, item.ID, item.RecipientSessionID, item.Kind, item.SourceID, item.CoalesceKey, item.Hint, item.Prompt, item.CreatedAt)
	if err != nil {
		return false, fmt.Errorf("enqueue pull request mailbox item: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

type pullRequestWatchScanner interface{ Scan(...any) error }

func scanPullRequestWatch(row pullRequestWatchScanner) (PullRequestWatch, error) {
	var watch PullRequestWatch
	var mode, cursorJSON string
	if err := row.Scan(&watch.SessionID, &watch.PRID, &mode, &watch.Reviewer, &watch.CreatedAt,
		&cursorJSON, &watch.LastSuccessAt, &watch.LastError, &watch.FeedbackError, &watch.OutageActive); err != nil {
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
