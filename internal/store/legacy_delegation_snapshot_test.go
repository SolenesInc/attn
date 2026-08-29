package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeLegacyDelegationSnapshot(t *testing.T, path string, version int, optionalColumns bool) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	extra := ""
	if optionalColumns {
		extra = ", worktree_token TEXT NOT NULL DEFAULT '', chief_session_id TEXT NOT NULL DEFAULT ''"
	}
	ddl := `
		CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
		CREATE TABLE delegation_operations (
			request_id TEXT PRIMARY KEY, operation_id TEXT NOT NULL UNIQUE, request_json TEXT NOT NULL,
			state TEXT NOT NULL, progress TEXT NOT NULL, session_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL DEFAULT '', ticket_id TEXT NOT NULL DEFAULT '',
			worktree_path TEXT NOT NULL DEFAULT '', worktree_owned INTEGER NOT NULL DEFAULT 0,
			result_json TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL` + extra + `
		);`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations VALUES (?, '2026-01-01T00:00:00Z')`, version); err != nil {
		t.Fatal(err)
	}
	columns := `request_id,operation_id,request_json,state,progress,session_id,workspace_id,ticket_id,
		worktree_path,worktree_owned,result_json,error,created_at,updated_at`
	values := `?,?,?,?,?,?,?,?,?,?,?,?,?,?`
	args := []any{
		"request-1", "operation-1", ` {"brief":"keep exact spacing"} `, "completed", "ready",
		"session-1", "workspace-1", "lost-ticket", "/worktree", 1,
		` {"session_id":"session-1"} `, "", "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z",
	}
	if optionalColumns {
		columns += `,worktree_token,chief_session_id`
		values += `,?,?`
		args = append(args, "token-1", "chief-1")
	}
	if _, err := db.Exec(`INSERT INTO delegation_operations (`+columns+`) VALUES (`+values+`)`, args...); err != nil {
		t.Fatal(err)
	}
}

func TestReadLegacyDelegationOperationsSnapshotPreservesEveryRawField(t *testing.T) {
	for _, tc := range []struct {
		name             string
		version          int
		optionalColumns  bool
		wantToken, chief string
	}{
		{name: "original schema", version: 70},
		{name: "current schema", version: LatestSchemaVersion(), optionalColumns: true, wantToken: "token-1", chief: "chief-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snapshot.db")
			writeLegacyDelegationSnapshot(t, path, tc.version, tc.optionalColumns)
			before, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			read, err := ReadLegacyDelegationOperationsSnapshot(path)
			if err != nil || read.SchemaVersion != tc.version || len(read.Operations) != 1 {
				t.Fatalf("read = %#v, %v", read, err)
			}
			operation := read.Operations[0]
			if operation.RequestJSON != ` {"brief":"keep exact spacing"} ` || operation.ResultJSON != ` {"session_id":"session-1"} ` {
				t.Fatalf("raw JSON changed: %#v", operation)
			}
			if operation.TicketID != "lost-ticket" || operation.WorktreeOwned != 1 || operation.WorktreeToken != tc.wantToken || operation.ChiefSessionID != tc.chief {
				t.Fatalf("operation = %#v", operation)
			}
			after, err := os.ReadDir(filepath.Dir(path))
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("immutable read changed source directory: before=%v after=%v err=%v", before, after, err)
			}
		})
	}
}

func TestReadLegacyDelegationOperationsSnapshotHandlesAbsentAndFutureSchemas(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent.db")
	db, err := sql.Open("sqlite3", absent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
		INSERT INTO schema_migrations VALUES (69, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if read, err := ReadLegacyDelegationOperationsSnapshot(absent); err != nil || len(read.Operations) != 0 {
		t.Fatalf("absent table read = %#v, %v", read, err)
	}

	future := filepath.Join(t.TempDir(), "future.db")
	writeLegacyDelegationSnapshot(t, future, LatestSchemaVersion()+1, true)
	if _, err := ReadLegacyDelegationOperationsSnapshot(future); err == nil || !strings.Contains(err.Error(), "future schema") {
		t.Fatalf("future schema error = %v", err)
	}
}
