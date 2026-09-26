package daemon_test

import (
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
		"private.txt":  []byte("must not be returned"),
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
			markdownTileDock(t, app, tileID, "markdown", path)
			got := testworld.Request(app, protocol.WorkspaceTileContentGetMessage{
				Cmd: protocol.CmdWorkspaceTileContentGet, WorkspaceID: "workspace-docs", TileID: tileID,
			}, protocol.EventWorkspaceTileContent, func(m protocol.WorkspaceTileContentMessage) bool { return m.TileID == tileID })
			if got.Path != path || got.Content != c.wantContent || (got.Error == nil) != (c.wantContent != "") {
				t.Errorf("the tile got content %q from %s with error %q, want %q from %s and an error only without content",
					got.Content, got.Path, protocol.Deref(got.Error), c.wantContent, path)
			}
		})
	}

	markdownTileDock(t, app, "tile-future", "future", filepath.Join(dir, "private.txt"))
	app.Send(protocol.WorkspaceTileContentGetMessage{Cmd: protocol.CmdWorkspaceTileContentGet, WorkspaceID: "workspace-docs", TileID: "tile-future"})
	if refusal := testworld.Refused(app); protocol.Deref(refusal.Cmd) != protocol.CmdWorkspaceTileContentGet {
		t.Fatalf("asking for the content of a tile kind the daemon does not serve was refused for %q", protocol.Deref(refusal.Cmd))
	}
	for _, e := range app.Received() {
		if e.Event == protocol.EventWorkspaceTileContent && protocol.Deref(e.TileID) == "tile-future" {
			t.Fatal("the daemon served file content to a tile kind it does not know")
		}
	}
}

func TestEditsToAMarkdownFileReachOnlyTheClientsWatchingItsTile(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		bystander := w.App()
		placeSessions(t, w, app, cli, "s1")
		watched, other, unwatched, retargeted := markdownFile(t, w, "watched.md"), markdownFile(t, w, "other.md"),
			markdownFile(t, w, "unwatched.md"), markdownFile(t, w, "retargeted.md")
		for tileID, path := range map[string]string{"tile-watched": watched, "tile-other": other, "tile-unwatched": unwatched} {
			markdownTileDock(t, app, tileID, "markdown", path)
		}
		for _, tileID := range []string{"tile-watched", "tile-other"} {
			testworld.Request(app, protocol.WorkspaceTileContentGetMessage{
				Cmd: protocol.CmdWorkspaceTileContentGet, WorkspaceID: "workspace-docs", TileID: tileID,
			}, protocol.EventWorkspaceTileContent, func(m protocol.WorkspaceTileContentMessage) bool { return m.TileID == tileID })
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
		markdownTileEdit(t, unwatched, "# unwatched, edited\n")
		w.advance(6 * time.Second)
		if got := markdownTileContents(app, mark, "tile-watched"); len(got) != 1 || protocol.Deref(got[0].Content) != "# WATCHED.md\n" {
			t.Errorf("after an edit that kept size and mtime the watcher got %v, want the new body once", markdownTileBodies(got))
		}
		for _, tileID := range []string{"tile-other", "tile-unwatched"} {
			if got := markdownTileContents(app, mark, tileID); len(got) != 0 {
				t.Errorf("the untouched or unwatched %s was pushed %v", tileID, markdownTileBodies(got))
			}
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

		mark = len(app.Received())
		markdownTileDock(t, app, "tile-watched", "markdown", retargeted)
		w.advance(time.Second)
		markdownTileEdit(t, watched, "# watched, edited after the tile moved on\n")
		w.advance(6 * time.Second)
		if got := markdownTileBodies(markdownTileContents(app, mark, "tile-watched")); !slices.Equal(got, []string{retargeted + ": # retargeted.md\n"}) {
			t.Errorf("after the tile was pointed at %s it was pushed %q, want only the new file once", retargeted, got)
		}

		if undocked := workspaceLayoutAction(app, protocol.WorkspaceLayoutUndockTileMessage{
			Cmd: protocol.CmdWorkspaceLayoutUndockTile, WorkspaceID: "workspace-docs", TileID: "tile-other",
		}, protocol.CmdWorkspaceLayoutUndockTile, "workspace-docs"); !undocked.Success {
			t.Fatalf("undocking failed: %s", protocol.Deref(undocked.Error))
		}
		mark = len(app.Received())
		markdownTileEdit(t, other, "# other, edited after the tile closed\n")
		w.advance(6 * time.Second)
		if got := markdownTileContents(app, mark, "tile-other"); len(got) != 0 {
			t.Errorf("the undocked tile was still pushed %v", markdownTileBodies(got))
		}

		if got := markdownTileContents(bystander, 0, ""); len(got) != 0 {
			t.Errorf("a client that watched no tile was pushed %v", markdownTileBodies(got))
		}
	})
}

func TestRedockingATileKeepsTheSizeTheUserGaveIt(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	readme := markdownFile(t, w, "README.md")
	for _, edge := range []struct {
		edge  protocol.WorkspaceLayoutDockEdge
		ratio *float64
	}{
		{protocol.WorkspaceLayoutDockEdgeRight, protocol.Ptr(0.41)},
		{protocol.WorkspaceLayoutDockEdgeBottom, nil},
	} {
		if docked := workspaceLayoutAction(app, protocol.WorkspaceLayoutDockTileMessage{
			Cmd: protocol.CmdWorkspaceLayoutDockTile, WorkspaceID: "workspace-docs", AnchorPaneID: "pane-s1", Edge: edge.edge,
			TileID: "tile-readme", TileKind: "markdown", TileParams: protocol.Ptr(readme), Ratio: edge.ratio,
		}, protocol.CmdWorkspaceLayoutDockTile, "workspace-docs"); !docked.Success {
			t.Fatalf("docking at the %s edge failed: %s", edge.edge, protocol.Deref(docked.Error))
		}
	}
	root := workspaceLayoutTree(t, workspaceLayoutNow(t, w, "workspace-docs"))
	split, index, ok := root.splitHolding("tile-readme")
	fraction := split.Ratio
	if index == 1 {
		fraction = 1 - split.Ratio
	}
	if !ok || split.Direction != "horizontal" || math.Abs(fraction-0.41) > 1e-9 {
		t.Errorf("after moving the tile to the bottom it takes %v of a %s split (found=%v), want the 0.41 it was given", fraction, split.Direction, ok)
	}
}

func TestABareMarkdownOpenLandsBesideTheSelectedSession(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	file := markdownFile(t, w, "selected.md")

	if err := cli.OpenMarkdown(file, ""); err == nil {
		t.Fatal("opening a file with no session named or selected succeeded")
	}
	app.Send(protocol.SessionSelectedMessage{Cmd: protocol.CmdSessionSelected, ID: "s1"})
	recentLocations(app, 0)
	if err := cli.OpenMarkdown(file, ""); err != nil {
		t.Fatalf("opening a file beside the selected session: %v", err)
	}
	tiles := workspaceLayoutTree(t, workspaceLayoutNow(t, w, "workspace-docs")).tiles()
	if len(tiles) != 1 || !slices.ContainsFunc(slices.Collect(maps.Values(tiles)), func(n workspaceLayoutNode) bool {
		return n.TileKind == "markdown" && n.TileParams == file && n.TileSessionID == "s1"
	}) {
		t.Errorf("the selected session's workspace has tiles %+v, want one markdown tile for %s bound to s1", tiles, file)
	}
}

func TestEachMarkdownFileGetsOneTileThatFollowsItsLatestOpener(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1", "s2")
	first, second, legacy := markdownFile(t, w, "first.md"), markdownFile(t, w, "second.md"), markdownFile(t, w, "legacy.md")
	markdownTileDock(t, app, "tile-markdown", "markdown", legacy)
	open := func(session, path string) string {
		t.Helper()
		opened := requestOpenMarkdown(app, session, path)
		if !opened.Success || protocol.Deref(opened.WorkspaceID) != "workspace-docs" || protocol.Deref(opened.TileID) == "" {
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

	before := workspaceLayoutTree(t, workspaceLayoutNow(t, w, "workspace-docs"))
	if got := open("s2", first); got != firstTile {
		t.Fatalf("reopening from s2 used tile %s, want %s", got, firstTile)
	}
	if after := workspaceLayoutTree(t, workspaceLayoutNow(t, w, "workspace-docs")); !reflect.DeepEqual(after, before.rebound(firstTile, "s2")) {
		t.Errorf("reopening from s2 changed the layout to %+v, want only the binding of %s moved", after, firstTile)
	}
	if got := open("s2", legacy); got != "tile-markdown" {
		t.Errorf("opening a file an older layout already shows used tile %s, want the existing tile-markdown", got)
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
		o.opener.Send(protocol.OpenMarkdownMessage{Cmd: protocol.CmdOpenMarkdown, Path: o.path, SessionID: protocol.Ptr("s1"), RequestID: protocol.Ptr(o.requestID)})
	}
	for _, o := range concurrent {
		if opened := testworld.Await(o.opener, protocol.EventOpenMarkdownResult, func(r protocol.OpenMarkdownResultMessage) bool {
			return protocol.Deref(r.RequestID) == o.requestID
		}); !opened.Success {
			t.Errorf("opening %s alongside others failed: %s", o.path, protocol.Deref(opened.Error))
		}
	}

	tilesByFile := map[string][]workspaceLayoutNode{}
	for _, tile := range workspaceLayoutTree(t, workspaceLayoutNow(t, w, "workspace-docs")).tiles() {
		tilesByFile[tile.TileParams] = append(tilesByFile[tile.TileParams], tile)
	}
	wantBinding := map[string]string{first: "s2", second: "s1", legacy: "s2"}
	for _, o := range concurrent {
		wantBinding[o.path] = "s1"
	}
	for path, session := range wantBinding {
		if got := tilesByFile[path]; len(got) != 1 || got[0].TileKind != "markdown" || got[0].TileSessionID != session {
			t.Errorf("%s is shown by tiles %+v, want one markdown tile bound to %s", path, got, session)
		}
	}
	if len(tilesByFile) != len(wantBinding) {
		t.Errorf("the workspace shows %d files, want %d", len(tilesByFile), len(wantBinding))
	}
}

func TestFilesAnAgentSendsOpenAsMarkdownTilesUnlessTurnedOff(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	plan, notes, report, later := markdownFile(t, w, "plan.md"), markdownFile(t, w, "notes.markdown"),
		markdownFile(t, w, "report.html"), markdownFile(t, w, "later.md")
	shown := func() []string {
		var params []string
		for _, tile := range workspaceLayoutTree(t, workspaceLayoutNow(t, w, "workspace-docs")).tiles() {
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
		{"markdown sent by no session anyone can find", "", []string{later}, []string{notes, plan}},
	} {
		if err := cli.OpenSentFiles(call.session, call.files); err != nil {
			t.Fatalf("%s: the hook call failed: %v", call.name, err)
		}
		if got := shown(); !slices.Equal(got, call.want) {
			t.Fatalf("%s: the workspace shows %v, want %v", call.name, got, call.want)
		}
	}

	setSetting(t, app, "open_sent_files_enabled", "false")
	if err := cli.OpenSentFiles("s1", []string{later}); err != nil {
		t.Fatalf("with the setting off the hook call failed: %v", err)
	}
	if got := shown(); !slices.Equal(got, []string{notes, plan}) {
		t.Errorf("with the setting off the workspace shows %v, want nothing new", got)
	}
}

func markdownTileDock(t *testing.T, app *testworld.Peer, tileID, kind, path string) {
	t.Helper()
	if docked := workspaceLayoutAction(app, protocol.WorkspaceLayoutDockTileMessage{
		Cmd: protocol.CmdWorkspaceLayoutDockTile, WorkspaceID: "workspace-docs", AnchorPaneID: "pane-s1",
		Edge: protocol.WorkspaceLayoutDockEdgeRight, TileID: tileID, TileKind: kind, TileParams: protocol.Ptr(path),
	}, protocol.CmdWorkspaceLayoutDockTile, "workspace-docs"); !docked.Success {
		t.Fatalf("docking %s for %s failed: %s", tileID, path, protocol.Deref(docked.Error))
	}
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
		if e.Event == protocol.EventWorkspaceTileContent && (tileID == "" || protocol.Deref(e.TileID) == tileID) {
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

func (n workspaceLayoutNode) rebound(tileID, sessionID string) workspaceLayoutNode {
	if n.Type == "tile" && n.TileID == tileID {
		n.TileSessionID = sessionID
	}
	children := make([]workspaceLayoutNode, len(n.Children))
	for i, child := range n.Children {
		children[i] = child.rebound(tileID, sessionID)
	}
	if n.Children != nil {
		n.Children = children
	}
	return n
}
