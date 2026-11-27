package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAMachineThatAdoptsTheIDItsHomeGaveItKeepsNoReferenceToItsOldID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outpost.db")
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old, given := s.MachineID(), "machine-the-home-gave"
	profile, desktop := mustCreateProfile(t, s, "Work")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	s.Add(&protocol.Session{ID: "own", Label: "own", Agent: "claude", Directory: "/work", ProfileID: profile.ID})
	if got := s.Get("own"); got == nil || got.Host != nil {
		t.Fatalf("before adoption: %+v, want this machine", got)
	}
	exec(`INSERT INTO recent_locations (host, path, last_seen) VALUES (?, '/work', '2026-11-20T00:00:00Z'), (?, '/work', '2026-11-26T00:00:00Z'), (?, '/other', '2026-11-20T00:00:00Z')`, old, given, old)
	exec(`INSERT INTO file_activity (host, path, source, last_at) VALUES (?, '/work/notes.md', 'open', '2026-11-20T00:00:00Z')`, old)
	exec(`INSERT INTO markdown_annotation_drafts (host, path, annotations_json, generation, updated_at) VALUES (?, '/work/notes.md', '[]', 1, '2026-11-20T00:00:00Z')`, old)
	exec(`INSERT INTO code_nav_setups (host, repository, language, session_id) VALUES (?, '/work', 'go', 'own')`, old)
	exec(`INSERT INTO delegation_operations (request_id, operation_id, request_json, state, progress, session_id, created_at, updated_at) VALUES ('r', 'o', json_object('resolved_host', ?), 'done', '', 'own', '', '')`, old)
	exec(`INSERT INTO settings (key, value) VALUES (?, 'true'), (?, 'dismissed'), (?, 'worktree'), (?, 'kept')`,
		"new_session_yolo_"+old, "code_review_setup_offer_"+old+"_/work", "new_session_destination_"+old+"_"+profile.ID, "new_session_destination_"+given+"_"+profile.ID)
	exec(`INSERT INTO settings (key, value) VALUES (?, 'written later')`, "new_session_destination_"+old+"_"+profile.ID+"x")
	exec(`INSERT INTO settings (key, value) VALUES (?, 'shadowed')`, "code_review_setup_offer_"+old+"_/copied")
	if err := transactWritingCopies(context.Background(), s.db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO settings (key, value, copied_from) VALUES (?, 'from home', 'home-1')`, "code_review_setup_offer_"+given+"_/copied"); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO sessions (id, user_label, directory, created_at, profile_id, host, copied_from, closed_at) VALUES ('copy-under-old-id', 'copy', '/far', '', ?, ?, 'home-1', '')`, profile.ID, old)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	for _, schema := range []struct {
		collection docstore.CollectionSchema
		id         string
		body       map[string]any
	}{
		{garden.DispatchesSchema(), "own", map[string]any{"session_id": "own", "crown": "s-1", "machine": old}},
		{garden.BedsSchema(), "bed", map[string]any{"id": "bed", "name": "work", "repo_paths": []map[string]string{{"path": "/work", "machine": old}, {"path": "/far", "machine": "far"}}}},
	} {
		if _, err := s.DefineDocumentCollection(schema.collection, now); err != nil {
			t.Fatal(err)
		}
		declared, _, err := s.DocumentCollection(schema.collection.Namespace, schema.collection.Collection)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(schema.body)
		if _, err := s.PutDocument(*declared, schema.id, encoded, now, nil); err != nil {
			t.Fatal(err)
		}
	}

	beds, _, err := s.DocumentCollection(garden.Namespace, "beds")
	if err != nil {
		t.Fatal(err)
	}
	var memo DocumentsMemo[string]
	bedMachine := func() string {
		t.Helper()
		machine, err := RememberDocuments(s, &memo, *beds, func() (string, error) {
			doc, _, err := s.GetDocument(*beds, "bed")
			var bed struct {
				RepoPaths []garden.RepoPath `json:"repo_paths"`
			}
			if err == nil {
				err = json.Unmarshal(doc.Body, &bed)
			}
			return bed.RepoPaths[0].Machine, err
		}, func(previous string, _ []string) (string, error) { return previous, nil })
		if err != nil {
			t.Fatal(err)
		}
		return machine
	}
	if got := bedMachine(); got != old {
		t.Fatalf("bed machine before adoption = %q, want %q", got, old)
	}

	notes := layouttree.MarkdownTileID(old, "/work/notes.md")
	params, _ := json.Marshal(map[string]string{"host": old, "path": "/work/notes.md"})
	tree := layouttree.Node{Type: "split", SplitID: "split-1", Direction: layouttree.DirectionVertical, Ratio: 0.5, Children: []layouttree.Node{
		{Type: "tile", TileID: notes, TileKind: string(layouttree.TileKindMarkdown), TileParams: string(params)},
		{Type: "tile", TileID: "tile-docked-by-app", TileKind: string(layouttree.TileKindMarkdown), TileParams: string(params)},
	}}
	encoded, err := layouttree.EncodeLayout(tree)
	if err != nil {
		t.Fatal(err)
	}
	history, _ := json.Marshal([]string{"tile-docked-by-app", notes})
	exec(`UPDATE desktops SET tree_json = ?, active_pane_id = ?, focus_history = ? WHERE id = ?`, encoded, notes, string(history), desktop.ID)

	if err := s.AdoptMachineID(given); err != nil {
		t.Fatal(err)
	}
	if err := s.AdoptMachineID(given); err != nil {
		t.Fatalf("adopting the same id again: %v", err)
	}
	if s.MachineID() != given {
		t.Fatalf("machine id = %q, want %q", s.MachineID(), given)
	}
	if left := columnsHolding(t, s, old); !slices.Equal(left, []string{"sessions.host"}) {
		t.Errorf("references to the old machine id remain in %v, want only the copy the home sent", left)
	}
	if got := s.CopiedSettings("home-1")["code_review_setup_offer_"+given+"_/copied"]; got != "from home" {
		t.Errorf("the home's copied setting = %q, want it kept", got)
	}
	var own string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ? AND copied_from = ''`, "code_review_setup_offer_"+given+"_/copied").Scan(&own); err != nil || own != "shadowed" {
		t.Errorf("this machine's own setting beside the home's copy = %q, %v; want it kept for after a leave", own, err)
	}
	if got := s.Get("own"); got == nil || got.Host != nil {
		t.Errorf("after adoption: %+v, want this machine", got)
	}
	for key, want := range map[string]string{
		"new_session_yolo_" + given:                                 "true",
		"code_review_setup_offer_" + given + "_/work":               "dismissed",
		"new_session_destination_" + given + "_" + profile.ID:       "kept",
		"new_session_destination_" + given + "_" + profile.ID + "x": "written later",
	} {
		if got := s.GetSetting(key); got != want {
			t.Errorf("setting %s = %q, want %q", key, got, want)
		}
	}
	var lastSeen string
	if err := s.db.QueryRow(`SELECT last_seen FROM recent_locations WHERE host = ? AND path = '/work'`, given).Scan(&lastSeen); err != nil || lastSeen != "2026-11-26T00:00:00Z" {
		t.Errorf("recent /work = %q, %v; want the row already stored under the new id", lastSeen, err)
	}
	if got := bedMachine(); got != given {
		t.Errorf("a bed read through its memo shows machine %q after adoption, want %q", got, given)
	}
	tiles, active, focus := shownMarkdownTiles(t, s, desktop.ID)
	moved := layouttree.MarkdownTileID(given, "/work/notes.md")
	if tiles[moved] != (shownTile{machine: given, path: "/work/notes.md"}) || tiles["tile-docked-by-app"] != (shownTile{machine: given, path: "/work/notes.md"}) {
		t.Errorf("tiles = %+v, want both showing /work/notes.md on %s", tiles, given)
	}
	if active != moved || len(focus) != 2 || focus[0] != "tile-docked-by-app" || focus[1] != moved {
		t.Errorf("active %q, focus %v; want %q and [tile-docked-by-app %s]", active, focus, moved, moved)
	}
}

func columnsHolding(t *testing.T, s *Store, value string) []string {
	t.Helper()
	tables, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	tables.Close()
	var holding []string
	for _, table := range names {
		columns, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		var cols []string
		for columns.Next() {
			var col string
			if err := columns.Scan(&col); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, col)
		}
		columns.Close()
		for _, col := range cols {
			var n int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM "`+table+`" WHERE instr(CAST("`+col+`" AS TEXT), ?) > 0`, value).Scan(&n); err != nil {
				t.Fatalf("%s.%s: %v", table, col, err)
			}
			if n > 0 {
				holding = append(holding, table+"."+col)
			}
		}
	}
	return holding
}
