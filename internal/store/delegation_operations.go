package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/protocol"
)

var (
	ErrDelegationRequestConflict = errors.New("delegation request id already has different inputs")
	ErrTicketDelegationReserved  = errors.New("ticket already has a delegation being prepared")
)

// DelegationOperationRecord is the durable launch journal. RequestJSON lets a restart resume
// the exact accepted request and reject a reused key whose inputs differ.
type DelegationOperationRecord struct {
	Operation             protocol.DelegationOperation
	ResolvedPreferences   string
	RequestJSON           string
	WorktreeOwned         bool
	WorktreeToken         string
	ChiefSessionID        string
	BaseCommit            string
	HandoffNoteID         string
	HandoverSeedRev       int
	HandoverTenderSession string
	HandoverTenderMember  string
}

type DelegationHandoverSnapshot struct {
	SeedRev       int
	TenderSession string
	TenderMember  string
}

func (s *Store) ClaimDelegationOperation(requestID, operationID, sessionID, chiefSessionID, ticketID, requestJSON string, now time.Time) (*DelegationOperationRecord, bool, error) {
	return s.ClaimDelegationOperationWithPreferences(requestID, operationID, sessionID, chiefSessionID, ticketID, requestJSON, "", now)
}

func (s *Store) ClaimDelegationOperationWithPreferences(requestID, operationID, sessionID, chiefSessionID, ticketID, requestJSON, resolvedPreferences string, now time.Time) (*DelegationOperationRecord, bool, error) {
	return s.ClaimDelegationOperationWithHandoverSnapshot(requestID, operationID, sessionID, chiefSessionID, ticketID, requestJSON, resolvedPreferences, DelegationHandoverSnapshot{}, now)
}

func (s *Store) ClaimDelegationOperationWithHandoverSnapshot(requestID, operationID, sessionID, chiefSessionID, ticketID, requestJSON, resolvedPreferences string, handover DelegationHandoverSnapshot, now time.Time) (*DelegationOperationRecord, bool, error) {
	if strings.HasPrefix(requestID, "op-") {
		return nil, false, fmt.Errorf("request id uses reserved operation prefix op-")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, false, errors.New("delegation idempotency requires a database")
	}
	if resolvedPreferences != "" {
		if existing, err := getDelegationOperation(s.db, requestID); err == nil {
			if existing.RequestJSON != requestJSON {
				return nil, false, ErrDelegationRequestConflict
			}
			return existing, false, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, err
		}
		var resolved delegationprefs.Resolved
		if err := json.Unmarshal([]byte(resolvedPreferences), &resolved); err != nil {
			return nil, false, err
		}
		current, err := s.readDelegationPreferences(s.db)
		if err != nil {
			return nil, false, err
		}
		if !current.Enabled || current.Revision != resolved.Revision {
			return nil, false, delegationprefs.ErrConflict
		}
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	result, err := s.db.Exec(`INSERT INTO delegation_operations
		(request_id, operation_id, request_json, state, progress, session_id, chief_session_id, ticket_id, resolved_preferences,
		 handover_seed_rev, handover_tender_session, handover_tender_member, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(request_id) DO NOTHING`, requestID, operationID, requestJSON,
		string(protocol.DelegationOperationStateAccepted), "accepted by daemon", sessionID, chiefSessionID, ticketID, resolvedPreferences,
		handover.SeedRev, handover.TenderSession, handover.TenderMember, stamp, stamp)
	if err != nil {
		if ticketID != "" && strings.Contains(err.Error(), "delegation_operations.ticket_id") {
			return nil, false, fmt.Errorf("%w: %s", ErrTicketDelegationReserved, ticketID)
		}
		return nil, false, fmt.Errorf("claim delegation operation: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, false, fmt.Errorf("claim delegation operation rows: %w", err)
	}
	record, err := getDelegationOperation(s.db, requestID)
	if err != nil {
		return nil, false, err
	}
	if record.RequestJSON != requestJSON {
		return nil, false, ErrDelegationRequestConflict
	}
	return record, rows == 1, nil
}

func (s *Store) GetDelegationOperation(id string) (*DelegationOperationRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, sql.ErrNoRows
	}
	return getDelegationOperation(s.db, id)
}

func getDelegationOperation(db *sql.DB, id string) (*DelegationOperationRecord, error) {
	var rec DelegationOperationRecord
	var state, workspaceID, ticketID, directory, branch, baseCommit, handoffNoteID, worktreePath, worktreeToken, chiefSessionID, resultJSON, errorText, failureCode string
	var worktreeOwned int
	err := db.QueryRow(`SELECT request_id, operation_id, request_json, state, progress,
		session_id, workspace_id, ticket_id, directory, branch, base_commit, handoff_note_id, worktree_path, worktree_owned, worktree_token, chief_session_id,
		handover_seed_rev, handover_tender_session, handover_tender_member, result_json, error, failure_code, resolved_preferences, created_at, updated_at
		FROM delegation_operations WHERE request_id = ? OR operation_id = ?`, id, id).Scan(
		&rec.Operation.RequestID, &rec.Operation.OperationID, &rec.RequestJSON, &state,
		&rec.Operation.Progress, &rec.Operation.SessionID, &workspaceID, &ticketID,
		&directory, &branch, &baseCommit, &handoffNoteID, &worktreePath, &worktreeOwned, &worktreeToken, &chiefSessionID,
		&rec.HandoverSeedRev, &rec.HandoverTenderSession, &rec.HandoverTenderMember,
		&resultJSON, &errorText, &failureCode, &rec.ResolvedPreferences, &rec.Operation.CreatedAt, &rec.Operation.UpdatedAt)
	if err != nil {
		return nil, err
	}
	rec.Operation.State = protocol.DelegationOperationState(state)
	rec.WorktreeOwned = worktreeOwned == 1
	rec.WorktreeToken = worktreeToken
	rec.ChiefSessionID = chiefSessionID
	rec.BaseCommit = baseCommit
	rec.HandoffNoteID = handoffNoteID
	if workspaceID != "" {
		rec.Operation.WorkspaceID = protocol.Ptr(workspaceID)
	}
	if ticketID != "" {
		if delegationRequestUsesSeed(rec.RequestJSON) {
			rec.Operation.SeedID = protocol.Ptr(ticketID)
		} else {
			rec.Operation.TicketID = protocol.Ptr(ticketID)
		}
	}
	if directory != "" {
		rec.Operation.Directory = protocol.Ptr(directory)
	}
	if branch != "" {
		rec.Operation.Branch = protocol.Ptr(branch)
	}
	if worktreePath != "" {
		rec.Operation.WorktreePath = protocol.Ptr(worktreePath)
	}
	if errorText != "" {
		rec.Operation.Error = protocol.Ptr(errorText)
		if failureCode == "" {
			failureCode = "delegation_failed"
		}
		rec.Operation.Failure = &protocol.DelegationFailure{Code: failureCode, Message: errorText}
	}
	if resultJSON != "" {
		var result protocol.DelegateResult
		if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
			return nil, fmt.Errorf("decode delegation result: %w", err)
		}
		rec.Operation.Result = &result
	}
	return &rec, nil
}

func delegationRequestUsesSeed(requestJSON string) bool {
	var request struct {
		Assignment json.RawMessage `json:"assignment"`
	}
	if json.Unmarshal([]byte(requestJSON), &request) != nil || len(request.Assignment) == 0 {
		return false
	}
	var assignment struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(request.Assignment, &assignment) == nil && assignment.Kind != ""
}

func (s *Store) RecordDelegationResolution(id, seedID, directory, branch, baseCommit, handoffNoteID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stamp := now.UTC().Format(sortableTimeFormat)
	_, err := s.db.Exec(`UPDATE delegation_operations SET
		ticket_id = CASE WHEN ? = '' THEN ticket_id ELSE ? END,
		directory = CASE WHEN ? = '' THEN directory ELSE ? END,
		branch = CASE WHEN ? = '' THEN branch ELSE ? END,
		base_commit = CASE WHEN ? = '' THEN base_commit ELSE ? END,
		handoff_note_id = CASE WHEN ? = '' THEN handoff_note_id ELSE ? END,
		updated_at = ? WHERE request_id = ? OR operation_id = ?`,
		seedID, seedID, directory, directory, branch, branch, baseCommit, baseCommit,
		handoffNoteID, handoffNoteID, stamp, id, id)
	return err
}

func (s *Store) MarkDelegationWorktreeOwned(id, path, token string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stamp := now.UTC().Format(sortableTimeFormat)
	_, err := s.db.Exec(`UPDATE delegation_operations SET worktree_path = ?, worktree_owned = 1, worktree_token = ?, updated_at = ?
		WHERE request_id = ? OR operation_id = ?`, path, token, stamp, id, id)
	return err
}

func (s *Store) UpdateDelegationOperation(id string, state protocol.DelegationOperationState, progress, workspaceID, ticketID, worktreePath string, result *protocol.DelegateResult, operationErr error, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("delegation idempotency requires a database")
	}
	resultJSON := ""
	if result != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("encode delegation result: %w", err)
		}
		resultJSON = string(encoded)
	}
	errorText := ""
	if operationErr != nil {
		errorText = operationErr.Error()
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	failureCode := ""
	resultDirectory := ""
	resultBranch := ""
	if operationErr != nil {
		failureCode = delegationFailureCode(errorText)
	}
	if result != nil {
		if ticketID == "" {
			ticketID = result.SeedID
		}
		resultDirectory = result.Directory
		resultBranch = protocol.Deref(result.Branch)
	}
	_, err := s.db.Exec(`UPDATE delegation_operations SET state = ?, progress = ?,
		workspace_id = CASE WHEN ? = '' THEN workspace_id ELSE ? END,
		ticket_id = CASE WHEN ? = '' THEN ticket_id ELSE ? END,
		directory = CASE WHEN ? = '' THEN directory ELSE ? END,
		branch = CASE WHEN ? = '' THEN branch ELSE ? END,
		worktree_path = CASE WHEN ? = '' THEN worktree_path ELSE ? END,
		result_json = ?, error = ?, failure_code = ?, updated_at = ? WHERE request_id = ? OR operation_id = ?`,
		string(state), progress, workspaceID, workspaceID, ticketID, ticketID,
		resultDirectory, resultDirectory, resultBranch, resultBranch,
		worktreePath, worktreePath, resultJSON, errorText, failureCode, stamp, id, id)
	return err
}

func delegationFailureCode(message string) string {
	switch {
	case strings.Contains(message, "retired implicit launch contract"):
		return "legacy_request_requires_explicit_retry"
	case strings.Contains(message, "base ref"):
		return "invalid_base_ref"
	case strings.Contains(message, "active Attn session") || strings.Contains(message, "worktree") || strings.Contains(message, "checkout"):
		return "checkout_conflict"
	case strings.Contains(message, "seed") || strings.Contains(message, "handover") || strings.Contains(message, "holder"):
		return "assignment_conflict"
	case strings.Contains(message, "spawn") || strings.Contains(message, "first turn") || strings.Contains(message, "exited"):
		return "worker_launch_failed"
	default:
		return "delegation_failed"
	}
}

func (s *Store) PendingDelegationOperations() ([]DelegationOperationRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT request_id FROM delegation_operations WHERE state IN (?, ?) ORDER BY created_at`,
		string(protocol.DelegationOperationStateAccepted), string(protocol.DelegationOperationStatePreparing))
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var out []DelegationOperationRecord
	for _, id := range ids {
		rec, err := getDelegationOperation(s.db, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, nil
}
