package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const LegacyTicketRecoveryVersion = 2

type LegacyTicketRecoveryState string

const (
	LegacyTicketRecoveryRunning   LegacyTicketRecoveryState = "running"
	LegacyTicketRecoverySucceeded LegacyTicketRecoveryState = "succeeded"
	LegacyTicketRecoveryWarned    LegacyTicketRecoveryState = "warned"
)

func (s LegacyTicketRecoveryState) Terminal() bool {
	return s == LegacyTicketRecoverySucceeded || s == LegacyTicketRecoveryWarned
}

type LegacyTicketRecoveryRun struct {
	Version               int
	State                 LegacyTicketRecoveryState
	InventoryJSON         string
	CountsJSON            string
	WarningNotificationID string
	StartedAt             time.Time
	RecoveryAt            time.Time
	FinishedAt            time.Time
	TerminalError         string
}

type LegacyTicketRecoverySource struct {
	RunVersion int    `json:"run_version"`
	Path       string `json:"path"`
	Family     string `json:"family"`
	Size       int64  `json:"size"`
	ModTimeNS  int64  `json:"mod_time_ns"`
	SHA256     string `json:"sha256"`
	State      string `json:"state,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

type LegacyTicketRecoveryItem struct {
	Fingerprint            string
	RunVersion             int
	SourceKind             string
	SourceKey              string
	TicketID               string
	RecoveredLocalIdentity string
	Result                 string
	Detail                 string
	CreatedAt              time.Time
}

func (s *Store) BeginLegacyTicketRecovery(version int, inventory []LegacyTicketRecoverySource, now time.Time) (*LegacyTicketRecoveryRun, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, fmt.Errorf("store: no database")
	}
	if version <= 0 {
		return nil, false, fmt.Errorf("legacy ticket recovery version must be positive")
	}
	if existing, err := s.GetLegacyTicketRecoveryRun(version); err != nil || existing != nil {
		return existing, false, err
	}

	sort.Slice(inventory, func(i, j int) bool { return inventory[i].Path < inventory[j].Path })
	encoded, err := json.Marshal(inventory)
	if err != nil {
		return nil, false, fmt.Errorf("encode legacy ticket recovery inventory: %w", err)
	}
	ts := now.UTC().Format(sortableTimeFormat)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO legacy_ticket_recovery_runs
		(version,state,inventory_json,counts_json,started_at,recovery_at)
		VALUES (?,? ,?,'{}',?,?)`, version, string(LegacyTicketRecoveryRunning), string(encoded), ts, ts); err != nil {
		if existing, readErr := getLegacyTicketRecoveryRun(tx, version); readErr == nil && existing != nil {
			return existing, false, nil
		}
		return nil, false, fmt.Errorf("begin legacy ticket recovery: %w", err)
	}
	for _, source := range inventory {
		if _, err := tx.Exec(`INSERT INTO legacy_ticket_recovery_sources
			(run_version,path,family,size,mod_time_ns,sha256,state,detail)
			VALUES (?,?,?,?,?,?,'pending','')`, version, source.Path, source.Family, source.Size, source.ModTimeNS, source.SHA256); err != nil {
			return nil, false, fmt.Errorf("record legacy ticket recovery source %s: %w", source.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &LegacyTicketRecoveryRun{
		Version: version, State: LegacyTicketRecoveryRunning, InventoryJSON: string(encoded),
		CountsJSON: "{}", StartedAt: now.UTC(), RecoveryAt: now.UTC(),
	}, true, nil
}

func (s *Store) GetLegacyTicketRecoveryRun(version int) (*LegacyTicketRecoveryRun, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	return getLegacyTicketRecoveryRun(s.db, version)
}

func getLegacyTicketRecoveryRun(q interface{ QueryRow(string, ...any) *sql.Row }, version int) (*LegacyTicketRecoveryRun, error) {
	var rec LegacyTicketRecoveryRun
	var state, startedAt, recoveryAt, finishedAt string
	err := q.QueryRow(`SELECT version,state,inventory_json,counts_json,warning_notification_id,
		started_at,recovery_at,finished_at,terminal_error
		FROM legacy_ticket_recovery_runs WHERE version=?`, version).Scan(
		&rec.Version, &state, &rec.InventoryJSON, &rec.CountsJSON, &rec.WarningNotificationID,
		&startedAt, &recoveryAt, &finishedAt, &rec.TerminalError,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read legacy ticket recovery run: %w", err)
	}
	rec.State = LegacyTicketRecoveryState(state)
	rec.StartedAt = parseStoreTime(startedAt)
	rec.RecoveryAt = parseStoreTime(recoveryAt)
	rec.FinishedAt = parseStoreTime(finishedAt)
	return &rec, nil
}

func (s *Store) ListLegacyTicketRecoverySources(version int) ([]LegacyTicketRecoverySource, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	rows, err := s.db.Query(`SELECT run_version,path,family,size,mod_time_ns,sha256,state,detail
		FROM legacy_ticket_recovery_sources WHERE run_version=? ORDER BY path`, version)
	if err != nil {
		return nil, fmt.Errorf("list legacy ticket recovery sources: %w", err)
	}
	defer rows.Close()
	var out []LegacyTicketRecoverySource
	for rows.Next() {
		var source LegacyTicketRecoverySource
		if err := rows.Scan(&source.RunVersion, &source.Path, &source.Family, &source.Size,
			&source.ModTimeNS, &source.SHA256, &source.State, &source.Detail); err != nil {
			return nil, err
		}
		out = append(out, source)
	}
	return out, rows.Err()
}

func (s *Store) SetLegacyTicketRecoverySourceState(version int, path, state, detail string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store: no database")
	}
	res, err := s.db.Exec(`UPDATE legacy_ticket_recovery_sources SET state=?,detail=?
		WHERE run_version=? AND path=?`, strings.TrimSpace(state), detail, version, path)
	if err != nil {
		return fmt.Errorf("update legacy ticket recovery source %s: %w", path, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("legacy ticket recovery source not found: %s", path)
	}
	return nil
}

func (s *Store) ProtectedLegacyTicketRecoveryPaths(version int) (map[string]struct{}, error) {
	sources, err := s.ListLegacyTicketRecoverySources(version)
	if err != nil {
		return nil, err
	}
	protected := make(map[string]struct{})
	for _, source := range sources {
		if source.State != "complete" {
			protected[source.Path] = struct{}{}
		}
	}
	return protected, nil
}

func (s *Store) HasAutomationTicketProvenance(ticketID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("store: no database")
	}
	var found int
	err := s.db.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM tickets WHERE id=? AND automation_run_id IS NOT NULL AND automation_run_id != ''
		UNION ALL SELECT 1 FROM automation_runs WHERE ticket_id=?
		UNION ALL SELECT 1 FROM automation_ticket_occurrence_events WHERE ticket_id=?
		UNION ALL SELECT 1 FROM automation_continuity_bindings WHERE ticket_id=?
	)`, ticketID, ticketID, ticketID, ticketID).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("read Automation-feature ticket provenance: %w", err)
	}
	return found != 0, nil
}

func (s *Store) LegacyTicketRecoverySourceKind(ticketID string) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("store: no database")
	}
	var kind string
	err := s.db.QueryRow(`SELECT source_kind FROM legacy_ticket_recovery_items
		WHERE ticket_id=? AND result IN ('recovered','live_won')
		ORDER BY created_at DESC,fingerprint LIMIT 1`, ticketID).Scan(&kind)
	if err == sql.ErrNoRows {
		return "live", nil
	}
	if err != nil {
		return "", err
	}
	return kind, nil
}

func recordLegacyTicketRecoveryItemTx(tx *sql.Tx, item LegacyTicketRecoveryItem) (bool, error) {
	res, err := tx.Exec(`INSERT INTO legacy_ticket_recovery_items
		(fingerprint,run_version,source_kind,source_key,ticket_id,recovered_local_identity,result,detail,created_at)
		VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(fingerprint) DO NOTHING`,
		item.Fingerprint, item.RunVersion, item.SourceKind, item.SourceKey, item.TicketID,
		item.RecoveredLocalIdentity, item.Result, item.Detail, item.CreatedAt.UTC().Format(sortableTimeFormat))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *Store) FinishLegacyTicketRecovery(version int, state LegacyTicketRecoveryState, counts any, terminalError string, warning *NotificationRecord, now time.Time) (string, error) {
	if !state.Terminal() {
		return "", fmt.Errorf("legacy ticket recovery cannot finish as %q", state)
	}
	encoded, err := json.Marshal(counts)
	if err != nil {
		return "", fmt.Errorf("encode legacy ticket recovery counts: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	existing, err := getLegacyTicketRecoveryRun(tx, version)
	if err != nil {
		return "", err
	}
	if existing == nil {
		return "", fmt.Errorf("legacy ticket recovery run %d not found", version)
	}
	if existing.State.Terminal() {
		return existing.WarningNotificationID, nil
	}

	notificationID := ""
	if warning != nil {
		notificationID = fmt.Sprintf("legacy-ticket-recovery-v%d", version)
		warning.Severity = NormalizeNotificationSeverity(string(warning.Severity))
		_, err = tx.Exec(`INSERT INTO notifications
			(id,kind,severity,title,body,detail,source_kind,source_id,created_at,read_at)
			VALUES (?,?,?,?,?,?,?,?,?,'') ON CONFLICT(id) DO NOTHING`,
			notificationID, warning.Kind, string(warning.Severity), warning.Title, warning.Body,
			warning.Detail, warning.SourceKind, warning.SourceID, now.UTC().Format(sortableTimeFormat))
		if err != nil {
			return "", fmt.Errorf("add legacy ticket recovery warning: %w", err)
		}
	}
	res, err := tx.Exec(`UPDATE legacy_ticket_recovery_runs SET state=?,counts_json=?,
		warning_notification_id=?,finished_at=?,terminal_error=? WHERE version=? AND state='running'`,
		string(state), string(encoded), notificationID, now.UTC().Format(sortableTimeFormat), terminalError, version)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", fmt.Errorf("legacy ticket recovery run %d changed while finishing", version)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return notificationID, nil
}
