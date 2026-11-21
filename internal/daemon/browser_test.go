package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
)

func TestOpenBrowserDocksBesideTheAgentThatOpensIt(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	_, second, err := d.store.CreateDesktop(desktop.ProfileID, "", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	injectTestSession(t, d, protocol.Session{ID: "session-2", Label: "other", Directory: t.TempDir()})
	if _, err := d.store.RemoveSessionPlacement("session-2"); err != nil {
		t.Fatal(err)
	}
	placeTestSession(t, d, "session-2", second.ID)
	focusTestAgent(t, d, "session-1")

	if err := openBrowserFor(t, d, "", "http://localhost:3000"); err != nil {
		t.Fatalf("open_browser for the current agent: %v", err)
	}
	if tile := desktopTile(t, d, desktop.ID, browserTileID); tile.TileParams != "http://localhost:3000" {
		t.Fatalf("browser on the current agent's desktop = %+v", tile)
	}

	if err := openBrowserFor(t, d, "session-2", "http://localhost:4000"); err != nil {
		t.Fatalf("open_browser for session-2: %v", err)
	}
	if tile := desktopTile(t, d, second.ID, browserTileID); tile.TileParams != "http://localhost:4000" {
		t.Fatalf("browser on session-2's desktop = %+v", tile)
	}
	if tile := desktopTile(t, d, desktop.ID, browserTileID); tile.TileParams != "http://localhost:3000" {
		t.Fatalf("the current agent's browser moved to %q", tile.TileParams)
	}
}

func TestOpenBrowserRetargetsTheBrowserOnADesktopWithoutAgents(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	if err := openBrowserFor(t, d, "", "http://localhost:3000"); err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := d.store.RemoveSessionPlacement("session-1"); err != nil {
		t.Fatal(err)
	}
	if err := openBrowserFor(t, d, "", "https://example.com/retargeted"); err != nil {
		t.Fatalf("second open: %v", err)
	}
	if tile := desktopTile(t, d, desktop.ID, browserTileID); tile.TileParams != "https://example.com/retargeted" {
		t.Fatalf("retargeted browser = %+v", tile)
	}
}

func TestOpenBrowserDocksBesideTheTileOfAPanelessDesktop(t *testing.T) {
	d, desktop := setupAgentDesktop(t)
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("# notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := d.openMarkdownTile("", notes, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.RemoveSessionPlacement("session-1"); err != nil {
		t.Fatal(err)
	}
	if err := openBrowserFor(t, d, "", "https://example.com"); err != nil {
		t.Fatalf("open_browser: %v", err)
	}
	updated, err := d.store.GetDesktop(desktop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !browserTileOn(updated.Tree) || !layouttree.HasTile(updated.Tree, markdownTileIDForPath(notes)) {
		t.Fatalf("desktop tree = %+v, want the browser beside the notes tile", updated.Tree)
	}
}
