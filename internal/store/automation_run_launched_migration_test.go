package store

import (
	"path/filepath"
	"testing"
)

func TestUpgradeMarksPendingRunsLaunchedOnlyWhenTheirSessionStartedAfterTheRunWasCreated(t *testing.T) {
	const beforeBackfill = 1793993403875462 - 1
	cases := []struct {
		name, state, createdAt, sessionLaunchedAt string
		launched                                  bool
	}{
		{"agent started for this run", "pending", "2026-11-01T10:00:00Z", "2026-11-01T10:00:05.5Z", true},
		{"agent started in the second the run was created", "pending", "2026-11-01T10:00:00Z", "2026-11-01T10:00:00.5Z", true},
		{"session started for an earlier run", "pending", "2026-11-01T10:00:00Z", "2026-11-01T09:00:00.5Z", false},
		{"session never started", "pending", "2026-11-01T10:00:00Z", "", false},
		{"run already finished", "delivered", "2026-11-01T10:00:00Z", "2026-11-01T10:00:05.5Z", false},
	}
	path := filepath.Join(t.TempDir(), "runs.db")
	old, err := newStoreAtVersion(path, beforeBackfill)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`INSERT INTO automation_definitions(id,name,enabled,revision,spec_json,created_at,updated_at) VALUES(1,'review',1,1,'{}','2026-11-01T00:00:00Z','2026-11-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		if _, err := old.db.Exec(`INSERT INTO sessions(id,user_label,directory,launched_at) VALUES(?,'run','/tmp/run',?)`, tc.name, tc.sessionLaunchedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := old.db.Exec(`INSERT INTO automation_occurrences(id,definition_id,provider,occurrence_key,subject_key,observed_at,payload_json,created_at) VALUES(?,1,'github',?,'subject',?,'{}',?)`, tc.name, tc.name, tc.createdAt, tc.createdAt); err != nil {
			t.Fatal(err)
		}
		if _, err := old.db.Exec(`INSERT INTO automation_runs(id,definition_id,occurrence_id,definition_revision,snapshot_json,state,ticket_id,session_id,created_at,updated_at) VALUES(?,1,?,1,'{}',?,'',?,?,?)`, tc.name, tc.name, tc.state, tc.name, tc.createdAt, tc.createdAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	for _, tc := range cases {
		run, err := upgraded.GetAutomationRun(tc.name)
		if err != nil || run == nil {
			t.Fatalf("%s: run %+v, %v", tc.name, run, err)
		}
		if launched := run.LaunchedAt != nil; launched != tc.launched {
			t.Errorf("%s: launched = %v (%v), want %v", tc.name, launched, run.LaunchedAt, tc.launched)
		}
	}
}
