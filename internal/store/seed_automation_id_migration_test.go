package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
)

func TestBackfillSeedAutomationIDMigrationStampsExistingAutomationSeedsOnly(t *testing.T) {
	const beforeBackfill = 1795485089532374 - 1
	path := filepath.Join(t.TempDir(), "seed-automation-id.db")
	old, err := newStoreAtVersion(path, beforeBackfill)
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := mustCreateProfile(t, old, "Work")
	for _, query := range []string{
		`INSERT INTO automation_definitions(id,name,enabled,revision,spec_json,created_at,updated_at,profile_id) VALUES
			(10,'sweep',1,1,'{}','2026-11-01T00:00:00Z','2026-11-01T00:00:00Z','` + profile.ID + `'),
			(20,'triage',1,1,'{}','2026-11-01T00:00:00Z','2026-11-01T00:00:00Z','` + profile.ID + `')`,
		`INSERT INTO automation_occurrences(id,definition_id,provider,occurrence_key,subject_key,observed_at,payload_json,created_at) VALUES
			('occ-1',10,'schedule','k1','s1','2026-11-01T00:00:00Z','{}','2026-11-01T00:00:00Z'),
			('occ-2',20,'schedule','k2','s2','2026-11-02T00:00:00Z','{}','2026-11-02T00:00:00Z'),
			('occ-3',10,'schedule','k3','s3','2026-11-03T00:00:00Z','{}','2026-11-03T00:00:00Z')`,
		`INSERT INTO automation_runs(id,definition_id,occurrence_id,definition_revision,snapshot_json,state,ticket_id,session_id,created_at,updated_at,seed_id,profile_id) VALUES
			('run-1',10,'occ-1',1,'{}','delivered','','sess-1','2026-11-01T00:00:00Z','2026-11-01T00:00:00Z','s-fromrn','` + profile.ID + `'),
			('run-2',20,'occ-2',1,'{}','delivered','','sess-2','2026-11-02T00:00:00Z','2026-11-02T00:00:00Z','s-fromrn','` + profile.ID + `'),
			('run-3',10,'occ-3',1,'{}','delivered','','sess-4','2026-11-03T00:00:00Z','2026-11-03T00:00:00Z','s-alrdy1','` + profile.ID + `')`,
		`INSERT INTO automation_continuity_bindings(id,definition_id,continuity_key,ticket_id,session_id,status,created_at,updated_at,seed_id,profile_id) VALUES
			('bind-1',10,'ck-1','','sess-3','active','2026-11-01T00:00:00Z','2026-11-01T00:00:00Z','s-frombd','` + profile.ID + `')`,
	} {
		if _, err := old.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	schema := garden.SeedsSchema()
	if _, err := old.DefineDocumentCollection(schema, now); err != nil {
		t.Fatal(err)
	}
	declared, _, err := old.DocumentCollection(schema.Namespace, schema.Collection)
	if err != nil {
		t.Fatal(err)
	}
	put := func(id string, body map[string]any) {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := old.PutDocument(*declared, id, encoded, now, nil); err != nil {
			t.Fatal(err)
		}
	}
	put("s-fromrn", map[string]any{"id": "s-fromrn", "profile_id": profile.ID, "title": "From run", "status": "growing", "step_slug": "from-run", "edges": []any{}, "vars": []any{}})
	put("s-frombd", map[string]any{"id": "s-frombd", "profile_id": profile.ID, "title": "From binding", "status": "growing", "step_slug": "from-binding", "edges": []any{}, "vars": []any{}})
	put("s-manual", map[string]any{"id": "s-manual", "profile_id": profile.ID, "title": "Manual", "status": "planted", "step_slug": "manual", "edges": []any{}, "vars": []any{}})
	put("s-alrdy1", map[string]any{"id": "s-alrdy1", "profile_id": profile.ID, "title": "Already stamped", "status": "growing", "step_slug": "already", "automation_id": 99, "edges": []any{}, "vars": []any{}})
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()

	seeds, _, err := upgraded.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id      string
		wantID  int
		wantRev int64
	}{
		{"s-fromrn", 20, 2},
		{"s-frombd", 10, 2},
		{"s-manual", 0, 1},
		{"s-alrdy1", 99, 1},
	} {
		doc, found, err := upgraded.GetDocument(*seeds, tc.id)
		if err != nil || !found {
			t.Fatalf("seed %s: found=%v %v", tc.id, found, err)
		}
		seed, err := garden.Decode(doc.Body)
		if err != nil {
			t.Fatalf("decode %s: %v", tc.id, err)
		}
		if seed.AutomationID != tc.wantID || doc.Rev != tc.wantRev {
			t.Errorf("%s: automation_id=%d rev=%d, want automation_id=%d rev=%d", tc.id, seed.AutomationID, doc.Rev, tc.wantID, tc.wantRev)
		}
	}
}
