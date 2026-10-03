package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

func TestOpenMarkdownDocksBesideTheCurrentAgent(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	file := filepath.Join(t.TempDir(), "current.md")
	if err := os.WriteFile(file, []byte("# Current"), 0o644); err != nil {
		t.Fatal(err)
	}
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleOpenMarkdown(serverConn, &protocol.OpenMarkdownMessage{Cmd: protocol.CmdOpenMarkdown, Path: file})
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("open_markdown failed: %v", protocol.Deref(resp.Error))
	}
	if tile := desktopTile(t, d, desktop.ID, markdownTileIDForPath(file)); tile.TileParams != file || tile.TileSessionID != "session-1" {
		t.Fatalf("markdown tile = %+v, want the file bound to the current agent", tile)
	}
}

func TestOpenMarkdownForAnUnknownSessionNamesIt(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	file := filepath.Join(t.TempDir(), "remote.md")
	if err := os.WriteFile(file, []byte("# Remote"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.openMarkdownTile(file, "session-remote"); err == nil || !strings.Contains(err.Error(), "session-remote") {
		t.Fatalf("open for an unknown session = %v, want an error naming it", err)
	}
	after, err := d.store.GetDesktop(desktop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if leaves := layouttree.TileLeaves(after.Tree); len(leaves) != 0 {
		t.Fatalf("the current desktop gained tiles %+v", leaves)
	}
}

func TestOpenMarkdownReusesATileAlreadyShowingTheFile(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	file := filepath.Join(t.TempDir(), "legacy.md")
	if err := os.WriteFile(file, []byte("# Legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.UpdateDesktopArrangement(desktop.ID, desktop.Revision, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		return dockTileOnDesktop(desktop, desktopTileDock{tileID: "tile-markdown", tileKind: string(layouttree.TileKindMarkdown), params: file, edge: protocol.LayoutDockEdgeRight})
	}); err != nil {
		t.Fatalf("dock the existing tile: %v", err)
	}

	gotDesktop, gotTile, err := d.openMarkdownTile(file, "session-1")
	if err != nil {
		t.Fatalf("openMarkdownTile: %v", err)
	}
	if gotDesktop != desktop.ID || gotTile != "tile-markdown" {
		t.Fatalf("open = (%q, %q), want the existing tile reused on %q", gotDesktop, gotTile, desktop.ID)
	}
	if leaves := layouttree.TileLeaves(desktopTree(t, d, desktop.ID)); len(leaves) != 1 {
		t.Fatalf("tile leaves = %+v, want the existing tile only (no hashed duplicate)", leaves)
	}
	if tile := desktopTile(t, d, desktop.ID, "tile-markdown"); tile.TileSessionID != "session-1" {
		t.Fatalf("existing tile binding = %q, want session-1", tile.TileSessionID)
	}
}

func TestRedockingATileKeepsItsFraction(t *testing.T) {
	tileID := markdownTileIDForPath("/tmp/README.md")
	desktop := profiles.Desktop{ID: "desktop-1", Tree: layouttree.DefaultLayout("pane-1")}
	docked, err := dockTileOnDesktop(desktop, desktopTileDock{
		tileID: tileID, tileKind: string(layouttree.TileKindMarkdown), params: "/tmp/README.md",
		anchorID: "pane-1", edge: protocol.LayoutDockEdgeRight, share: 0.41,
	})
	if err != nil {
		t.Fatalf("dock: %v", err)
	}
	moved, err := dockTileOnDesktop(docked, desktopTileDock{
		tileID: tileID, tileKind: string(layouttree.TileKindMarkdown), params: "/tmp/README.md",
		anchorID: "pane-1", edge: protocol.LayoutDockEdgeBottom,
	})
	if err != nil {
		t.Fatalf("re-dock: %v", err)
	}
	if got, ok := layouttree.TileFractionByID(moved.Tree, tileID); !ok || got < 0.41-1e-9 || got > 0.41+1e-9 {
		t.Fatalf("tile fraction after moving = (%v, %v), want (0.41, true)", got, ok)
	}
}
