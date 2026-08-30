package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type App struct {
	Name                     string
	CurrentVersionID         int64
	PreviousServingVersionID int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

const appColumnsSQL = `
	SELECT a.name, a.current_version_id,
	       (SELECT p.version_id FROM app_serving_steps c
	          JOIN app_serving_steps p ON p.id = c.parent_id
	         WHERE c.id = a.serving_step_id),
	       a.created_at, a.updated_at
	FROM apps a`

func pushServingStep(tx *sql.Tx, name string, versionID int64, stamp string) (int64, bool, error) {
	var current, cursor sql.NullInt64
	err := tx.QueryRow(`SELECT current_version_id, serving_step_id FROM apps WHERE name = ?`, name).
		Scan(&current, &cursor)
	if err == sql.ErrNoRows {
		return 0, false, fmt.Errorf("store: no app named %q", name)
	}
	if err != nil {
		return 0, false, err
	}
	if current.Valid && current.Int64 == versionID {
		_, err = tx.Exec(`UPDATE apps SET updated_at = ? WHERE name = ?`, stamp, name)
		return current.Int64, false, err
	}
	res, err := tx.Exec(`
		INSERT INTO app_serving_steps (app_name, version_id, parent_id, created_at)
		VALUES (?, ?, ?, ?)
	`, name, versionID, cursor, stamp)
	if err != nil {
		return 0, false, err
	}
	step, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	_, err = tx.Exec(`
		UPDATE apps SET current_version_id = ?, serving_step_id = ?, updated_at = ? WHERE name = ?
	`, versionID, step, stamp, name)
	return current.Int64, true, err
}

type AppVersion struct {
	ID           int64
	AppName      string
	ContentHash  string
	Declaration  string
	ArtifactPath string
	CreatedAt    time.Time
}

type AppInvocation struct {
	ID               int64
	AppName          string
	VersionID        int64
	Kind             string
	EventSeq         int64
	EventName        string
	EventSubject     string
	Handler          string
	Status           string
	Error            string
	Duration         time.Duration
	StartedAt        time.Time
	FinishedAt       time.Time
	ReconcileReason  string
	ThroughRequestID int64
	ThroughSeq       int64
}

const (
	AppInvocationKindSubscription = "subscription"
	AppInvocationKindCommand      = "command"
	AppInvocationKindView         = "view"
	AppInvocationKindReconcile    = "reconcile"

	AppInvocationStatusRunning      = "running"
	AppInvocationStatusOK           = "ok"
	AppInvocationStatusError        = "error"
	AppInvocationStatusRuntimeError = "runtime_error"
	AppInvocationStatusInterrupted  = "interrupted"
)

func (s *Store) SaveApp(name string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	_, err := s.db.Exec(`
		INSERT INTO apps (name, current_version_id, created_at, updated_at)
		VALUES (?, NULL, ?, ?)
		ON CONFLICT(name) DO UPDATE SET updated_at = excluded.updated_at
	`, name, stamp, stamp)
	return err
}

func (s *Store) GetApp(name string) (App, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return App{}, false, nil
	}
	app, err := scanApp(s.db.QueryRow(appColumnsSQL+` WHERE a.name = ?`, name))
	switch err {
	case nil:
		return app, true, nil
	case sql.ErrNoRows:
		return App{}, false, nil
	default:
		return App{}, false, err
	}
}

func (s *Store) ListApps() ([]App, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(appColumnsSQL + ` ORDER BY a.name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []App
	for rows.Next() {
		app, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, app)
	}
	return out, rows.Err()
}

func (s *Store) DeleteApp(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM app_reconcile_requests WHERE app_name = ?`, name); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM app_reconcile_progress WHERE app_name = ?`, name); err != nil {
		return false, err
	}
	res, err := tx.Exec(`DELETE FROM apps WHERE name = ?`, name)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, tx.Commit()
}

// The bool reports whether this call minted the row: reuse is a database
// property (UNIQUE(app_name, content_hash)) the caller cannot otherwise see.
func (s *Store) CommitAppVersion(v AppVersion, now time.Time) (AppVersion, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return AppVersion{}, false, nil
	}
	if v.AppName == "" || v.ContentHash == "" {
		return AppVersion{}, false, fmt.Errorf("store: an app version needs both an app name and a content hash (got name %q, hash %q)", v.AppName, v.ContentHash)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return AppVersion{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	stamp := now.UTC().Format(sortableTimeFormat)
	if _, err := tx.Exec(`
		INSERT INTO apps (name, current_version_id, created_at, updated_at)
		VALUES (?, NULL, ?, ?)
		ON CONFLICT(name) DO UPDATE SET updated_at = excluded.updated_at
	`, v.AppName, stamp, stamp); err != nil {
		return AppVersion{}, false, err
	}

	created := false
	existing, err := scanAppVersion(tx.QueryRow(`
		SELECT id, app_name, content_hash, declaration, artifact_path, created_at
		FROM app_versions WHERE app_name = ? AND content_hash = ?
	`, v.AppName, v.ContentHash))
	switch err {
	case nil:
		v = existing
	case sql.ErrNoRows:
		v.CreatedAt = now.UTC()
		res, err := tx.Exec(`
			INSERT INTO app_versions (app_name, content_hash, declaration, artifact_path, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, v.AppName, v.ContentHash, v.Declaration, v.ArtifactPath, stamp)
		if err != nil {
			return AppVersion{}, false, err
		}
		if v.ID, err = res.LastInsertId(); err != nil {
			return AppVersion{}, false, err
		}
		created = true
	default:
		return AppVersion{}, false, err
	}

	previous, moved, err := pushServingStep(tx, v.AppName, v.ID, stamp)
	if err != nil {
		return AppVersion{}, false, err
	}
	if moved && previous != 0 {
		cursor, subscribed, err := appConsumerCursorWith(tx, v.AppName)
		if err != nil {
			return AppVersion{}, false, err
		}
		if subscribed {
			if err := appendAppReconcileRequest(tx, v.AppName, AppReconcileVersionChange, v.ID, cursor, previous, now); err != nil {
				return AppVersion{}, false, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return AppVersion{}, false, err
	}
	return v, created, nil
}

func (s *Store) SetAppCurrentVersion(name string, versionID int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	var owner string
	err := s.db.QueryRow(`SELECT app_name FROM app_versions WHERE id = ?`, versionID).Scan(&owner)
	switch {
	case err == sql.ErrNoRows:
		return fmt.Errorf("store: no app version %d", versionID)
	case err != nil:
		return err
	case owner != name:
		return fmt.Errorf("store: app version %d belongs to app %q, not %q", versionID, owner, name)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	previous, moved, err := pushServingStep(tx, name, versionID, now.UTC().Format(sortableTimeFormat))
	if err != nil {
		return err
	}
	if moved && previous != 0 {
		cursor, subscribed, err := appConsumerCursorWith(tx, name)
		if err != nil {
			return err
		}
		if subscribed {
			if err := appendAppReconcileRequest(tx, name, AppReconcileVersionChange, versionID, cursor, previous, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) StepAppVersionBack(name string, target int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	var stepID, versionID, previous int64
	err := s.db.QueryRow(`
		SELECT p.id, p.version_id, c.version_id FROM apps a
			JOIN app_serving_steps c ON c.id = a.serving_step_id
			JOIN app_serving_steps p ON p.id = c.parent_id
		WHERE a.name = ?
	`, name).Scan(&stepID, &versionID, &previous)
	switch {
	case err == sql.ErrNoRows:
		return fmt.Errorf("store: app %q has nothing below it in its serving history", name)
	case err != nil:
		return err
	case versionID != target:
		return fmt.Errorf("store: app %q now has version %d one step back, not the %d this rollback resolved; nothing moved", name, versionID, target)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`
		UPDATE apps SET current_version_id = ?, serving_step_id = ?, updated_at = ? WHERE name = ?
	`, versionID, stepID, now.UTC().Format(sortableTimeFormat), name)
	if err != nil {
		return err
	}
	if previous != 0 {
		cursor, subscribed, err := appConsumerCursorWith(tx, name)
		if err != nil {
			return err
		}
		if subscribed {
			if err := appendAppReconcileRequest(tx, name, AppReconcileVersionChange, versionID, cursor, previous, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) ListAppServingHistory(name string, limit int) ([]int64, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, 0, nil
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		WITH RECURSIVE walk(id, version_id, parent_id) AS (
			SELECT s.id, s.version_id, s.parent_id FROM app_serving_steps s
				JOIN apps a ON a.serving_step_id = s.id
			WHERE a.name = ?
			UNION ALL
			SELECT s.id, s.version_id, s.parent_id FROM app_serving_steps s
				JOIN walk w ON s.id = w.parent_id
		)
		SELECT version_id FROM walk
	`, name)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var (
		out   []int64
		steps int
	)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, 0, err
		}
		steps++
		if len(out) < limit {
			out = append(out, id)
		}
	}
	return out, steps, rows.Err()
}

func (s *Store) GetAppVersion(id int64) (AppVersion, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return AppVersion{}, false, nil
	}
	v, err := scanAppVersion(s.db.QueryRow(`
		SELECT id, app_name, content_hash, declaration, artifact_path, created_at
		FROM app_versions WHERE id = ?
	`, id))
	switch err {
	case nil:
		return v, true, nil
	case sql.ErrNoRows:
		return AppVersion{}, false, nil
	default:
		return AppVersion{}, false, err
	}
}

func (s *Store) ListAppVersions(name string) ([]AppVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT id, app_name, content_hash, declaration, artifact_path, created_at
		FROM app_versions WHERE app_name = ? ORDER BY id DESC
	`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AppVersion
	for rows.Next() {
		v, err := scanAppVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) CountAppVersions(name string) (int, error) {
	return s.countAppRows(`SELECT COUNT(*) FROM app_versions WHERE app_name = ?`, name)
}

func (s *Store) CountAppInvocations(name string) (int, error) {
	return s.countAppRows(`SELECT COUNT(*) FROM app_invocations WHERE app_name = ?`, name)
}

func (s *Store) countAppRows(query, name string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return 0, nil
	}
	var n int
	if err := s.db.QueryRow(query, name).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) AppendAppInvocation(inv AppInvocation) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, nil
	}
	inv = normalizeAppInvocation(inv)
	res, err := s.db.Exec(`
			INSERT INTO app_invocations
				(app_name, version_id, event_seq, event_name, event_subject, handler, status, error,
				 duration_ms, started_at, kind, reconcile_reason, through_request_id, through_seq, finished_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, inv.AppName, inv.VersionID, inv.EventSeq, inv.EventName, inv.EventSubject, inv.Handler,
		inv.Status, inv.Error, inv.Duration.Milliseconds(), inv.StartedAt.UTC().Format(sortableTimeFormat),
		inv.Kind, inv.ReconcileReason, nullablePositiveInt64(inv.ThroughRequestID), nullableInt64(inv.ThroughSeq, inv.ThroughRequestID != 0),
		nullableStoreTime(inv.FinishedAt))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) StartAppInvocation(inv AppInvocation) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, nil
	}
	inv = normalizeAppInvocation(inv)
	if inv.StartedAt.IsZero() {
		return 0, errors.New("store: an app invocation needs a start time")
	}
	if inv.Kind == AppInvocationKindReconcile {
		if inv.ThroughRequestID <= 0 {
			return 0, fmt.Errorf("store: a reconcile invocation needs a positive through_request_id (got %d)", inv.ThroughRequestID)
		}
		if !json.Valid([]byte(inv.ReconcileReason)) {
			return 0, errors.New("store: a reconcile invocation needs a valid JSON reason")
		}
	}
	res, err := s.db.Exec(`
		INSERT INTO app_invocations
			(app_name, version_id, event_seq, event_name, event_subject, handler, status, error,
			 duration_ms, started_at, kind, reconcile_reason, through_request_id, through_seq, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, '', 0, ?, ?, ?, ?, ?, NULL)
	`, inv.AppName, inv.VersionID, inv.EventSeq, inv.EventName, inv.EventSubject, inv.Handler,
		AppInvocationStatusRunning, inv.StartedAt.UTC().Format(sortableTimeFormat), inv.Kind,
		inv.ReconcileReason, nullablePositiveInt64(inv.ThroughRequestID), nullableInt64(inv.ThroughSeq, inv.ThroughRequestID != 0))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// The status predicate makes a daemon-shutdown settlement and a startup
// interruption repair safe to race: the terminal answer cannot be rewritten.
func (s *Store) SettleAppInvocation(id int64, status, failure string, finishedAt time.Time) (bool, error) {
	if !terminalAppInvocationStatus(status) {
		return false, fmt.Errorf("store: %q is not a terminal app invocation status", status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, nil
	}

	var startedText string
	err := s.db.QueryRow(`SELECT started_at FROM app_invocations WHERE id = ? AND status = ?`, id, AppInvocationStatusRunning).Scan(&startedText)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if finishedAt.IsZero() {
		return false, errors.New("store: settling an app invocation needs a finish time")
	}
	duration := finishedAt.Sub(parseStoreTime(startedText))
	if duration < 0 {
		duration = 0
	}
	res, err := s.db.Exec(`
		UPDATE app_invocations
		SET status = ?, error = ?, duration_ms = ?, finished_at = ?
		WHERE id = ? AND status = ?
	`, status, failure, duration.Milliseconds(), finishedAt.UTC().Format(sortableTimeFormat), id, AppInvocationStatusRunning)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) InterruptRunningAppInvocations(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return 0, nil
	}
	if now.IsZero() {
		return 0, errors.New("store: interrupting app invocations needs a time")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query(`SELECT id, started_at FROM app_invocations WHERE status = ? ORDER BY id`, AppInvocationStatusRunning)
	if err != nil {
		return 0, err
	}
	type runningInvocation struct {
		id      int64
		started string
	}
	var running []runningInvocation
	for rows.Next() {
		var inv runningInvocation
		if err := rows.Scan(&inv.id, &inv.started); err != nil {
			_ = rows.Close()
			return 0, err
		}
		running = append(running, inv)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	for _, inv := range running {
		duration := now.Sub(parseStoreTime(inv.started))
		if duration < 0 {
			duration = 0
		}
		if _, err := tx.Exec(`
			UPDATE app_invocations
			SET status = ?, duration_ms = ?, finished_at = ?
			WHERE id = ? AND status = ?
		`, AppInvocationStatusInterrupted, duration.Milliseconds(), stamp, inv.id, AppInvocationStatusRunning); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(running), nil
}

func (s *Store) LatestOwedAppReconcileInvocation(name string) (AppInvocation, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return AppInvocation{}, false, nil
	}
	inv, err := scanAppInvocation(s.db.QueryRow(`
		SELECT id, app_name, version_id, event_seq, event_name, event_subject, handler,
		       status, error, duration_ms, started_at, kind, reconcile_reason,
		       through_request_id, through_seq, finished_at
		FROM app_invocations
		WHERE app_name = ? AND kind = ?
		  AND through_request_id > COALESCE((
			SELECT completed_request_id FROM app_reconcile_progress WHERE app_name = ?
		  ), 0)
		ORDER BY id DESC LIMIT 1
	`, name, AppInvocationKindReconcile, name))
	if err == sql.ErrNoRows {
		return AppInvocation{}, false, nil
	}
	if err != nil {
		return AppInvocation{}, false, err
	}
	return inv, true, nil
}

func (s *Store) ListAppInvocations(name string, limit int) ([]AppInvocation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
			SELECT id, app_name, version_id, event_seq, event_name, event_subject, handler,
			       status, error, duration_ms, started_at, kind, reconcile_reason,
			       through_request_id, through_seq, finished_at
			FROM app_invocations WHERE app_name = ? ORDER BY started_at DESC, id DESC LIMIT ?
		`, name, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AppInvocation
	for rows.Next() {
		inv, err := scanAppInvocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// The age window cannot bound the log's size, so both limits exist; their values and receipts
// live with the caller (AppInvocationRetention, AppInvocationsPerApp).
func (s *Store) TrimAppInvocations(cutoff time.Time, perApp int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return 0, nil
	}
	removed := 0
	res, err := s.db.Exec(`DELETE FROM app_invocations WHERE started_at < ?`,
		cutoff.UTC().Format(sortableTimeFormat))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	removed += int(n)

	if perApp <= 0 {
		return removed, nil
	}
	// The ordering matches ListAppInvocations exactly — newest first, id breaking
	// a same-timestamp tie — so what the cap keeps is what a reader can see.
	res, err = s.db.Exec(`
		DELETE FROM app_invocations WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (
					PARTITION BY app_name ORDER BY started_at DESC, id DESC
				) AS rank FROM app_invocations
			) WHERE rank > ?
		)`, perApp)
	if err != nil {
		return removed, err
	}
	n, err = res.RowsAffected()
	if err != nil {
		return removed, err
	}
	return removed + int(n), nil
}

func normalizeAppInvocation(inv AppInvocation) AppInvocation {
	if inv.Kind == "" {
		switch inv.EventName {
		case "app.command":
			inv.Kind = AppInvocationKindCommand
		case "app.view.crashed":
			inv.Kind = AppInvocationKindView
		default:
			inv.Kind = AppInvocationKindSubscription
		}
	}
	if inv.Status != "" && inv.Status != AppInvocationStatusRunning && inv.FinishedAt.IsZero() && !inv.StartedAt.IsZero() {
		inv.FinishedAt = inv.StartedAt.Add(inv.Duration)
	}
	return inv
}

func terminalAppInvocationStatus(status string) bool {
	switch status {
	case AppInvocationStatusOK, AppInvocationStatusError, AppInvocationStatusRuntimeError,
		AppInvocationStatusInterrupted:
		return true
	default:
		return false
	}
}

func nullablePositiveInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullableInt64(value int64, present bool) any {
	if !present {
		return nil
	}
	return value
}

func nullableStoreTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(sortableTimeFormat)
}

func scanAppInvocation(row rowScanner) (AppInvocation, error) {
	var (
		inv                          AppInvocation
		durMillis                    int64
		startedAt                    string
		finishedAt                   sql.NullString
		throughRequestID, throughSeq sql.NullInt64
	)
	if err := row.Scan(&inv.ID, &inv.AppName, &inv.VersionID, &inv.EventSeq, &inv.EventName,
		&inv.EventSubject, &inv.Handler, &inv.Status, &inv.Error, &durMillis, &startedAt,
		&inv.Kind, &inv.ReconcileReason, &throughRequestID, &throughSeq, &finishedAt); err != nil {
		return AppInvocation{}, err
	}
	inv.Duration = time.Duration(durMillis) * time.Millisecond
	inv.StartedAt = parseStoreTime(startedAt)
	inv.ThroughRequestID = throughRequestID.Int64
	inv.ThroughSeq = throughSeq.Int64
	if finishedAt.Valid {
		inv.FinishedAt = parseStoreTime(finishedAt.String)
	}
	return inv, nil
}

func scanApp(row rowScanner) (App, error) {
	var (
		app        App
		versionID  sql.NullInt64
		previousID sql.NullInt64
		createdAt  string
		updatedAt  string
	)
	if err := row.Scan(&app.Name, &versionID, &previousID, &createdAt, &updatedAt); err != nil {
		return App{}, err
	}
	app.CurrentVersionID = versionID.Int64
	app.PreviousServingVersionID = previousID.Int64
	app.CreatedAt = parseStoreTime(createdAt)
	app.UpdatedAt = parseStoreTime(updatedAt)
	return app, nil
}

func scanAppVersion(row rowScanner) (AppVersion, error) {
	var (
		v         AppVersion
		createdAt string
	)
	if err := row.Scan(&v.ID, &v.AppName, &v.ContentHash, &v.Declaration, &v.ArtifactPath, &createdAt); err != nil {
		return AppVersion{}, err
	}
	v.CreatedAt = parseStoreTime(createdAt)
	return v, nil
}
