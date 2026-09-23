package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
)

type desktopTilesWorld struct {
	*profilesTestDaemon
	client    *wsClient
	profileID string
	desktop   protocol.Desktop
}

func newDesktopTilesWorld(t *testing.T) *desktopTilesWorld {
	t.Helper()
	w := newProfilesTestDaemon(t)
	client, _ := w.connect("")
	created := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "attn"})
	w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": created.Profile.ID})
	drainClientPayloads(t, client)
	return &desktopTilesWorld{profilesTestDaemon: w, client: client, profileID: created.Profile.ID, desktop: created.Desktops[0]}
}

func (w *desktopTilesWorld) apply(command map[string]any) protocol.ProfileActionResultMessage {
	w.t.Helper()
	command["desktop_id"] = w.desktop.ID
	command["expected_revision"] = w.desktop.Revision
	result := w.mustSend(w.client, command)
	w.desktop = result.Desktops[0]
	return result
}

func (w *desktopTilesWorld) tree() layouttree.Node {
	w.t.Helper()
	tree, err := layouttree.DecodeLayout(w.desktop.TreeJson)
	if err != nil {
		w.t.Fatalf("decode desktop tree %q: %v", w.desktop.TreeJson, err)
	}
	return tree
}

func (w *desktopTilesWorld) tile(tileID string) layouttree.TileLeaf {
	w.t.Helper()
	tile, found := tileLeafByID(w.tree(), tileID)
	if !found {
		w.t.Fatalf("desktop tree %s has no tile %s", w.desktop.TreeJson, tileID)
	}
	return tile
}

func tileContents(t *testing.T, client *wsClient) []protocol.DesktopTileContentMessage {
	t.Helper()
	var contents []protocol.DesktopTileContentMessage
	for _, payload := range drainClientPayloads(t, client) {
		if eventName(t, payload) != protocol.EventDesktopTileContent {
			continue
		}
		var content protocol.DesktopTileContentMessage
		decodeInto(t, payload, &content)
		contents = append(contents, content)
	}
	return contents
}

func TestATileDockedOnAnEmptyDesktopBecomesItsWholeTree(t *testing.T) {
	w := newDesktopTilesWorld(t)
	second, _ := w.connect(w.profileID)

	w.apply(map[string]any{
		"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-notebook", "tile_kind": "notebook", "tile_params": "{}", "edge": "right",
	})

	if tree := w.tree(); tree.Type != "tile" || tree.TileID != "tile-notebook" || tree.TileParams != "{}" {
		t.Fatalf("empty desktop tree after docking is %s, want the notebook tile alone", w.desktop.TreeJson)
	}
	seen := arrangementChanges(t, second)
	if len(seen) != 1 || len(seen[0].Desktops) != 1 || seen[0].Desktops[0].TreeJson != w.desktop.TreeJson {
		t.Fatalf("the other client saw %+v, want the docked tile", seen)
	}
}

func TestATileDocksBesideTheActivePaneAndReDockingKeepsItsParams(t *testing.T) {
	w := newDesktopTilesWorld(t)
	w.agent("agent-a", w.profileID)
	placed := w.apply(map[string]any{"cmd": protocol.CmdDesktopPlaceSession, "session_id": "agent-a"})
	paneID := protocol.Deref(placed.PaneID)
	notes := filepath.Join(t.TempDir(), "notes.md")

	w.apply(map[string]any{
		"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-md", "tile_kind": "markdown", "tile_params": notes,
		"tile_session_id": "agent-a", "edge": "right", "tile_share": 0.4,
	})
	tree := w.tree()
	if tree.Type != "split" || tree.Children[0].PaneID != paneID || tree.Children[1].TileID != "tile-md" || tree.Ratio != 0.6 {
		t.Fatalf("docking beside the active pane produced %s, want pane %s then the tile at 40%%", w.desktop.TreeJson, paneID)
	}
	if w.desktop.ActivePaneID != paneID {
		t.Fatalf("docking a tile moved the active pane to %q", w.desktop.ActivePaneID)
	}

	w.apply(map[string]any{"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-md", "tile_kind": "markdown", "edge": "left"})
	tree = w.tree()
	if tree.Children[0].TileID != "tile-md" || tree.Children[1].PaneID != paneID {
		t.Fatalf("re-docking on the left produced %s", w.desktop.TreeJson)
	}
	if tile := w.tile("tile-md"); tile.TileParams != notes || tile.TileSessionID != "agent-a" {
		t.Fatalf("re-docking lost the tile's params or session: %+v", tile)
	}
}

func TestDockingATileRefusesAStaleRevisionAndAPaneID(t *testing.T) {
	w := newDesktopTilesWorld(t)
	w.agent("agent-a", w.profileID)
	placed := w.apply(map[string]any{"cmd": protocol.CmdDesktopPlaceSession, "session_id": "agent-a"})

	stale := w.send(w.client, map[string]any{
		"cmd": protocol.CmdDesktopDockTile, "desktop_id": w.desktop.ID, "expected_revision": w.desktop.Revision - 1,
		"tile_id": "tile-notebook", "tile_kind": "notebook", "edge": "right",
	})
	wantErrorCode(t, stale, protocol.ProfileErrorCodeStaleRevision)

	clash := w.send(w.client, map[string]any{
		"cmd": protocol.CmdDesktopDockTile, "desktop_id": w.desktop.ID, "expected_revision": w.desktop.Revision,
		"tile_id": protocol.Deref(placed.PaneID), "tile_kind": "notebook", "edge": "right",
	})
	wantErrorCode(t, clash, protocol.ProfileErrorCodeInvalid)

	unknownSession := w.send(w.client, map[string]any{
		"cmd": protocol.CmdDesktopDockTile, "desktop_id": w.desktop.ID, "expected_revision": w.desktop.Revision,
		"tile_id": "tile-md", "tile_kind": "markdown", "tile_params": "/notes.md", "tile_session_id": "agent-that-never-was", "edge": "right",
	})
	wantErrorCode(t, unknownSession, protocol.ProfileErrorCodeNotFound)
}

func TestUpdatingATileValidatesItsParamsByKind(t *testing.T) {
	w := newDesktopTilesWorld(t)
	w.agent("agent-a", w.profileID)
	notes := filepath.Join(t.TempDir(), "notes.md")
	w.apply(map[string]any{"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-web", "tile_kind": "browser", "tile_params": "https://example.com", "edge": "right"})
	w.apply(map[string]any{"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-md", "tile_kind": "markdown", "tile_params": notes, "edge": "right"})

	w.apply(map[string]any{"cmd": protocol.CmdDesktopUpdateTile, "tile_id": "tile-web", "tile_params": "https://attn.example/docs"})
	if got := w.tile("tile-web").TileParams; got != "https://attn.example/docs" {
		t.Fatalf("browser tile params are %q after the update", got)
	}

	badURL := w.send(w.client, map[string]any{
		"cmd": protocol.CmdDesktopUpdateTile, "desktop_id": w.desktop.ID, "expected_revision": w.desktop.Revision,
		"tile_id": "tile-web", "tile_params": "javascript:alert(1)",
	})
	wantErrorCode(t, badURL, protocol.ProfileErrorCodeInvalid)

	markdownPath := w.send(w.client, map[string]any{
		"cmd": protocol.CmdDesktopUpdateTile, "desktop_id": w.desktop.ID, "expected_revision": w.desktop.Revision,
		"tile_id": "tile-md", "tile_params": "/elsewhere.md",
	})
	wantErrorCode(t, markdownPath, protocol.ProfileErrorCodeInvalid)

	unknownSession := w.send(w.client, map[string]any{
		"cmd": protocol.CmdDesktopUpdateTile, "desktop_id": w.desktop.ID, "expected_revision": w.desktop.Revision,
		"tile_id": "tile-md", "tile_session_id": "agent-that-never-was",
	})
	wantErrorCode(t, unknownSession, protocol.ProfileErrorCodeNotFound)

	w.apply(map[string]any{"cmd": protocol.CmdDesktopUpdateTile, "tile_id": "tile-md", "tile_session_id": "agent-a"})
	if tile := w.tile("tile-md"); tile.TileSessionID != "agent-a" || tile.TileParams != notes {
		t.Fatalf("rebinding the markdown tile gave %+v", tile)
	}
}

func TestDockingATileValidatesItsParamsLikeAnUpdate(t *testing.T) {
	w := newDesktopTilesWorld(t)
	before := w.desktop

	for name, command := range map[string]struct {
		kind, params string
		want         protocol.ProfileErrorCode
	}{
		"a script URL":    {"browser", "javascript:alert(1)", protocol.ProfileErrorCodeInvalid},
		"a missing seed":  {"seed", "s-thatneverwas", protocol.ProfileErrorCodeNotFound},
		"an unknown kind": {"spreadsheet", "/tmp/sheet.csv", protocol.ProfileErrorCodeInvalid},
		"an empty kind":   {"", "https://example.com", protocol.ProfileErrorCodeInvalid},
	} {
		refused := w.send(w.client, map[string]any{
			"cmd": protocol.CmdDesktopDockTile, "desktop_id": w.desktop.ID, "expected_revision": w.desktop.Revision,
			"tile_id": "tile-new", "tile_kind": command.kind, "tile_params": command.params, "edge": "right",
		})
		if refused.Success || refused.ErrorCode == nil || *refused.ErrorCode != command.want {
			t.Fatalf("docking %s answered %+v, want %s", name, refused, command.want)
		}
	}
	if after, _ := w.d.store.GetDesktop(before.ID); after.Revision != int64(before.Revision) {
		t.Fatalf("refused docks moved desktop %s from revision %d to %d", before.ID, before.Revision, after.Revision)
	}

	w.apply(map[string]any{"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-web", "tile_kind": "browser", "tile_params": "https://example.com/docs", "edge": "right"})
	if got := w.tile("tile-web").TileParams; got != "https://example.com/docs" {
		t.Fatalf("a valid browser dock stored params %q", got)
	}

	notes := filepath.Join(t.TempDir(), "notes.md")
	w.apply(map[string]any{"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-md", "tile_kind": "markdown", "tile_params": notes, "edge": "right"})
	for _, kind := range []string{"browser", "seed"} {
		retyped := w.send(w.client, map[string]any{
			"cmd": protocol.CmdDesktopDockTile, "desktop_id": w.desktop.ID, "expected_revision": w.desktop.Revision,
			"tile_id": "tile-md", "tile_kind": kind, "edge": "left",
		})
		wantErrorCode(t, retyped, protocol.ProfileErrorCodeInvalid)
	}
	if tile := w.tile("tile-md"); tile.TileKind != "markdown" || tile.TileParams != notes {
		t.Fatalf("refused re-docks changed the markdown tile to %+v", tile)
	}
}

func TestMarkdownTileContentFollowsTheFileUntilTheTileLeaves(t *testing.T) {
	w := newDesktopTilesWorld(t)
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("# first"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.apply(map[string]any{"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-md", "tile_kind": "markdown", "tile_params": notes, "edge": "right"})
	bystander, _ := w.connect(w.profileID)
	drainClientPayloads(t, w.client)

	w.d.handleClientMessage(w.client, []byte(`{"cmd":"desktop_tile_content_get","desktop_id":"`+w.desktop.ID+`","tile_id":"tile-md"}`))
	first := tileContents(t, w.client)
	if len(first) != 1 || first[0].Content != "# first" || first[0].Path != notes || first[0].DesktopID != w.desktop.ID {
		t.Fatalf("desktop_tile_content_get answered %+v", first)
	}
	w.d.pollMarkdownOnce()
	tileContents(t, w.client)

	if err := os.WriteFile(notes, []byte("# second, longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.d.pollMarkdownOnce()
	changed := tileContents(t, w.client)
	if len(changed) != 1 || changed[0].Content != "# second, longer" {
		t.Fatalf("after the file changed the subscriber got %+v", changed)
	}
	if leaked := tileContents(t, bystander); len(leaked) != 0 {
		t.Fatalf("a client that never asked for the tile got %d content messages", len(leaked))
	}

	w.apply(map[string]any{"cmd": protocol.CmdDesktopRemoveLeaf, "leaf_id": "tile-md"})
	if err := os.WriteFile(notes, []byte("# third, after the tile left"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.d.pollMarkdownOnce()
	if late := tileContents(t, w.client); len(late) != 0 {
		t.Fatalf("a removed tile still streamed %+v", late)
	}
	if keys := w.client.tileContentSubscriptionKeys(); len(keys) != 0 {
		t.Fatalf("removing the tile left subscriptions %v", keys)
	}
}

func TestSessionsCarryTheirProfileOnTheWire(t *testing.T) {
	w := newDesktopTilesWorld(t)
	w.agent("agent-a", w.profileID)

	_, initial := w.connect(w.profileID)
	for _, session := range initial.Sessions {
		if session.ID == "agent-a" {
			if session.ProfileID != w.profileID {
				t.Fatalf("agent-a reached the client with profile %q, want %q", session.ProfileID, w.profileID)
			}
			return
		}
	}
	t.Fatalf("initial_state did not list agent-a: %+v", initial.Sessions)
}

func TestDeletingAProfileDropsItsTileSubscriptions(t *testing.T) {
	w := newDesktopTilesWorld(t)
	doomed := w.mustSend(w.client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "doomed"})
	w.mustSend(w.client, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": doomed.Profile.ID})
	w.desktop = doomed.Desktops[0]
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("# notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.apply(map[string]any{"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-md", "tile_kind": "markdown", "tile_params": notes, "edge": "right"})
	w.d.handleClientMessage(w.client, []byte(`{"cmd":"desktop_tile_content_get","desktop_id":"`+w.desktop.ID+`","tile_id":"tile-md"}`))
	if keys := w.client.tileContentSubscriptionKeys(); len(keys) != 1 {
		t.Fatalf("subscribing gave keys %v", keys)
	}
	current, err := w.d.store.GetProfile(doomed.Profile.ID)
	if err != nil {
		t.Fatal(err)
	}

	w.mustSend(w.client, map[string]any{
		"cmd": protocol.CmdProfileDelete, "profile_id": doomed.Profile.ID, "expected_revision": current.Revision, "destination_profile_id": w.profileID,
	})
	w.d.pollMarkdownOnce()

	if keys := w.client.tileContentSubscriptionKeys(); len(keys) != 0 {
		t.Fatalf("deleting the profile left subscriptions %v", keys)
	}
}

func TestSwitchingProfilesDropsTileSubscriptionsOfTheOldProfile(t *testing.T) {
	w := newDesktopTilesWorld(t)
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("# notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.apply(map[string]any{"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-md", "tile_kind": "markdown", "tile_params": notes, "edge": "right"})
	w.d.handleClientMessage(w.client, []byte(`{"cmd":"desktop_tile_content_get","desktop_id":"`+w.desktop.ID+`","tile_id":"tile-md"}`))
	other := w.mustSend(w.client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "elsewhere"})

	w.mustSend(w.client, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": other.Profile.ID})
	drainClientPayloads(t, w.client)
	if err := os.WriteFile(notes, []byte("# edited after the switch"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.d.pollMarkdownOnce()

	if late := tileContents(t, w.client); len(late) != 0 {
		t.Fatalf("a client on another profile still got %+v", late)
	}
	if keys := w.client.tileContentSubscriptionKeys(); len(keys) != 0 {
		t.Fatalf("switching profiles left subscriptions %v", keys)
	}

	w.d.handleClientMessage(w.client, []byte(`{"cmd":"desktop_tile_content_get","desktop_id":"`+w.desktop.ID+`","tile_id":"tile-md"}`))
	if keys := w.client.tileContentSubscriptionKeys(); len(keys) != 0 {
		t.Fatalf("a client subscribed to a desktop outside its profile: %v", keys)
	}
}
