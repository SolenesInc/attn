package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/layouttree"
)

const machineIDsMigration = 1795743017234424

var machineReferences = map[string]string{
	"sessions":                   "host",
	"recent_locations":           "host",
	"file_activity":              "host",
	"markdown_annotation_drafts": "host",
	"code_nav_setups":            "host",
}

func requireEveryMachineNamed(t *testing.T, s *Store) {
	t.Helper()
	machines := map[string]bool{}
	rows, err := s.db.Query(`SELECT id FROM machines`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		machines[id] = true
	}
	rows.Close()
	for table, column := range machineReferences {
		var unnamed int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE ` + column + ` IN ('', 'local')`).Scan(&unnamed); err != nil {
			t.Fatal(err)
		}
		if unnamed != 0 {
			t.Errorf("%s has %d rows whose %s names no machine", table, unnamed, column)
		}
	}
	var settingKeys []string
	keys, err := s.db.Query(`SELECT key FROM settings WHERE key LIKE '%local%'`)
	if err != nil {
		t.Fatal(err)
	}
	for keys.Next() {
		var key string
		if err := keys.Scan(&key); err != nil {
			t.Fatal(err)
		}
		settingKeys = append(settingKeys, key)
	}
	keys.Close()
	if len(settingKeys) != 0 {
		t.Errorf("settings still keyed by the local machine: %v", settingKeys)
	}
	var unresolved int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM delegation_operations WHERE json_extract(request_json, '$.resolved_host') IN ('', 'local')`).Scan(&unresolved); err != nil {
		t.Fatal(err)
	}
	if unresolved != 0 {
		t.Errorf("%d delegation operations resolved to no machine", unresolved)
	}
	for _, collection := range []string{garden.CollectionDispatches, "beds"} {
		declared, _, err := s.DocumentCollection(garden.Namespace, collection)
		if err != nil || declared == nil {
			continue
		}
		var bodies []string
		rows, err := s.db.Query(`SELECT body FROM ` + declared.Table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var body string
			if err := rows.Scan(&body); err != nil {
				t.Fatal(err)
			}
			bodies = append(bodies, body)
		}
		rows.Close()
		for _, body := range bodies {
			var doc struct {
				Machine   *string           `json:"machine"`
				RepoPaths []garden.RepoPath `json:"repo_paths"`
			}
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Machine != nil && !machines[*doc.Machine] {
				t.Errorf("%s %s names machine %q, which the machines table lacks", collection, body, *doc.Machine)
			}
			for _, repo := range doc.RepoPaths {
				if !machines[repo.Machine] {
					t.Errorf("bed repo path %s names machine %q, which the machines table lacks", repo.Path, repo.Machine)
				}
			}
		}
	}
}

type shownTile struct{ machine, path string }

func shownMarkdownTiles(t *testing.T, s *Store, desktopID string) (map[string]shownTile, string, []string) {
	t.Helper()
	desktop, err := s.GetDesktop(desktopID)
	if err != nil {
		t.Fatal(err)
	}
	tiles := map[string]shownTile{}
	for _, leaf := range layouttree.TileLeaves(desktop.Tree) {
		if leaf.TileKind != string(layouttree.TileKindMarkdown) {
			continue
		}
		var located struct{ Host, Path string }
		if strings.HasPrefix(leaf.TileParams, "{") {
			if err := json.Unmarshal([]byte(leaf.TileParams), &located); err != nil {
				t.Fatal(err)
			}
		} else {
			located.Path = leaf.TileParams
		}
		tiles[leaf.TileID] = shownTile{machine: located.Host, path: located.Path}
	}
	return tiles, desktop.ActivePaneID, desktop.FocusHistory
}

func TestMachineIDsMigrationNamesTheHomeMachineEverywhereAndKeepsTilesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "home.db")
	old, err := newStoreAtVersion(path, machineIDsMigration-1)
	if err != nil {
		t.Fatal(err)
	}
	profile, desktop := mustCreateProfile(t, old, "Work")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := old.db.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO outposts (id, name, destination, added_at) VALUES ('outpost-far', 'far', 'user@far', '2026-11-01T00:00:00Z')`)
	exec(`INSERT INTO sessions(id,user_label,directory,created_at,profile_id,host,closed_at) VALUES
 ('home-open','Home','/work/home','',?,'',''),
 ('home-closed','Home old','/work/old','',?,'','2026-11-18T10:00:00Z'),
 ('far-open','Far','/home/user/repo','',?,'outpost-far','')`, profile.ID, profile.ID, profile.ID)
	exec(`INSERT INTO recent_locations (host, path, last_seen) VALUES ('', '/work/home', '2026-11-20T00:00:00Z'), ('outpost-far', '/home/user/repo', '2026-11-20T00:00:00Z')`)
	exec(`INSERT INTO file_activity (host, path, source, last_at) VALUES ('', '/work/home/notes.md', 'open', '2026-11-20T00:00:00Z'), ('outpost-far', '/home/user/repo/plan.md', 'open', '2026-11-20T00:00:00Z')`)
	exec(`INSERT INTO markdown_annotation_drafts (host, path, annotations_json, generation, updated_at) VALUES ('', '/work/home/notes.md', '[]', 1, '2026-11-20T00:00:00Z'), ('', 'attn://seed/s-1', '[]', 1, '2026-11-20T00:00:00Z'), ('outpost-far', '/home/user/repo/plan.md', '[]', 1, '2026-11-20T00:00:00Z')`)
	exec(`INSERT INTO code_nav_setups (host, repository, language, session_id) VALUES ('', '/work/home', 'go', 'home-open'), ('outpost-far', '/home/user/repo', 'rust', 'far-open')`)
	exec(`INSERT INTO delegation_operations (request_id, operation_id, request_json, state, progress, session_id, created_at, updated_at) VALUES
 ('r-home', 'o-home', '{"resolved_host":""}', 'done', '', 'home-open', '', ''),
 ('r-far', 'o-far', '{"resolved_host":"outpost-far"}', 'done', '', 'far-open', '', '')`)
	exec(`INSERT INTO settings (key, value) VALUES ('new_session_yolo_local', 'true'), ('code_review_setup_offer_local_/work/home', 'dismissed'), ('new_session_destination_local_profile-1', 'worktree'), ('new_session_yolo_outpost-far', 'false')`)

	now := time.Now()
	for _, schema := range []struct {
		name string
		docs map[string]any
	}{
		{name: garden.CollectionDispatches, docs: map[string]any{
			"home-open": map[string]any{"session_id": "home-open", "crown": "s-1", "machine": ""},
			"far-open":  map[string]any{"session_id": "far-open", "crown": "s-2", "machine": "outpost-far"},
		}},
		{name: "beds", docs: map[string]any{
			"bed-work": map[string]any{"id": "bed-work", "name": "work", "repo_paths": []map[string]string{{"path": "/work/home", "machine": ""}, {"path": "/home/user/repo", "machine": "outpost-far"}}},
		}},
	} {
		collection := garden.DispatchesSchema()
		if schema.name == "beds" {
			collection = garden.BedsSchema()
		}
		if _, err := old.DefineDocumentCollection(collection, now); err != nil {
			t.Fatal(err)
		}
		declared, _, err := old.DocumentCollection(collection.Namespace, collection.Collection)
		if err != nil {
			t.Fatal(err)
		}
		for id, body := range schema.docs {
			encoded, _ := json.Marshal(body)
			if _, err := old.PutDocument(*declared, id, encoded, now, nil); err != nil {
				t.Fatal(err)
			}
		}
	}

	hashedID := markdownTileID1795743017234424("/work/home/notes.md")
	remoteID := markdownTileID1795743017234424("outpost-far\x00/home/user/repo/plan.md")
	tree := layouttree.Node{Type: "split", SplitID: "split-1", Direction: layouttree.DirectionVertical, Ratio: 0.5, Children: []layouttree.Node{
		{Type: "tile", TileID: hashedID, TileKind: string(layouttree.TileKindMarkdown), TileParams: "/work/home/notes.md", TileSessionID: "home-open"},
		{Type: "split", SplitID: "split-2", Direction: layouttree.DirectionHorizontal, Ratio: 0.5, Children: []layouttree.Node{
			{Type: "tile", TileID: "tile-docked-by-app", TileKind: string(layouttree.TileKindMarkdown), TileParams: "/work/home/README.md"},
			{Type: "tile", TileID: remoteID, TileKind: string(layouttree.TileKindMarkdown), TileParams: `{"host":"outpost-far","path":"/home/user/repo/plan.md"}`},
		}},
	}}
	encoded, err := layouttree.EncodeLayout(tree)
	if err != nil {
		t.Fatal(err)
	}
	history, _ := json.Marshal([]string{"tile-docked-by-app", hashedID, remoteID})
	exec(`UPDATE desktops SET tree_json = ?, active_pane_id = ?, focus_history = ? WHERE id = ?`, string(encoded), hashedID, string(history), desktop.ID)

	before, beforeActive, beforeHistory := shownMarkdownTiles(t, old, desktop.ID)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	self := upgraded.MachineID()
	requireEveryMachineNamed(t, upgraded)

	if got := upgraded.Get("home-open"); got == nil || got.Host != nil {
		t.Fatalf("home session = %+v, want it shown on this machine", got)
	}
	if got := upgraded.Get("far-open"); got == nil || got.Host == nil || *got.Host != "outpost-far" {
		t.Fatalf("outpost session = %+v, want it shown on outpost-far", got)
	}
	if outposts, err := upgraded.ListOutposts(); err != nil || len(outposts) != 1 || outposts[0].ID != "outpost-far" {
		t.Fatalf("outposts = %+v, want outpost-far alone", outposts)
	}
	for _, key := range []string{"new_session_yolo_" + self, "code_review_setup_offer_" + self + "_/work/home", "new_session_destination_" + self + "_profile-1", "new_session_yolo_outpost-far"} {
		if upgraded.GetSetting(key) == "" {
			t.Errorf("setting %s lost its value", key)
		}
	}

	after, afterActive, afterHistory := shownMarkdownTiles(t, upgraded, desktop.ID)
	if len(after) != len(before) {
		t.Fatalf("tiles = %+v, want the %d tiles from before", after, len(before))
	}
	fileOf := func(tiles map[string]shownTile, id string) string { return tiles[id].path }
	shown := func(tiles map[string]shownTile) []string {
		var files []string
		for _, tile := range tiles {
			files = append(files, tile.path)
		}
		slices.Sort(files)
		return files
	}
	if !slices.Equal(shown(after), shown(before)) {
		t.Fatalf("tiles show %v, want %v", shown(after), shown(before))
	}
	for id, tile := range after {
		want := self
		if strings.HasPrefix(tile.path, "/home/user/") {
			want = "outpost-far"
		}
		if tile.machine != want {
			t.Errorf("tile %s shows %s on machine %q, want %q", id, tile.path, tile.machine, want)
		}
	}
	if _, kept := after["tile-docked-by-app"]; !kept {
		t.Errorf("the tile id the app chose changed: %+v", after)
	}
	if _, kept := after[remoteID]; !kept {
		t.Errorf("the outpost tile changed its id: %+v", after)
	}
	if fileOf(after, afterActive) != fileOf(before, beforeActive) {
		t.Errorf("active tile shows %q, want %q", fileOf(after, afterActive), fileOf(before, beforeActive))
	}
	var historyBefore, historyAfter []string
	for _, id := range beforeHistory {
		historyBefore = append(historyBefore, fileOf(before, id))
	}
	for _, id := range afterHistory {
		historyAfter = append(historyAfter, fileOf(after, id))
	}
	if !slices.Equal(historyAfter, historyBefore) {
		t.Errorf("focus history shows %v, want %v", historyAfter, historyBefore)
	}
}

func TestMachineIDsMigrationLeavesAnEnrolledOutpostReadingItsCopiesAsThisMachine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outpost.db")
	old, err := newStoreAtVersion(path, machineIDsMigration-1)
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := mustCreateProfile(t, old, "Work")
	if err := transactWritingCopies(context.Background(), old.db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO sessions(id,user_label,directory,created_at,profile_id,host,copied_from,closed_at) VALUES ('copied','Copied','/home/user/repo','',?,'outpost-far','home-1','')`, profile.ID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO settings (key, value, copied_from) VALUES ('new_session_yolo_local', 'true', 'home-1'), ('theme', 'dark', 'home-1')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	dormant := upgraded.MachineID()
	upgraded.SetMachineID("outpost-far")
	requireEveryMachineNamed(t, upgraded)

	if got := upgraded.Get("copied"); got == nil || got.Host != nil {
		t.Fatalf("copied session = %+v, want it shown on this machine", got)
	}
	for table, column := range machineReferences {
		var held int
		if err := upgraded.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+column+` = ?`, dormant).Scan(&held); err != nil {
			t.Fatal(err)
		}
		if held != 0 {
			t.Errorf("%s has %d rows on the outpost's dormant home machine", table, held)
		}
	}
	if copied := upgraded.CopiedSettings("home-1"); copied["theme"] != "dark" || len(copied) != 1 {
		t.Fatalf("copied settings = %v, want the home's theme alone until the home sends its renamed keys", copied)
	}
}
