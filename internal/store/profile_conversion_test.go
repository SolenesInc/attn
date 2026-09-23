package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profilemigration"
	"github.com/victorarias/attn/internal/profiles"
)

type legacyFixture struct {
	t    *testing.T
	path string
	db   *sql.DB
	rank int
}

type legacyPaneRow struct {
	PaneID    string `json:"pane_id"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
}

func newLegacyFixture(t *testing.T) *legacyFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "attn.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	f := &legacyFixture{t: t, path: path, db: db}
	f.exec(`
		DELETE FROM desktop_panes; DELETE FROM desktops; DELETE FROM profiles; DELETE FROM profile_migration;
		UPDATE sessions SET profile_id = '';
		DELETE FROM schema_migrations WHERE version >= ?;`, ProfileConversionSchemaVersion)
	t.Cleanup(func() { f.db.Close() })
	return f
}

func (f *legacyFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		f.t.Fatalf("fixture %q: %v", strings.Fields(query)[0], err)
	}
}

func (f *legacyFixture) session(id string, closed bool) {
	f.t.Helper()
	closedAt := ""
	if closed {
		closedAt = "2026-09-01T00:00:00Z"
	}
	f.exec(`INSERT INTO sessions (id, label, directory, state_since, state_updated_at, last_seen, closed_at)
		VALUES (?, ?, '/fixture', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z', ?)`, id, "label "+id, closedAt)
}

func (f *legacyFixture) workspace(id string, tree layouttree.Node, active string, panes ...legacyPaneRow) {
	f.t.Helper()
	f.rank++
	f.exec(`INSERT INTO workspaces (id, title, directory, created_at, rank) VALUES (?, ?, ?, '2026-09-01T00:00:00Z', ?)`,
		id, "title "+id, "/fixture/"+id, fmt.Sprintf("r%03d", f.rank))
	encoded, err := layouttree.EncodeLayout(tree)
	if err != nil {
		f.t.Fatal(err)
	}
	f.rawLayout(id, active, encoded)
	for _, pane := range panes {
		status := pane.Status
		if status == "" {
			status = "ready"
		}
		updated := pane.UpdatedAt
		if updated == "" {
			updated = "2026-09-01T00:00:00Z"
		}
		f.exec(`INSERT INTO workspace_layout_panes (workspace_id, pane_id, runtime_id, session_id, kind, title, status, error, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'agent', 'Agent', ?, '', ?, ?)`, id, pane.PaneID, pane.SessionID, pane.SessionID, status, updated, updated)
	}
}

func (f *legacyFixture) rawLayout(workspaceID, active, layoutJSON string) {
	f.t.Helper()
	f.exec(`INSERT INTO workspace_layouts (workspace_id, active_pane_id, layout_json, updated_at) VALUES (?, ?, ?, '2026-09-01T00:00:00Z')`,
		workspaceID, active, layoutJSON)
}

func (f *legacyFixture) agentWorkspace(id, sessionID string) {
	f.t.Helper()
	f.session(sessionID, false)
	f.workspace(id, layouttree.DefaultLayout("pane-"+sessionID), "pane-"+sessionID, legacyPaneRow{PaneID: "pane-" + sessionID, SessionID: sessionID})
}

func (f *legacyFixture) convert() (*Store, SchemaUpgrade, error) {
	f.t.Helper()
	if err := f.db.Close(); err != nil {
		f.t.Fatal(err)
	}
	s, upgrade, err := Open(f.path)
	if err == nil {
		f.t.Cleanup(func() { s.Close() })
	}
	return s, upgrade, err
}

func (f *legacyFixture) mustConvert() (*Store, ProfileMigrationView, []profiles.Desktop) {
	f.t.Helper()
	s, _, err := f.convert()
	if err != nil {
		f.t.Fatalf("conversion: %v", err)
	}
	view, err := s.ProfileMigration()
	if err != nil {
		f.t.Fatalf("ProfileMigration: %v", err)
	}
	_, desktops, err := s.ProfileArrangement(view.Manifest.ProfileID)
	if err != nil {
		f.t.Fatalf("ProfileArrangement: %v", err)
	}
	return s, view, desktops
}

func split(direction layouttree.Direction, ratio float64, locked bool, first, second layouttree.Node) layouttree.Node {
	return layouttree.Node{Type: "split", SplitID: "split", Direction: direction, Ratio: ratio, RatioLocked: locked, Children: []layouttree.Node{first, second}}
}

func tile(id string) layouttree.Node {
	return layouttree.Node{Type: "tile", TileID: id, TileKind: "markdown", TileParams: "/fixture/doc.md"}
}

func pane(id string) layouttree.Node {
	return layouttree.DefaultLayout(id)
}

func slotsOf(desktops []profiles.Desktop) []int {
	var slots []int
	for _, desktop := range desktops {
		slots = append(slots, desktop.ShortcutSlot)
	}
	return slots
}

func placements(desktops []profiles.Desktop) map[string]string {
	placed := make(map[string]string)
	for _, desktop := range desktops {
		for _, pane := range desktop.Panes {
			placed[pane.SessionID] = desktop.ID
		}
	}
	return placed
}

type legacyFixtureFile struct {
	Workspaces []struct {
		ID, Title, Directory, Rank string
		CreatedAt                  string `json:"created_at"`
	} `json:"workspaces"`
	Layouts []struct {
		WorkspaceID  string `json:"workspace_id"`
		ActivePaneID string `json:"active_pane_id"`
		LayoutJSON   string `json:"layout_json"`
	} `json:"layouts"`
	Panes []struct {
		WorkspaceID string `json:"workspace_id"`
		legacyPaneRow
		RuntimeID string `json:"runtime_id"`
		CreatedAt string `json:"created_at"`
	} `json:"panes"`
	Sessions []struct {
		ID          string `json:"id"`
		Label       string `json:"label"`
		WorkspaceID string `json:"workspace_id"`
		ClosedAt    string `json:"closed_at"`
	} `json:"sessions"`
}

func (f *legacyFixture) load(name string) legacyFixtureFile {
	f.t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		f.t.Fatal(err)
	}
	var file legacyFixtureFile
	if err := json.Unmarshal(raw, &file); err != nil {
		f.t.Fatal(err)
	}
	for _, ws := range file.Workspaces {
		f.exec(`INSERT INTO workspaces (id, title, directory, created_at, rank) VALUES (?, ?, ?, ?, ?)`, ws.ID, ws.Title, ws.Directory, ws.CreatedAt, ws.Rank)
	}
	for _, layout := range file.Layouts {
		f.rawLayout(layout.WorkspaceID, layout.ActivePaneID, layout.LayoutJSON)
	}
	for _, p := range file.Panes {
		f.exec(`INSERT INTO workspace_layout_panes (workspace_id, pane_id, runtime_id, session_id, kind, title, status, error, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'agent', 'Agent', ?, '', ?, ?)`, p.WorkspaceID, p.PaneID, p.RuntimeID, p.SessionID, p.Status, p.CreatedAt, p.UpdatedAt)
	}
	for _, session := range file.Sessions {
		f.exec(`INSERT INTO sessions (id, label, directory, state_since, state_updated_at, last_seen, closed_at, workspace_id)
			VALUES (?, ?, '/fixture', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z', ?, ?)`, session.ID, session.Label, session.ClosedAt, session.WorkspaceID)
	}
	return file
}

func TestConversionOfARealInstallKeepsEveryLiveAgentWhereItWas(t *testing.T) {
	f := newLegacyFixture(t)
	file := f.load("legacy_workspaces.json")
	legacyTrees := make(map[string]layouttree.Node)
	for _, layout := range file.Layouts {
		tree, err := layouttree.DecodeLayout(layout.LayoutJSON)
		if err != nil {
			t.Fatal(err)
		}
		legacyTrees[layout.WorkspaceID] = tree
	}
	s, view, desktops := f.mustConvert()

	if view.State.Phase != profilemigration.PhasePlacementRequired || view.State.SchemaVersion != ProfileConversionSchemaVersion {
		t.Fatalf("migration state = %+v, want placement_required from conversion %d", view.State, ProfileConversionSchemaVersion)
	}
	if len(view.Manifest.Groups) != 5 || len(view.Manifest.DroppedWorkspaces) != 15 {
		t.Fatalf("groups=%d dropped=%d, want the 5 workspaces with live agents and 15 document-only ones", len(view.Manifest.Groups), len(view.Manifest.DroppedWorkspaces))
	}
	for _, dropped := range view.Manifest.DroppedWorkspaces {
		if dropped.Reason != profilemigration.DropReasonDocumentOnly {
			t.Fatalf("dropped %+v, want only document-only cleanup", dropped)
		}
	}
	if got := slotsOf(desktops); !reflect.DeepEqual(got, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("slots = %v, want the five groups in slots 1-5 by rank", got)
	}
	for i, group := range view.Manifest.Groups {
		desktop := desktops[i]
		if group.DesktopID != desktop.ID {
			t.Fatalf("group %d is recorded on %s but desktop %d is %s", i, group.DesktopID, i, desktop.ID)
		}
		legacyLeaves := append(layouttree.PaneIDs(legacyTrees[group.ID]), layouttree.TileIDs(legacyTrees[group.ID])...)
		sort.Strings(legacyLeaves)
		got := append([]string(nil), group.LeafIDs...)
		sort.Strings(got)
		if !reflect.DeepEqual(got, legacyLeaves) {
			t.Fatalf("group %s leaves = %v, want the legacy leaves %v", group.ID, got, legacyLeaves)
		}
	}
	open := 0
	for _, session := range file.Sessions {
		if session.ClosedAt == "" {
			open++
		}
	}
	if placed := placements(desktops); len(placed) != open {
		t.Fatalf("%d agents placed, want every one of the %d open sessions", len(placed), open)
	}
	profile, err := s.GetProfile(view.Manifest.ProfileID)
	if err != nil || profile.Name != DefaultProfileName || profile.CurrentDesktopID != desktops[0].ID {
		t.Fatalf("profile = %+v, %v; want Default on the highest-ranked desktop", profile, err)
	}
	var unstamped int
	if err := s.db.QueryRow(`SELECT count(*) FROM sessions WHERE profile_id != ?`, profile.ID).Scan(&unstamped); err != nil || unstamped != 0 {
		t.Fatalf("%d sessions (closed ones included) lack the Default profile (%v)", unstamped, err)
	}
}

func TestConversionOfAFreshInstallCompletesWithOneEmptyDesktop(t *testing.T) {
	s, _ := openProfileStore(t)
	view, err := s.ProfileMigration()
	if err != nil || view.State.Phase != profilemigration.PhaseComplete || len(view.Manifest.Groups) != 0 {
		t.Fatalf("fresh migration = %+v, %v; want complete with nothing imported", view.State, err)
	}
	profile, desktops, err := s.ProfileArrangement(view.Manifest.ProfileID)
	if err != nil || profile.Name != DefaultProfileName || len(desktops) != 1 || desktops[0].ShortcutSlot != 1 || !layouttree.LayoutEmpty(desktops[0].Tree) {
		t.Fatalf("fresh arrangement = %+v %+v, %v; want Default with one empty desktop in slot 1", profile, desktops, err)
	}
}

func TestConversionFillsTheNineSlotsByRankAndKeepsTheRestAsExtras(t *testing.T) {
	for _, count := range []int{3, 9, 11} {
		t.Run(fmt.Sprintf("%d workspaces", count), func(t *testing.T) {
			f := newLegacyFixture(t)
			for i := 1; i <= count; i++ {
				f.agentWorkspace(fmt.Sprintf("ws-%02d", i), fmt.Sprintf("agent-%02d", i))
			}
			_, view, desktops := f.mustConvert()
			var want []int
			for i := 1; i <= count; i++ {
				want = append(want, min(i, 10)%10)
			}
			if got := slotsOf(desktops); !reflect.DeepEqual(got, want) {
				t.Fatalf("slots = %v, want %v", got, want)
			}
			for i, group := range view.Manifest.Groups {
				if group.ID != fmt.Sprintf("ws-%02d", i+1) || group.ShortcutSlot != want[i] {
					t.Fatalf("group %d = %+v, want ws-%02d in slot %d", i, group, i+1, want[i])
				}
			}
			plan := view.Plan
			if len(plan.Desktops) != 9+max(0, count-9) {
				t.Fatalf("draft has %d desktops, want nine slots plus %d extras", len(plan.Desktops), max(0, count-9))
			}
		})
	}
}

func TestConversionPreservesMixedTreesRatiosAndPendingAgents(t *testing.T) {
	f := newLegacyFixture(t)
	f.session("live", false)
	f.session("pending", false)
	f.session("closed", true)
	tree := split(layouttree.DirectionVertical, 0.7, true,
		split(layouttree.DirectionHorizontal, 0.5, false, pane("pane-live"), pane("pane-closed")),
		split(layouttree.DirectionHorizontal, 0.4, true, tile("tile-doc"), split(layouttree.DirectionVertical, 0.5, false, pane("pane-pending"), pane("pane-ghost"))))
	f.workspace("mixed", tree, "pane-closed",
		legacyPaneRow{PaneID: "pane-live", SessionID: "live"},
		legacyPaneRow{PaneID: "pane-closed", SessionID: "closed"},
		legacyPaneRow{PaneID: "pane-pending", SessionID: "pending", Status: "spawning"},
		legacyPaneRow{PaneID: "pane-missing", SessionID: "never-existed"})
	_, view, desktops := f.mustConvert()

	desktop := desktops[0]
	want := split(layouttree.DirectionVertical, 0.7, true,
		pane("pane-live"),
		split(layouttree.DirectionHorizontal, 0.4, true, tile("tile-doc"), pane("pane-pending")))
	if !sameShape(desktop.Tree, want) {
		t.Fatalf("converted tree = %+v, want the legacy tree without its dead panes and with its locked ratios", desktop.Tree)
	}
	if desktop.ActivePaneID != "pane-live" {
		t.Fatalf("active pane = %q, want the first live pane after the closed one left", desktop.ActivePaneID)
	}
	statuses := map[string]profiles.PaneStatus{}
	for _, p := range desktop.Panes {
		statuses[p.SessionID] = p.Status
	}
	if !reflect.DeepEqual(statuses, map[string]profiles.PaneStatus{"live": profiles.PaneStatusReady, "pending": profiles.PaneStatusSpawning}) {
		t.Fatalf("panes = %+v, want the live and the pending agent", desktop.Panes)
	}
	reasons := map[string]string{}
	for _, dropped := range view.Manifest.DroppedPanes {
		reasons[dropped.PaneID] = dropped.Reason
	}
	if !reflect.DeepEqual(reasons, map[string]string{"pane-closed": profilemigration.DropReasonSessionClosed, "pane-ghost": profilemigration.DropReasonNoPaneRow}) {
		t.Fatalf("dropped panes = %+v", view.Manifest.DroppedPanes)
	}
}

func sameShape(got, want layouttree.Node) bool {
	if got.Type != want.Type || got.PaneID != want.PaneID || got.TileID != want.TileID || len(got.Children) != len(want.Children) {
		return false
	}
	if got.Type == "split" && (got.Direction != want.Direction || (want.RatioLocked && (got.Ratio != want.Ratio || !got.RatioLocked))) {
		return false
	}
	for i := range got.Children {
		if !sameShape(got.Children[i], want.Children[i]) {
			return false
		}
	}
	return true
}

func TestConversionKeepsTheNewestOfDuplicatePlacementsAndRenamesCollidingPanes(t *testing.T) {
	f := newLegacyFixture(t)
	f.session("twice", false)
	f.session("first-owner", false)
	f.session("second-owner", false)
	f.workspace("older", split(layouttree.DirectionVertical, 0.5, false, pane("pane-a"), pane("pane-shared")), "pane-a",
		legacyPaneRow{PaneID: "pane-a", SessionID: "twice", UpdatedAt: "2026-09-01T00:00:00Z"},
		legacyPaneRow{PaneID: "pane-shared", SessionID: "first-owner"})
	f.workspace("newer", split(layouttree.DirectionVertical, 0.5, false, pane("pane-b"), pane("pane-shared")), "pane-shared",
		legacyPaneRow{PaneID: "pane-b", SessionID: "twice", UpdatedAt: "2026-09-02T00:00:00Z"},
		legacyPaneRow{PaneID: "pane-shared", SessionID: "second-owner"})
	_, view, desktops := f.mustConvert()

	placed := placements(desktops)
	if placed["twice"] != desktops[1].ID {
		t.Fatalf("the agent placed twice landed on %s, want the newer placement on %s", placed["twice"], desktops[1].ID)
	}
	if !reflect.DeepEqual(view.Manifest.DroppedPlacements, []profilemigration.DroppedPlacement{{
		SessionID: "twice", WorkspaceID: "older", PaneID: "pane-a", KeptWorkspaceID: "newer", KeptPaneID: "pane-b",
	}}) {
		t.Fatalf("dropped placements = %+v", view.Manifest.DroppedPlacements)
	}
	if len(view.Manifest.RenamedPanes) != 1 || view.Manifest.RenamedPanes[0].WorkspaceID != "newer" || view.Manifest.RenamedPanes[0].From != "pane-shared" {
		t.Fatalf("renamed panes = %+v, want the second pane-shared renamed", view.Manifest.RenamedPanes)
	}
	renamed := view.Manifest.RenamedPanes[0].To
	if desktops[1].ActivePaneID != renamed || !layouttree.HasPane(desktops[1].Tree, renamed) {
		t.Fatalf("newer desktop = %+v, want its active pane renamed to %s", desktops[1], renamed)
	}
	if placed["first-owner"] != desktops[0].ID || placed["second-owner"] != desktops[1].ID {
		t.Fatalf("placements = %+v, want each owner of pane-shared kept on its own desktop", placed)
	}
}

func TestConversionStampsLaunchesRunsAndDefinitionsWithDefault(t *testing.T) {
	f := newLegacyFixture(t)
	f.exec(`INSERT INTO delegation_operations (request_id, operation_id, request_json, state, progress, session_id, created_at, updated_at)
		VALUES ('req', 'op', '{}', 'accepted', '', 'child', 'now', 'now')`)
	f.exec(`INSERT INTO automation_definitions (id, name, enabled, revision, spec_json, created_at, updated_at) VALUES ('auto', 'nightly', 1, 1, '{}', 'now', 'now')`)
	s, view, _ := f.mustConvert()
	for _, table := range []string{"delegation_operations", "automation_definitions"} {
		var profileID string
		if err := s.db.QueryRow(`SELECT profile_id FROM ` + table).Scan(&profileID); err != nil || profileID != view.Manifest.ProfileID {
			t.Fatalf("%s profile_id = %q (%v), want Default %s", table, profileID, err, view.Manifest.ProfileID)
		}
	}
}

func TestAFailedConversionLeavesTheDatabaseAsItWas(t *testing.T) {
	f := newLegacyFixture(t)
	f.agentWorkspace("good", "agent")
	f.exec(`INSERT INTO workspaces (id, title, directory, created_at, rank) VALUES ('broken', 'Broken', '/fixture', 'now', 'z')`)
	f.rawLayout("broken", "", `{"type":"split","split_id":"s","direction":"diagonal","ratio":0.5,"children":[{"type":"pane","pane_id":"p1"},{"type":"pane","pane_id":"p2"}]}`)
	f.exec(`DELETE FROM schema_migrations WHERE version >= 150`)

	_, upgrade, err := f.convert()
	if err == nil || !strings.Contains(err.Error(), `legacy workspace broken ("Broken")`) {
		t.Fatalf("conversion error = %v, want the broken workspace named", err)
	}
	if upgrade.From != 149 || upgrade.To != LatestSchemaVersion() || upgrade.BackupPath == "" {
		t.Fatalf("upgrade report = %+v, want 149 -> latest with a backup", upgrade)
	}
	if _, err := os.Stat(upgrade.BackupPath); err != nil {
		t.Fatalf("backup %s: %v", upgrade.BackupPath, err)
	}
	db, err := openSQLite(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	version, err := getCurrentVersion(db)
	if err != nil || version != 149 {
		t.Fatalf("schema version after the failure = %d (%v), want 149 unchanged", version, err)
	}
	var profileRows int
	if err := db.QueryRow(`SELECT count(*) FROM profiles`).Scan(&profileRows); err != nil || profileRows != 0 {
		t.Fatalf("%d profiles after the failure (%v), want none", profileRows, err)
	}

	if _, err := db.Exec(`DELETE FROM workspace_layouts WHERE workspace_id = 'broken'`); err != nil {
		t.Fatal(err)
	}
	s, upgrade, err := Open(f.path)
	if err != nil {
		t.Fatalf("retry after repairing the input: %v", err)
	}
	defer s.Close()
	if upgrade.From != 149 {
		t.Fatalf("retry upgraded from %d, want 149", upgrade.From)
	}
}

func TestReopeningAConvertedDatabaseChangesNothing(t *testing.T) {
	f := newLegacyFixture(t)
	f.agentWorkspace("one", "agent-1")
	f.agentWorkspace("two", "agent-2")
	s, view, desktops := f.mustConvert()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, upgrade, err := Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	again, err := reopened.ProfileMigration()
	if err != nil {
		t.Fatal(err)
	}
	_, desktopsAgain, err := reopened.ProfileArrangement(view.Manifest.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if upgrade.From != upgrade.To || !reflect.DeepEqual(again.State, view.State) || !reflect.DeepEqual(desktopsAgain, desktops) {
		t.Fatalf("reopen changed the conversion: upgrade %+v, state %+v -> %+v", upgrade, view.State, again.State)
	}
	if err := reopened.db.QueryRow(`SELECT 1`).Err(); err != nil {
		t.Fatal(err)
	}
	var migrationRuns int
	if err := reopened.db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version = ?`, ProfileConversionSchemaVersion).Scan(&migrationRuns); err != nil || migrationRuns != 1 {
		t.Fatalf("the conversion migration recorded %d times (%v)", migrationRuns, err)
	}
}

func TestOpenCurrentRefusesAnOlderSchemaWithoutUpgradingIt(t *testing.T) {
	f := newLegacyFixture(t)
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := OpenCurrent(f.path)
	var behind *SchemaBehindError
	if !errors.As(err, &behind) || behind.Current != ProfileConversionSchemaVersion-1 || behind.Required != LatestSchemaVersion() {
		t.Fatalf("OpenCurrent = %v, want a refusal naming v%d and v%d", err, ProfileConversionSchemaVersion-1, LatestSchemaVersion())
	}
	if !strings.Contains(err.Error(), "attn daemon ensure") {
		t.Fatalf("refusal %q does not say how to upgrade", err)
	}
	db, err := openSQLite(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if version, err := getCurrentVersion(db); err != nil || version != ProfileConversionSchemaVersion-1 {
		t.Fatalf("schema version = %d (%v), want v%d untouched", version, err, ProfileConversionSchemaVersion-1)
	}
	if entries, _ := os.ReadDir(BackupDirForDatabase(f.path)); len(entries) != 0 {
		t.Fatalf("OpenCurrent wrote backups %v", entries)
	}
}

func TestAFailedUpgradeOfAnUnversionedLegacyDatabaseRecordsNoVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(baseSchema + `
		DROP TABLE endpoints;
		ALTER TABLE prs ADD COLUMN head_sha TEXT;
		CREATE TRIGGER refuse_version_20 BEFORE INSERT ON schema_migrations
		WHEN NEW.version = 20 BEGIN SELECT RAISE(ABORT, 'injected upgrade failure'); END;`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, upgrade, err := Open(path)
	if err == nil || !strings.Contains(err.Error(), "injected upgrade failure") {
		t.Fatalf("Open = %v, want the injected failure", err)
	}
	if upgrade.From != 0 {
		t.Fatalf("upgrade reported from v%d, want the recorded v0", upgrade.From)
	}
	db, err = openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if version, err := getCurrentVersion(db); err != nil || version != 0 {
		t.Fatalf("schema version after the failure = %d (%v), want 0 with no legacy versions recorded", version, err)
	}
	for _, path := range []string{path, upgrade.BackupPath} {
		inspected, err := openSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		var endpoints int
		err = inspected.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'endpoints'`).Scan(&endpoints)
		inspected.Close()
		if err != nil || endpoints != 0 {
			t.Fatalf("%s gained the base schema's endpoints table (%d, %v); the failed upgrade and its snapshot must hold the original schema", path, endpoints, err)
		}
	}
}
