package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/bus"
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

func TestAnEmptyNotebookParamClearsItsRootWhileAMissingOneIsRefused(t *testing.T) {
	w := newDesktopTilesWorld(t)
	w.agent("agent-a", w.profileID)
	w.apply(map[string]any{"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-nb", "tile_kind": "notebook", "tile_params": `{"root":"/elsewhere"}`, "edge": "right"})

	w.apply(map[string]any{"cmd": protocol.CmdDesktopUpdateTile, "tile_id": "tile-nb", "tile_params": ""})
	if got := w.tile("tile-nb").TileParams; got != "" {
		t.Fatalf("notebook tile params are %q after clearing its root", got)
	}

	nothing := w.send(w.client, map[string]any{
		"cmd": protocol.CmdDesktopUpdateTile, "desktop_id": w.desktop.ID, "expected_revision": w.desktop.Revision,
		"tile_id": "tile-nb",
	})
	wantErrorCode(t, nothing, protocol.ProfileErrorCodeInvalid)
}

func TestATileCanOnlyFollowAnAgentOfItsOwnProfile(t *testing.T) {
	w := newDesktopTilesWorld(t)
	elsewhere := w.mustSend(w.client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "elsewhere"})
	w.agent("their-agent", elsewhere.Profile.ID)
	w.agent("my-agent", w.profileID)
	notes := filepath.Join(t.TempDir(), "notes.md")

	docked := w.send(w.client, map[string]any{
		"cmd": protocol.CmdDesktopDockTile, "desktop_id": w.desktop.ID, "expected_revision": w.desktop.Revision,
		"tile_id": "tile-md", "tile_kind": "markdown", "tile_params": notes, "tile_session_id": "their-agent", "edge": "right",
	})
	wantErrorCode(t, docked, protocol.ProfileErrorCodeCrossProfile)

	w.apply(map[string]any{"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-md", "tile_kind": "markdown", "tile_params": notes, "tile_session_id": "my-agent", "edge": "right"})
	rebound := w.send(w.client, map[string]any{
		"cmd": protocol.CmdDesktopUpdateTile, "desktop_id": w.desktop.ID, "expected_revision": w.desktop.Revision,
		"tile_id": "tile-md", "tile_session_id": "their-agent",
	})
	wantErrorCode(t, rebound, protocol.ProfileErrorCodeCrossProfile)
	if tile := w.tile("tile-md"); tile.TileSessionID != "my-agent" {
		t.Fatalf("a refused rebind left the tile following %q", tile.TileSessionID)
	}
}

func TestDockingATileValidatesItsParamsLikeAnUpdate(t *testing.T) {
	w := newDesktopTilesWorld(t)
	before := w.desktop

	for name, command := range map[string]struct {
		kind, params string
		want         protocol.ProfileErrorCode
	}{
		"a script URL":                   {"browser", "javascript:alert(1)", protocol.ProfileErrorCodeInvalid},
		"a missing seed":                 {"seed", "s-thatneverwas", protocol.ProfileErrorCodeNotFound},
		"an unknown kind":                {"spreadsheet", "/tmp/sheet.csv", protocol.ProfileErrorCodeInvalid},
		"an empty kind":                  {"", "https://example.com", protocol.ProfileErrorCodeInvalid},
		"a browser without a URL":        {"browser", "  ", protocol.ProfileErrorCodeInvalid},
		"a seed without an id":           {"seed", "", protocol.ProfileErrorCodeNotFound},
		"a markdown tile without a file": {"markdown", "", protocol.ProfileErrorCodeInvalid},
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
	w.apply(map[string]any{"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-app", "tile_kind": "app:kanban/board", "tile_params": `{"board":"work"}`, "edge": "right"})
	if tile := w.tile("tile-app"); tile.TileKind != "app:kanban/board" || tile.TileParams != `{"board":"work"}` {
		t.Fatalf("an app view dock stored %+v", tile)
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
	elsewhere := w.send(w.client, map[string]any{
		"cmd": protocol.CmdDesktopDockTile, "desktop_id": w.desktop.ID, "expected_revision": w.desktop.Revision,
		"tile_id": "tile-md", "tile_kind": "markdown", "tile_params": "/elsewhere.md", "edge": "left",
	})
	wantErrorCode(t, elsewhere, protocol.ProfileErrorCodeInvalid)
	if tile := w.tile("tile-md"); tile.TileKind != "markdown" || tile.TileParams != notes {
		t.Fatalf("refused re-docks changed the markdown tile to %+v", tile)
	}
	w.apply(map[string]any{"cmd": protocol.CmdDesktopDockTile, "tile_id": "tile-md", "tile_kind": "markdown", "tile_params": notes, "edge": "left"})
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

func (w *desktopTilesWorld) dockMarkdown(tileID, content string) string {
	w.t.Helper()
	path := filepath.Join(w.t.TempDir(), tileID+".md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		w.t.Fatal(err)
	}
	w.apply(map[string]any{"cmd": protocol.CmdDesktopDockTile, "tile_id": tileID, "tile_kind": "markdown", "tile_params": path, "edge": "right"})
	return path
}

func (w *desktopTilesWorld) deliveredTiles(client *wsClient) int {
	w.d.desktopTiles.mu.Lock()
	defer w.d.desktopTiles.mu.Unlock()
	return len(w.d.desktopTiles.delivered[client])
}

func contentsOf(messages []protocol.DesktopTileContentMessage) []string {
	contents := make([]string, 0, len(messages))
	for _, message := range messages {
		contents = append(contents, message.Content)
	}
	return contents
}

func TestTheDaemonSendsMarkdownContentToEveryClientOnTheCurrentDesktopOnce(t *testing.T) {
	w := newDesktopTilesWorld(t)
	path := w.dockMarkdown("tile-md", "# first")
	second, _ := w.connect(w.profileID)
	outsider, _ := w.connect("")
	elsewhere := w.mustSend(outsider, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "elsewhere"})
	w.mustSend(outsider, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": elsewhere.Profile.ID})
	drainClientPayloads(t, w.client)
	drainClientPayloads(t, second)
	drainClientPayloads(t, outsider)

	w.d.deliverDesktopTileContent()
	for name, client := range map[string]*wsClient{"the docking client": w.client, "a second client": second} {
		got := tileContents(t, client)
		if len(got) != 1 || got[0].Content != "# first" || got[0].Path != path || got[0].DesktopID != w.desktop.ID {
			t.Fatalf("%s received %+v, want the tile's content once", name, got)
		}
	}
	if leaked := tileContents(t, outsider); len(leaked) != 0 {
		t.Fatalf("a client on another profile received %d content messages", len(leaked))
	}

	w.d.deliverDesktopTileContent()
	if again := tileContents(t, w.client); len(again) != 0 {
		t.Fatalf("an unchanged file was sent again: %v", contentsOf(again))
	}

	replacement := path + ".next"
	if err := os.WriteFile(replacement, []byte("# second, written elsewhere and renamed over"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	w.d.deliverDesktopTileContent()
	if changed := contentsOf(tileContents(t, second)); len(changed) != 1 || changed[0] != "# second, written elsewhere and renamed over" {
		t.Fatalf("after an atomic replace the second client received %v", changed)
	}
}

func TestLeavingADesktopStopsItsContentAndReturningSendsItAgain(t *testing.T) {
	w := newDesktopTilesWorld(t)
	path := w.dockMarkdown("tile-md", "# notes")
	first := w.desktop
	other := w.mustSend(w.client, map[string]any{"cmd": protocol.CmdDesktopCreate, "profile_id": w.profileID}).Desktops[0]
	w.d.deliverDesktopTileContent()
	drainClientPayloads(t, w.client)

	w.mustSend(w.client, map[string]any{"cmd": protocol.CmdDesktopSetCurrent, "profile_id": w.profileID, "desktop_id": other.ID})
	w.d.deliverDesktopTileContent()
	if err := os.WriteFile(path, []byte("# edited while away"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.d.deliverDesktopTileContent()
	if late := tileContents(t, w.client); len(late) != 0 {
		t.Fatalf("a desktop the client left still streamed %v", contentsOf(late))
	}
	if held := w.deliveredTiles(w.client); held != 0 {
		t.Fatalf("leaving the desktop left %d delivered tiles tracked for the client", held)
	}

	w.mustSend(w.client, map[string]any{"cmd": protocol.CmdDesktopSetCurrent, "profile_id": w.profileID, "desktop_id": first.ID})
	w.d.deliverDesktopTileContent()
	if back := contentsOf(tileContents(t, w.client)); len(back) != 1 || back[0] != "# edited while away" {
		t.Fatalf("returning to the desktop sent %v, want the current file once", back)
	}
}

func TestARescopedClientGetsTheContentOfItsNewProfileOnly(t *testing.T) {
	w := newDesktopTilesWorld(t)
	w.dockMarkdown("tile-md", "# old profile")
	w.d.deliverDesktopTileContent()

	doomed := w.mustSend(w.client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "doomed"})
	w.mustSend(w.client, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": doomed.Profile.ID})
	oldProfile := w.profileID
	w.profileID, w.desktop = doomed.Profile.ID, doomed.Desktops[0]
	w.dockMarkdown("tile-doomed", "# doomed profile")
	drainClientPayloads(t, w.client)
	w.d.deliverDesktopTileContent()
	if got := contentsOf(tileContents(t, w.client)); len(got) != 1 || got[0] != "# doomed profile" {
		t.Fatalf("after profile_select the client received %v, want only the new profile's tile", got)
	}

	current, err := w.d.store.GetProfile(doomed.Profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	w.mustSend(w.client, map[string]any{
		"cmd": protocol.CmdProfileDelete, "profile_id": doomed.Profile.ID, "expected_revision": current.Revision, "destination_profile_id": oldProfile,
	})
	drainClientPayloads(t, w.client)
	w.d.deliverDesktopTileContent()
	if got := contentsOf(tileContents(t, w.client)); len(got) != 1 || got[0] != "# old profile" {
		t.Fatalf("after its profile was deleted the client received %v, want the destination's tile again", got)
	}

	w.d.wsHub.remove(w.client)
	w.d.deliverDesktopTileContent()
	if held := w.deliveredTiles(w.client); held != 0 {
		t.Fatalf("a disconnected client still has %d delivered tiles tracked", held)
	}
}

func TestArrangementChangesAndHelloWakeTheContentSender(t *testing.T) {
	w := newDesktopTilesWorld(t)
	drain := func() bool {
		select {
		case <-w.d.desktopTiles.nudge:
			return true
		default:
			return false
		}
	}
	drain()
	w.dockMarkdown("tile-md", "# notes")
	if !drain() {
		t.Fatal("an arrangement change did not wake the content sender")
	}
	w.connect(w.profileID)
	if !drain() {
		t.Fatal("a new client's initial state did not wake the content sender")
	}
	w.d.projectProfileArrangementChanged(bus.Event{Name: FactProfileArrangementChanged, Subject: w.profileID})
	if drain() {
		t.Fatal("the arrangement projection woke the content sender; projections only write to the wire")
	}
}

func TestContentAClientCouldNotQueueIsSentAgainOnTheNextTick(t *testing.T) {
	w := newDesktopTilesWorld(t)
	w.dockMarkdown("tile-md", "# notes")
	drainClientPayloads(t, w.client)
	for len(w.client.send) < cap(w.client.send) {
		w.client.send <- outboundMessage{kind: messageKindText, payload: []byte(`{"event":"filler"}`)}
	}

	w.d.deliverDesktopTileContent()
	if held := w.deliveredTiles(w.client); held != 0 {
		t.Fatalf("content that could not be queued was recorded as delivered (%d tiles)", held)
	}

	drainClientPayloads(t, w.client)
	w.d.deliverDesktopTileContent()
	if got := contentsOf(tileContents(t, w.client)); len(got) != 1 || got[0] != "# notes" {
		t.Fatalf("once the queue drained the client received %v, want the tile's content", got)
	}
}

func TestTileContentAlwaysFollowsTheArrangementItBelongsTo(t *testing.T) {
	w := newDesktopTilesWorld(t)
	first := w.desktop
	firstPath := w.dockMarkdown("tile-first", "# first desktop")
	second := w.mustSend(w.client, map[string]any{"cmd": protocol.CmdDesktopCreate, "profile_id": w.profileID}).Desktops[0]
	w.desktop = second
	w.mustSend(w.client, map[string]any{"cmd": protocol.CmdDesktopSetCurrent, "profile_id": w.profileID, "desktop_id": second.ID})
	secondPath := w.dockMarkdown("tile-second", "# second desktop")

	var stream [][]byte
	record := func() { stream = append(stream, drainClientPayloads(t, w.client)...) }
	for round, target := range []string{first.ID, second.ID, first.ID, second.ID} {
		w.d.deliverDesktopTileContent()
		record()
		for _, path := range []string{firstPath, secondPath} {
			if err := os.WriteFile(path, []byte(fmt.Sprintf("# edit %d", round)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		w.mustSend(w.client, map[string]any{"cmd": protocol.CmdDesktopSetCurrent, "profile_id": w.profileID, "desktop_id": target})
		w.d.deliverDesktopTileContent()
		record()
	}

	current, contents := "", 0
	for _, payload := range stream {
		switch eventName(t, payload) {
		case protocol.EventProfileArrangementChanged:
			var arrangement protocol.ProfileArrangementChangedMessage
			decodeInto(t, payload, &arrangement)
			current = arrangement.Profile.CurrentDesktopID
		case protocol.EventDesktopTileContent:
			var content protocol.DesktopTileContentMessage
			decodeInto(t, payload, &content)
			contents++
			if content.DesktopID != current {
				t.Fatalf("content for desktop %s arrived while the client's arrangement had %s current", content.DesktopID, current)
			}
		}
	}
	if contents == 0 {
		t.Fatal("no tile content was sent at all")
	}
}
