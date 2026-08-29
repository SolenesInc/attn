package store

import (
	"database/sql"
	"fmt"
	"net/url"
)

type LegacyDelegationOperation struct {
	RequestID      string `json:"request_id"`
	OperationID    string `json:"operation_id"`
	RequestJSON    string `json:"request_json"`
	State          string `json:"state"`
	Progress       string `json:"progress"`
	SessionID      string `json:"session_id"`
	WorkspaceID    string `json:"workspace_id"`
	TicketID       string `json:"ticket_id"`
	WorktreePath   string `json:"worktree_path"`
	WorktreeOwned  int64  `json:"worktree_owned"`
	WorktreeToken  string `json:"worktree_token"`
	ChiefSessionID string `json:"chief_session_id"`
	ResultJSON     string `json:"result_json"`
	Error          string `json:"error"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type LegacyDelegationSnapshotRead struct {
	SchemaVersion int
	Operations    []LegacyDelegationOperation
}

func ReadLegacyDelegationOperationsSnapshot(path string) (LegacyDelegationSnapshotRead, error) {
	u := &url.URL{Scheme: "file", Path: path}
	query := u.Query()
	query.Set("mode", "ro")
	query.Set("immutable", "1")
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return LegacyDelegationSnapshotRead{}, fmt.Errorf("open immutable delegation snapshot: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return LegacyDelegationSnapshotRead{}, fmt.Errorf("delegation snapshot integrity check: %w", err)
	}
	if integrity != "ok" {
		return LegacyDelegationSnapshotRead{}, fmt.Errorf("delegation snapshot integrity check: %s", integrity)
	}
	version, err := GetSchemaVersion(db)
	if err != nil {
		return LegacyDelegationSnapshotRead{}, fmt.Errorf("delegation snapshot schema version: %w", err)
	}
	if version > LatestSchemaVersion() {
		return LegacyDelegationSnapshotRead{}, fmt.Errorf("unsupported future schema version %d (binary knows through %d)", version, LatestSchemaVersion())
	}
	result := LegacyDelegationSnapshotRead{SchemaVersion: version}
	if version < 70 || !hasSnapshotTable(db, "delegation_operations") {
		return result, nil
	}
	result.Operations, err = readLegacyDelegationOperations(db)
	if err != nil {
		return LegacyDelegationSnapshotRead{}, fmt.Errorf("read delegation operations from schema %d: %w", version, err)
	}
	return result, nil
}

func (s *Store) ListLegacyDelegationOperations() ([]LegacyDelegationOperation, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return readLegacyDelegationOperations(s.db)
}

func readLegacyDelegationOperations(db *sql.DB) ([]LegacyDelegationOperation, error) {
	required := []string{
		"request_id", "operation_id", "request_json", "state", "progress", "session_id",
		"workspace_id", "ticket_id", "worktree_path", "worktree_owned", "result_json",
		"error", "created_at", "updated_at",
	}
	for _, column := range required {
		has, err := snapshotHasColumn(db, "delegation_operations", column)
		if err != nil {
			return nil, err
		}
		if !has {
			return nil, fmt.Errorf("delegation_operations.%s is missing", column)
		}
	}
	worktreeToken, chiefSessionID := `''`, `''`
	if has, err := snapshotHasColumn(db, "delegation_operations", "worktree_token"); err != nil {
		return nil, err
	} else if has {
		worktreeToken = "worktree_token"
	}
	if has, err := snapshotHasColumn(db, "delegation_operations", "chief_session_id"); err != nil {
		return nil, err
	} else if has {
		chiefSessionID = "chief_session_id"
	}
	rows, err := db.Query(`SELECT request_id,operation_id,request_json,state,progress,session_id,
		workspace_id,ticket_id,worktree_path,worktree_owned,` + worktreeToken + `,` + chiefSessionID + `,
		result_json,error,created_at,updated_at FROM delegation_operations
		ORDER BY ticket_id,updated_at,request_id,operation_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LegacyDelegationOperation
	for rows.Next() {
		var operation LegacyDelegationOperation
		if err := rows.Scan(
			&operation.RequestID, &operation.OperationID, &operation.RequestJSON, &operation.State,
			&operation.Progress, &operation.SessionID, &operation.WorkspaceID, &operation.TicketID,
			&operation.WorktreePath, &operation.WorktreeOwned, &operation.WorktreeToken,
			&operation.ChiefSessionID, &operation.ResultJSON, &operation.Error,
			&operation.CreatedAt, &operation.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, operation)
	}
	return out, rows.Err()
}
