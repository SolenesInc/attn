package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

type LegacyTicketCandidate struct {
	Ticket          Ticket
	ResumeSessionID string
	Activity        []TicketActivity
	Attachments     []TicketAttachment
	AutomationOwned bool
	SourcePath      string
	SchemaVersion   int
	Fingerprint     string
}

type LegacyTicketSnapshotRead struct {
	SchemaVersion       int
	Candidates          []LegacyTicketCandidate
	TicketIDs           []string
	AutomationTicketIDs []string
	Warnings            []string
}

func LatestSchemaVersion() int {
	if len(migrations) == 0 {
		return 0
	}
	return migrations[len(migrations)-1].version
}

func ReadLegacyTicketSnapshot(path string) (LegacyTicketSnapshotRead, error) {
	u := &url.URL{Scheme: "file", Path: path}
	query := u.Query()
	query.Set("mode", "ro")
	query.Set("immutable", "1")
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return LegacyTicketSnapshotRead{}, fmt.Errorf("open immutable snapshot: %w", err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	defer db.Close()

	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return LegacyTicketSnapshotRead{}, fmt.Errorf("integrity check: %w", err)
	}
	if integrity != "ok" {
		return LegacyTicketSnapshotRead{}, fmt.Errorf("integrity check: %s", integrity)
	}
	version, err := GetSchemaVersion(db)
	if err != nil {
		return LegacyTicketSnapshotRead{}, fmt.Errorf("schema version: %w", err)
	}
	if version < 55 {
		return LegacyTicketSnapshotRead{}, fmt.Errorf("unsupported schema version %d: ticket tables begin at 55", version)
	}
	if version > LatestSchemaVersion() {
		return LegacyTicketSnapshotRead{}, fmt.Errorf("unsupported future schema version %d (binary knows through %d)", version, LatestSchemaVersion())
	}

	resumeExpr, reconciledExpr, automationExpr := `''`, `''`, `NULL`
	hasResume, err := snapshotHasColumn(db, "tickets", "resume_session_id")
	if err != nil {
		return LegacyTicketSnapshotRead{}, err
	}
	if version >= 57 && !hasResume {
		return LegacyTicketSnapshotRead{}, fmt.Errorf("schema %d is missing tickets.resume_session_id", version)
	}
	if hasResume {
		resumeExpr = "resume_session_id"
	}
	hasReconciled, err := snapshotHasColumn(db, "tickets", "reconciled_at")
	if err != nil {
		return LegacyTicketSnapshotRead{}, err
	}
	if version >= 60 && !hasReconciled {
		return LegacyTicketSnapshotRead{}, fmt.Errorf("schema %d is missing tickets.reconciled_at", version)
	}
	if hasReconciled {
		reconciledExpr = "reconciled_at"
	}
	hasAutomation, err := snapshotHasColumn(db, "tickets", "automation_run_id")
	if err != nil {
		return LegacyTicketSnapshotRead{}, err
	}
	if version >= 73 && !hasAutomation {
		return LegacyTicketSnapshotRead{}, fmt.Errorf("schema %d is missing tickets.automation_run_id", version)
	}
	if hasAutomation {
		automationExpr = "automation_run_id"
	}
	result := LegacyTicketSnapshotRead{SchemaVersion: version}
	allRows, err := db.Query(`SELECT id FROM tickets ORDER BY id`)
	if err != nil {
		return LegacyTicketSnapshotRead{}, fmt.Errorf("read ticket identities from schema %d: %w", version, err)
	}
	for allRows.Next() {
		var id string
		if err := allRows.Scan(&id); err != nil {
			allRows.Close()
			return LegacyTicketSnapshotRead{}, err
		}
		if err := ValidateTicketID(id); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("ticket row has invalid id %q: %v", id, err))
			continue
		}
		result.TicketIDs = append(result.TicketIDs, id)
	}
	if err := allRows.Close(); err != nil {
		return LegacyTicketSnapshotRead{}, err
	}
	if err := allRows.Err(); err != nil {
		return LegacyTicketSnapshotRead{}, err
	}
	result.AutomationTicketIDs = snapshotAutomationTicketIDs(db)

	rows, err := db.Query(`SELECT id,title,description,status,assignee,cwd,last_agent_id,project_id,
		` + automationExpr + `,created_at,updated_at,closed_at,archived_at,` + reconciledExpr + `,` + resumeExpr + `
		FROM tickets WHERE status IN ('done','failed','crashed') ORDER BY id`)
	if err != nil {
		return LegacyTicketSnapshotRead{}, fmt.Errorf("read tickets from schema %d: %w", version, err)
	}
	defer rows.Close()

	for rows.Next() {
		candidate, warning, err := scanLegacyTicketCandidate(db, rows, path, version)
		if err != nil {
			return LegacyTicketSnapshotRead{}, err
		}
		if warning != "" {
			result.Warnings = append(result.Warnings, warning)
			continue
		}
		result.Candidates = append(result.Candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return LegacyTicketSnapshotRead{}, err
	}
	return result, nil
}

func snapshotAutomationTicketIDs(db *sql.DB) []string {
	ids := make(map[string]struct{})
	if hasSnapshotTable(db, "tickets") {
		if hasColumn, err := snapshotHasColumn(db, "tickets", "automation_run_id"); err == nil && hasColumn {
			rows, err := db.Query(`SELECT id FROM tickets WHERE automation_run_id IS NOT NULL AND automation_run_id != ''`)
			if err == nil {
				for rows.Next() {
					var id string
					if rows.Scan(&id) == nil && ValidateTicketID(id) == nil {
						ids[id] = struct{}{}
					}
				}
				rows.Close()
			}
		}
	}
	for _, check := range []struct {
		table  string
		column string
	}{
		{"automation_runs", "ticket_id"},
		{"automation_ticket_occurrence_events", "ticket_id"},
		{"automation_continuity_bindings", "ticket_id"},
	} {
		if !hasSnapshotTable(db, check.table) {
			continue
		}
		hasColumn, err := snapshotHasColumn(db, check.table, check.column)
		if err != nil || !hasColumn {
			continue
		}
		rows, err := db.Query(`SELECT ` + check.column + ` FROM ` + check.table + ` WHERE ` + check.column + ` != ''`)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil && ValidateTicketID(id) == nil {
				ids[id] = struct{}{}
			}
		}
		rows.Close()
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func snapshotHasColumn(db *sql.DB, table, column string) (bool, error) {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}

func hasSnapshotTable(db *sql.DB, table string) bool {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
		return false
	}
	return count == 1
}

func scanLegacyTicketCandidate(db *sql.DB, scanner ticketScanner, path string, version int) (LegacyTicketCandidate, string, error) {
	var candidate LegacyTicketCandidate
	var status, createdAt, updatedAt, closedAt, archivedAt, reconciledAt string
	var automationRunID sql.NullString
	err := scanner.Scan(
		&candidate.Ticket.ID, &candidate.Ticket.Title, &candidate.Ticket.Description, &status,
		&candidate.Ticket.Assignee, &candidate.Ticket.Cwd, &candidate.Ticket.LastAgentID,
		&candidate.Ticket.ProjectID, &automationRunID, &createdAt, &updatedAt, &closedAt,
		&archivedAt, &reconciledAt, &candidate.ResumeSessionID,
	)
	if err != nil {
		return candidate, "", err
	}
	if err := ValidateTicketID(candidate.Ticket.ID); err != nil {
		return candidate, fmt.Sprintf("ticket row has invalid id %q: %v", candidate.Ticket.ID, err), nil
	}
	candidate.Ticket.Status = TicketStatus(status)
	if !candidate.Ticket.Status.IsTerminal() {
		return candidate, fmt.Sprintf("ticket %s has non-terminal status %q", candidate.Ticket.ID, status), nil
	}
	if strings.TrimSpace(candidate.Ticket.Title) == "" {
		return candidate, fmt.Sprintf("ticket %s has an empty title", candidate.Ticket.ID), nil
	}
	var ok bool
	if candidate.Ticket.CreatedAt, ok = strictTicketTime(createdAt, false); !ok {
		return candidate, fmt.Sprintf("ticket %s has invalid created_at %q", candidate.Ticket.ID, createdAt), nil
	}
	if candidate.Ticket.UpdatedAt, ok = strictTicketTime(updatedAt, false); !ok {
		return candidate, fmt.Sprintf("ticket %s has invalid updated_at %q", candidate.Ticket.ID, updatedAt), nil
	}
	if ts, valid := strictTicketTime(closedAt, false); valid {
		candidate.Ticket.ClosedAt = &ts
	} else {
		return candidate, fmt.Sprintf("ticket %s has invalid or empty closed_at %q", candidate.Ticket.ID, closedAt), nil
	}
	if archivedAt != "" {
		if ts, valid := strictTicketTime(archivedAt, true); valid {
			candidate.Ticket.ArchivedAt = &ts
		} else {
			return candidate, fmt.Sprintf("ticket %s has invalid archived_at %q", candidate.Ticket.ID, archivedAt), nil
		}
	}
	if reconciledAt != "" {
		if ts, valid := strictTicketTime(reconciledAt, true); valid {
			candidate.Ticket.ReconciledAt = &ts
		} else {
			return candidate, fmt.Sprintf("ticket %s has invalid reconciled_at %q", candidate.Ticket.ID, reconciledAt), nil
		}
	}
	candidate.Ticket.AutomationRunID = automationRunID.String
	candidate.SourcePath = path
	candidate.SchemaVersion = version
	candidate.AutomationOwned = automationRunID.Valid && automationRunID.String != ""
	if !candidate.AutomationOwned {
		candidate.AutomationOwned = snapshotHasAutomationTicket(db, candidate.Ticket.ID)
	}

	activity, err := readLegacyTicketActivity(db, candidate.Ticket.ID)
	if err != nil {
		return candidate, "", fmt.Errorf("read activity for %s: %w", candidate.Ticket.ID, err)
	}
	attachments, err := readLegacyTicketAttachments(db, candidate.Ticket.ID)
	if err != nil {
		return candidate, "", fmt.Errorf("read attachments for %s: %w", candidate.Ticket.ID, err)
	}
	candidate.Activity = activity
	candidate.Attachments = attachments
	candidate.Fingerprint = legacyTicketCandidateFingerprint(candidate)
	return candidate, "", nil
}

func strictTicketTime(raw string, allowEmpty bool) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, allowEmpty
	}
	ts, err := time.Parse(time.RFC3339, raw)
	return ts, err == nil
}

func snapshotHasAutomationTicket(db *sql.DB, ticketID string) bool {
	checks := []struct {
		table  string
		column string
	}{
		{"automation_runs", "ticket_id"},
		{"automation_ticket_occurrence_events", "ticket_id"},
		{"automation_continuity_bindings", "ticket_id"},
	}
	for _, check := range checks {
		hasColumn, err := snapshotHasColumn(db, check.table, check.column)
		if !hasSnapshotTable(db, check.table) || err != nil || !hasColumn {
			continue
		}
		var found int
		query := `SELECT 1 FROM ` + check.table + ` WHERE ` + check.column + `=? LIMIT 1`
		if err := db.QueryRow(query, ticketID).Scan(&found); err == nil {
			return true
		}
	}
	return false
}

func readLegacyTicketActivity(db *sql.DB, ticketID string) ([]TicketActivity, error) {
	rows, err := db.Query(`SELECT id,ticket_id,kind,author,from_status,to_status,comment,created_at
		FROM ticket_activity WHERE ticket_id=? ORDER BY id`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TicketActivity
	for rows.Next() {
		var row TicketActivity
		var kind, from, to, createdAt string
		if err := rows.Scan(&row.ID, &row.TicketID, &kind, &row.Author, &from, &to, &row.Comment, &createdAt); err != nil {
			return nil, err
		}
		row.Kind, row.FromStatus, row.ToStatus = TicketActivityKind(kind), TicketStatus(from), TicketStatus(to)
		var ok bool
		if row.CreatedAt, ok = strictTicketTime(createdAt, false); !ok {
			return nil, fmt.Errorf("row %d has invalid created_at %q", row.ID, createdAt)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func readLegacyTicketAttachments(db *sql.DB, ticketID string) ([]TicketAttachment, error) {
	rows, err := db.Query(`SELECT id,ticket_id,filename,path,note,created_at
		FROM ticket_attachments WHERE ticket_id=? ORDER BY id`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TicketAttachment
	for rows.Next() {
		var row TicketAttachment
		var createdAt string
		if err := rows.Scan(&row.ID, &row.TicketID, &row.Filename, &row.Path, &row.Note, &createdAt); err != nil {
			return nil, err
		}
		var ok bool
		if row.CreatedAt, ok = strictTicketTime(createdAt, false); !ok {
			return nil, fmt.Errorf("row %d has invalid created_at %q", row.ID, createdAt)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func legacyTicketCandidateFingerprint(candidate LegacyTicketCandidate) string {
	candidate.SourcePath = ""
	candidate.SchemaVersion = 0
	candidate.Fingerprint = ""
	candidate.Activity = append([]TicketActivity(nil), candidate.Activity...)
	candidate.Attachments = append([]TicketAttachment(nil), candidate.Attachments...)
	for i := range candidate.Activity {
		candidate.Activity[i].ID = 0
	}
	for i := range candidate.Attachments {
		candidate.Attachments[i].ID = 0
	}
	encoded, _ := json.Marshal(candidate)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (s *Store) RecordLegacyTicketRecoveryItem(item LegacyTicketRecoveryItem) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	created, err := recordLegacyTicketRecoveryItemTx(tx, item)
	if err != nil {
		return false, err
	}
	return created, tx.Commit()
}

func (s *Store) RestoreLegacyTicket(candidate LegacyTicketCandidate, item LegacyTicketRecoveryItem) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return "", fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var recordedResult string
	if err := tx.QueryRow(`SELECT result FROM legacy_ticket_recovery_items WHERE fingerprint=?`, item.Fingerprint).Scan(&recordedResult); err == nil {
		return recordedResult, nil
	} else if err != sql.ErrNoRows {
		return "", err
	}
	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM tickets WHERE id=?`, candidate.Ticket.ID).Scan(&exists); err == nil {
		item.Result = "live_won"
		if _, err := recordLegacyTicketRecoveryItemTx(tx, item); err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return item.Result, nil
	} else if err != sql.ErrNoRows {
		return "", err
	}

	_, err = tx.Exec(`INSERT INTO tickets
		(id,title,description,status,assignee,cwd,last_agent_id,project_id,automation_run_id,
		created_at,updated_at,closed_at,archived_at,reconciled_at,resume_session_id)
		VALUES (?,?,?,?,?,?,?,?,NULL,?,?,?,?,?,?)`,
		candidate.Ticket.ID, candidate.Ticket.Title, candidate.Ticket.Description, string(candidate.Ticket.Status),
		candidate.Ticket.Assignee, candidate.Ticket.Cwd, candidate.Ticket.LastAgentID, candidate.Ticket.ProjectID,
		formatTicketTime(candidate.Ticket.CreatedAt), formatTicketTime(candidate.Ticket.UpdatedAt),
		formatTicketTimePtr(candidate.Ticket.ClosedAt), formatTicketTimePtr(candidate.Ticket.ArchivedAt),
		formatTicketTimePtr(candidate.Ticket.ReconciledAt), candidate.ResumeSessionID)
	if err != nil {
		return "", fmt.Errorf("restore ticket %s: %w", candidate.Ticket.ID, err)
	}
	for _, row := range candidate.Activity {
		if _, err := tx.Exec(`INSERT INTO ticket_activity
			(ticket_id,kind,author,from_status,to_status,comment,created_at) VALUES (?,?,?,?,?,?,?)`,
			candidate.Ticket.ID, string(row.Kind), row.Author, string(row.FromStatus), string(row.ToStatus),
			row.Comment, formatTicketTime(row.CreatedAt)); err != nil {
			return "", fmt.Errorf("restore activity for %s: %w", candidate.Ticket.ID, err)
		}
	}
	for _, row := range candidate.Attachments {
		if _, err := tx.Exec(`INSERT INTO ticket_attachments
			(ticket_id,filename,path,note,created_at) VALUES (?,?,?,?,?)`,
			candidate.Ticket.ID, row.Filename, row.Path, row.Note, formatTicketTime(row.CreatedAt)); err != nil {
			return "", fmt.Errorf("restore attachment for %s: %w", candidate.Ticket.ID, err)
		}
	}
	item.Result = "recovered"
	item.RecoveredLocalIdentity = candidate.Ticket.ID
	if _, err := recordLegacyTicketRecoveryItemTx(tx, item); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return item.Result, nil
}

func (s *Store) RestoreLegacyTicketAttachment(row TicketAttachment, item LegacyTicketRecoveryItem) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return "", fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var recordedResult string
	if err := tx.QueryRow(`SELECT result FROM legacy_ticket_recovery_items WHERE fingerprint=?`, item.Fingerprint).Scan(&recordedResult); err == nil {
		return recordedResult, nil
	} else if err != sql.ErrNoRows {
		return "", err
	}
	var ticketExists int
	if err := tx.QueryRow(`SELECT 1 FROM tickets WHERE id=?`, row.TicketID).Scan(&ticketExists); err == sql.ErrNoRows {
		item.Result = "unbound"
	} else if err != nil {
		return "", err
	}
	if item.Result == "" {
		var filename, note, createdAt string
		err := tx.QueryRow(`SELECT filename,note,created_at FROM ticket_attachments
			WHERE ticket_id=? AND path=? ORDER BY id LIMIT 1`, row.TicketID, row.Path).Scan(&filename, &note, &createdAt)
		switch {
		case err == nil && filename == row.Filename && note == row.Note && createdAt == formatTicketTime(row.CreatedAt):
			item.Result = "adopted"
		case err == nil:
			item.Result = "live_won"
		case err != sql.ErrNoRows:
			return "", err
		default:
			if _, err := tx.Exec(`INSERT INTO ticket_attachments
				(ticket_id,filename,path,note,created_at) VALUES (?,?,?,?,?)`,
				row.TicketID, row.Filename, row.Path, row.Note, formatTicketTime(row.CreatedAt)); err != nil {
				return "", err
			}
			item.Result = "recovered"
			item.RecoveredLocalIdentity = row.Path
		}
	}
	if _, err := recordLegacyTicketRecoveryItemTx(tx, item); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return item.Result, nil
}
