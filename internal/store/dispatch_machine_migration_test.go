package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
)

func TestBackfillDispatchMachineMigrationRecordsSessionHostAndLeavesOrphansUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatches.db")
	old, err := newStoreAtVersion(path, 1795466678307445-1)
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := mustCreateProfile(t, old, "Work")
	if _, err := old.db.Exec(`
INSERT INTO sessions(id,user_label,directory,created_at,profile_id,host,closed_at) VALUES
 ('home-open','Home open','/work/home','',?,'',''),
 ('home-closed','Home closed','/work/home-old','',?,'','2026-11-18T10:00:00Z'),
 ('far-open','Far open','/home/user/repo','',?,'outpost-far',''),
 ('far-closed','Far closed','/home/user/repo-old','',?,'outpost-far','2026-11-18T11:00:00Z');
`, profile.ID, profile.ID, profile.ID, profile.ID); err != nil {
		t.Fatal(err)
	}
	if err := transactWritingCopies(context.Background(), old.db, func(tx *sql.Tx) error {
		_, execErr := tx.Exec(`
INSERT INTO sessions(id,user_label,directory,created_at,profile_id,host,copied_from,closed_at) VALUES
 ('copied-outpost','Copied outpost','/home/user/local','',?,'outpost-far','d-home','')
`, profile.ID)
		return execErr
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	schema := garden.DispatchesSchema()
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
	put("home-open", map[string]any{"session_id": "home-open", "crown": "s-1", "cwd": "/work/home"})
	put("home-closed", map[string]any{"session_id": "home-closed", "crown": "s-2", "cwd": "/work/home-old"})
	put("far-open", map[string]any{"session_id": "far-open", "crown": "s-3", "cwd": "/home/user/repo"})
	put("far-closed", map[string]any{"session_id": "far-closed", "crown": "s-4", "cwd": "/home/user/repo-old"})
	put("copied-outpost", map[string]any{"session_id": "copied-outpost", "crown": "s-8", "cwd": "/home/user/local"})
	put("orphan", map[string]any{"session_id": "orphan", "crown": "s-5", "cwd": "/home/user/reaped"})
	put("already-outpost", map[string]any{"session_id": "already-outpost", "crown": "s-6", "machine": "outpost-kept"})
	put("already-home", map[string]any{"session_id": "already-home", "crown": "s-7", "machine": ""})
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()

	dispatches, _, err := upgraded.DocumentCollection(garden.Namespace, garden.CollectionDispatches)
	if err != nil {
		t.Fatal(err)
	}
	read := func(id string) (garden.Execution, int64) {
		t.Helper()
		doc, found, err := upgraded.GetDocument(*dispatches, id)
		if err != nil || !found {
			t.Fatalf("dispatch %s: found=%v %v", id, found, err)
		}
		exec, err := garden.DecodeExecution(doc.Body)
		if err != nil {
			t.Fatalf("decode %s: %v", id, err)
		}
		return exec, doc.Rev
	}

	for _, tc := range []struct {
		id      string
		want    string
		wantRev int64
	}{
		{"home-open", upgraded.MachineID(), 3},
		{"home-closed", upgraded.MachineID(), 3},
		{"far-open", "outpost-far", 2},
		{"far-closed", "outpost-far", 2},
		{"copied-outpost", upgraded.MachineID(), 3},
		{"already-outpost", "outpost-kept", 1},
		{"already-home", upgraded.MachineID(), 2},
	} {
		exec, rev := read(tc.id)
		if exec.Machine == nil {
			t.Errorf("%s: Machine = nil, want %q", tc.id, tc.want)
			continue
		}
		if *exec.Machine != tc.want {
			t.Errorf("%s: Machine = %q, want %q", tc.id, *exec.Machine, tc.want)
		}
		if rev != tc.wantRev {
			t.Errorf("%s: rev = %d, want %d", tc.id, rev, tc.wantRev)
		}
	}

	orphan, orphanRev := read("orphan")
	if orphan.Machine != nil {
		t.Errorf("orphan: Machine = %q, want nil (unknown)", *orphan.Machine)
	}
	if orphanRev != 1 {
		t.Errorf("orphan: rev = %d, want 1", orphanRev)
	}
}
