package daemon_test

import (
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAMarkdownTileServesItsFileOrSaysWhyItCannot(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	dir := w.Path("files")
	if err := os.MkdirAll(filepath.Join(dir, "folder.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{
		"README.md":    []byte("# Title\n\nBody."),
		"too-large.md": make([]byte, 4<<20),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe.md"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		file        string
		wantContent string
	}{
		{"README.md", "# Title\n\nBody."},
		{"missing.md", ""},
		{"folder.md", ""},
		{"pipe.md", ""},
		{"too-large.md", ""},
	} {
		t.Run(c.file, func(t *testing.T) {
			path := filepath.Join(dir, c.file)
			tileID := "tile-" + c.file
			markdownTileDock(t, w, app, tileID, "markdown", path)
			got := awaitTileContent(app, tileID, path)
			if protocol.Deref(got.Content) != c.wantContent || (got.Error == nil) != (c.wantContent != "") {
				t.Errorf("the tile got content %q with error %q, want %q and an error only without content",
					protocol.Deref(got.Content), protocol.Deref(got.Error), c.wantContent)
			}
		})
	}

	desktop := currentDesktop(t, w)
	refused := dockOnDesktop(app, desktop, protocol.DesktopDockTileMessage{
		TileID: "tile-future", TileKind: "future", TileParams: protocol.Ptr(filepath.Join(dir, "README.md")), Edge: protocol.LayoutDockEdgeRight,
	})
	if refused.Success {
		t.Fatal("a tile of a kind the daemon does not know was docked")
	}
	for _, e := range app.Received() {
		if e.Event == protocol.EventDesktopTileContent && protocol.Deref(e.TileID) == "tile-future" {
			t.Fatal("the daemon served file content to a tile kind it does not know")
		}
	}
}

func TestEditsToAMarkdownFileReachItsTileAndStopWhenTheTileLeaves(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		watched, other, retargeted := markdownFile(t, w, "watched.md"), markdownFile(t, w, "other.md"), markdownFile(t, w, "retargeted.md")
		for tileID, path := range map[string]string{"tile-watched": watched, "tile-other": other} {
			markdownTileDock(t, w, app, tileID, "markdown", path)
		}
		w.advance(time.Second)

		mark := len(app.Received())
		info, err := os.Stat(watched)
		if err != nil {
			t.Fatal(err)
		}
		markdownTileEdit(t, watched, "# WATCHED.md\n")
		if err := os.Chtimes(watched, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
		w.advance(6 * time.Second)
		if got := markdownTileContents(app, mark, "tile-watched"); len(got) != 1 || protocol.Deref(got[0].Content) != "# WATCHED.md\n" {
			t.Errorf("after an edit that kept size and mtime the tile got %v, want the new body once", markdownTileBodies(got))
		}
		if got := markdownTileContents(app, mark, "tile-other"); len(got) != 0 {
			t.Errorf("the untouched tile-other was pushed %v", markdownTileBodies(got))
		}

		mark = len(app.Received())
		markdownTileEdit(t, other, "# other, edited with more bytes\n")
		w.advance(time.Second)
		if got := markdownTileContents(app, mark, "tile-other"); len(got) != 1 || protocol.Deref(got[0].Content) != "# other, edited with more bytes\n" {
			t.Errorf("after editing the other file its tile got %v, want the new body once", markdownTileBodies(got))
		}
		if got := markdownTileContents(app, mark, "tile-watched"); len(got) != 0 {
			t.Errorf("editing the other file pushed the watched tile %v", markdownTileBodies(got))
		}

		desktop := currentDesktop(t, w)
		if updated := desktopAction(app, protocol.DesktopUpdateTileMessage{
			Cmd: protocol.CmdDesktopUpdateTile, DesktopID: desktop.ID, TileID: "tile-watched", TileParams: protocol.Ptr(retargeted),
		}); updated.Success || !strings.Contains(protocol.Deref(updated.Error), "keeps its file") {
			t.Fatalf("pointing a markdown tile at another file = %+v, want it refused", updated)
		}

		desktop = currentDesktop(t, w)
		if removed := desktopAction(app, protocol.DesktopRemoveLeafMessage{
			Cmd: protocol.CmdDesktopRemoveLeaf, DesktopID: desktop.ID, LeafID: "tile-other",
		}); !removed.Success {
			t.Fatalf("removing the tile failed: %s", protocol.Deref(removed.Error))
		}
		mark = len(app.Received())
		markdownTileEdit(t, other, "# other, edited after the tile closed\n")
		w.advance(6 * time.Second)
		if got := markdownTileContents(app, mark, "tile-other"); len(got) != 0 {
			t.Errorf("the removed tile was still pushed %v", markdownTileBodies(got))
		}
	})
}

func TestRedockingATileKeepsTheSizeTheUserGaveIt(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	readme := markdownFile(t, w, "README.md")
	_, anchor := placedPane(t, w, "s1")
	for _, edge := range []struct {
		edge  protocol.LayoutDockEdge
		share *float64
	}{
		{protocol.LayoutDockEdgeRight, protocol.Ptr(0.41)},
		{protocol.LayoutDockEdgeBottom, nil},
	} {
		if docked := dockOnDesktop(app, currentDesktop(t, w), protocol.DesktopDockTileMessage{
			AnchorID: protocol.Ptr(anchor), Edge: edge.edge, TileID: "tile-readme", TileKind: "markdown", TileParams: protocol.Ptr(readme), TileShare: edge.share,
		}); !docked.Success {
			t.Fatalf("docking at the %s edge failed: %s", edge.edge, protocol.Deref(docked.Error))
		}
	}
	split, index, ok := desktopTree(t, currentDesktop(t, w)).splitHolding("tile-readme")
	fraction := split.Ratio
	if index == 1 {
		fraction = 1 - split.Ratio
	}
	if !ok || split.Direction != "horizontal" || math.Abs(fraction-0.41) > 1e-9 {
		t.Errorf("after moving the tile to the bottom it takes %v of a %s split (found=%v), want the 0.41 it was given", fraction, split.Direction, ok)
	}
}

func TestABareMarkdownOpenLandsBesideTheFocusedAgent(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1", "s2")
	file := markdownFile(t, w, "focused.md")

	focusAgent(t, w, app, "s1")
	if err := cli.OpenMarkdown(file, ""); err != nil {
		t.Fatalf("opening a file beside the focused agent: %v", err)
	}
	tiles := desktopTree(t, currentDesktop(t, w)).tiles()
	if len(tiles) != 1 || !slices.ContainsFunc(slices.Collect(maps.Values(tiles)), func(n layoutNode) bool {
		return n.TileKind == "markdown" && n.TileParams == file && n.TileSessionID == "s1"
	}) {
		t.Errorf("the focused agent's desktop has tiles %+v, want one markdown tile for %s bound to s1", tiles, file)
	}
}

func TestEachMarkdownFileGetsOneTileThatFollowsItsLatestOpener(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1", "s2")
	desktopID := currentDesktop(t, w).ID
	first, second, docked := markdownFile(t, w, "first.md"), markdownFile(t, w, "second.md"), markdownFile(t, w, "docked.md")
	markdownTileDock(t, w, app, "tile-markdown", "markdown", docked)
	open := func(session, path string) string {
		t.Helper()
		opened := requestOpenMarkdown(app, session, path)
		if !opened.Success || protocol.Deref(opened.DesktopID) != desktopID || protocol.Deref(opened.TileID) == "" {
			t.Fatalf("opening %s from %s answered %+v (%s)", path, session, opened, protocol.Deref(opened.Error))
		}
		return protocol.Deref(opened.TileID)
	}

	firstTile, secondTile := open("s1", first), open("s1", second)
	if firstTile == secondTile {
		t.Fatalf("two files share tile %s", firstTile)
	}
	dir := filepath.Dir(first)
	for _, spelling := range []string{dir + "/./first.md", dir + "//first.md", dir + "/sub/../first.md"} {
		if got := open("s1", spelling); got != firstTile {
			t.Errorf("opening %s used tile %s, want %s", spelling, got, firstTile)
		}
	}
	for _, c := range []struct{ session, path string }{{"s1", "docs/first.md"}, {"session-nobody-spawned", first}} {
		if refused := requestOpenMarkdown(app, c.session, c.path); refused.Success || refused.Error == nil {
			t.Errorf("opening %s from %s answered %+v, want a failure", c.path, c.session, refused)
		}
	}

	before := desktopTree(t, currentDesktop(t, w))
	if got := open("s2", first); got != firstTile {
		t.Fatalf("reopening from s2 used tile %s, want %s", got, firstTile)
	}
	if after := desktopTree(t, currentDesktop(t, w)); !reflect.DeepEqual(after, before.rebound(firstTile, "s2")) {
		t.Errorf("reopening from s2 changed the tree to %+v, want only the binding of %s moved", after, firstTile)
	}
	if got := open("s2", docked); got != "tile-markdown" {
		t.Errorf("opening a file a docked tile already shows used tile %s, want the existing tile-markdown", got)
	}

	type concurrentOpen struct {
		opener    *testworld.Peer
		path      string
		requestID string
	}
	var concurrent []concurrentOpen
	for i := range 8 {
		concurrent = append(concurrent, concurrentOpen{w.App(), markdownFile(t, w, fmt.Sprintf("concurrent-%d.md", i)), uuid.NewString()})
	}
	for _, o := range concurrent {
		o.opener.Send(protocol.OpenMarkdownMessage{Cmd: protocol.CmdOpenMarkdown, Path: o.path, SessionID: protocol.Ptr(protocol.SessionID("s1")), RequestID: protocol.Ptr(o.requestID)})
	}
	for _, o := range concurrent {
		if opened := testworld.Await(o.opener, protocol.EventOpenMarkdownResult, func(r protocol.OpenMarkdownResultMessage) bool {
			return protocol.Deref(r.RequestID) == o.requestID
		}); !opened.Success {
			t.Errorf("opening %s alongside others failed: %s", o.path, protocol.Deref(opened.Error))
		}
	}

	tilesByFile := map[string][]layoutNode{}
	for _, tile := range desktopTree(t, currentDesktop(t, w)).tiles() {
		tilesByFile[tile.TileParams] = append(tilesByFile[tile.TileParams], tile)
	}
	wantBinding := map[string]string{first: "s2", second: "s1", docked: "s2"}
	for _, o := range concurrent {
		wantBinding[o.path] = "s1"
	}
	for path, session := range wantBinding {
		if got := tilesByFile[path]; len(got) != 1 || got[0].TileKind != "markdown" || got[0].TileSessionID != session {
			t.Errorf("%s is shown by tiles %+v, want one markdown tile bound to %s", path, got, session)
		}
	}
	if len(tilesByFile) != len(wantBinding) {
		t.Errorf("the desktop shows %d files, want %d", len(tilesByFile), len(wantBinding))
	}
}

func TestFilesAnAgentSendsOpenAsMarkdownTilesUnlessTurnedOff(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	plan, notes, report, later, off := markdownFile(t, w, "plan.md"), markdownFile(t, w, "notes.markdown"),
		markdownFile(t, w, "report.html"), markdownFile(t, w, "later.md"), markdownFile(t, w, "off.md")
	shown := func() []string {
		var params []string
		for _, tile := range desktopTree(t, currentDesktop(t, w)).tiles() {
			params = append(params, tile.TileParams)
		}
		slices.Sort(params)
		return params
	}

	for _, call := range []struct {
		name    string
		session string
		files   []string
		want    []string
	}{
		{"markdown sent by a session", "s1", []string{plan, notes, report}, []string{notes, plan}},
		{"markdown sent by no session lands beside the current agent", "", []string{later}, []string{later, notes, plan}},
	} {
		if err := cli.OpenSentFiles(protocol.SessionID(call.session), call.files); err != nil {
			t.Fatalf("%s: the hook call failed: %v", call.name, err)
		}
		if got := shown(); !slices.Equal(got, call.want) {
			t.Fatalf("%s: the desktop shows %v, want %v", call.name, got, call.want)
		}
	}

	setSetting(t, app, "open_sent_files_enabled", "false")
	if err := cli.OpenSentFiles("s1", []string{off}); err != nil {
		t.Fatalf("with the setting off the hook call failed: %v", err)
	}
	if got := shown(); !slices.Equal(got, []string{later, notes, plan}) {
		t.Errorf("with the setting off the desktop shows %v, want nothing new", got)
	}
}

func currentDesktop(t *testing.T, w *world) protocol.Desktop {
	t.Helper()
	view := w.App().Initial
	selected := protocol.Deref(view.SelectedProfileID)
	for _, profile := range view.Profiles {
		if profile.ID == selected || selected == "" {
			for _, desktop := range view.Desktops {
				if desktop.ID == profile.CurrentDesktopID {
					return desktop
				}
			}
		}
	}
	t.Fatalf("the selected profile %q shows no current desktop", selected)
	return protocol.Desktop{}
}

func desktopAction(app *testworld.Peer, cmd any) protocol.ProfileActionResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	switch m := cmd.(type) {
	case protocol.DesktopUpdateTileMessage:
		m.RequestID = requestID
		cmd = m
	case protocol.DesktopRemoveLeafMessage:
		m.RequestID = requestID
		cmd = m
	}
	return testworld.Request(app, cmd, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == requestID })
}

func dockOnDesktop(app *testworld.Peer, desktop protocol.Desktop, dock protocol.DesktopDockTileMessage) protocol.ProfileActionResultMessage {
	app.T.Helper()
	dock.Cmd, dock.DesktopID, dock.RequestID = protocol.CmdDesktopDockTile, desktop.ID, uuid.NewString()
	return testworld.Request(app, dock, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == dock.RequestID })
}

func markdownTileDock(t *testing.T, w *world, app *testworld.Peer, tileID, kind, path string) {
	t.Helper()
	_, anchor := placedPane(t, w, "s1")
	if docked := dockOnDesktop(app, currentDesktop(t, w), protocol.DesktopDockTileMessage{
		AnchorID: protocol.Ptr(anchor), Edge: protocol.LayoutDockEdgeRight, TileID: tileID, TileKind: kind, TileParams: protocol.Ptr(path),
	}); !docked.Success {
		t.Fatalf("docking %s for %s failed: %s", tileID, path, protocol.Deref(docked.Error))
	}
}

func awaitTileContent(app *testworld.Peer, tileID, path string) protocol.WebSocketEvent {
	app.T.Helper()
	return testworld.Await(app, protocol.EventDesktopTileContent, func(e protocol.WebSocketEvent) bool {
		return protocol.Deref(e.TileID) == tileID && protocol.Deref(e.Path) == path
	})
}

func markdownTileEdit(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func markdownTileContents(p *testworld.Peer, from int, tileID string) []protocol.WebSocketEvent {
	var pushed []protocol.WebSocketEvent
	for _, e := range p.Received()[from:] {
		if e.Event == protocol.EventDesktopTileContent && (tileID == "" || protocol.Deref(e.TileID) == tileID) {
			pushed = append(pushed, e)
		}
	}
	return pushed
}

func markdownTileBodies(pushed []protocol.WebSocketEvent) []string {
	bodies := make([]string, 0, len(pushed))
	for _, e := range pushed {
		bodies = append(bodies, protocol.Deref(e.Path)+": "+protocol.Deref(e.Content))
	}
	return bodies
}

func (n layoutNode) rebound(tileID, sessionID string) layoutNode {
	if n.Type == "tile" && n.TileID == tileID {
		n.TileSessionID = sessionID
	}
	children := make([]layoutNode, len(n.Children))
	for i, child := range n.Children {
		children[i] = child.rebound(tileID, sessionID)
	}
	if n.Children != nil {
		n.Children = children
	}
	return n
}
