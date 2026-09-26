package daemon

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/workspacelayout"
)

func assertTileOnlyWorkspaceAlive(t *testing.T, d *Daemon, workspaceID, sessionID string) {
	t.Helper()
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("session %s still registered after its pane closed", sessionID)
	}
	if ws := d.store.GetWorkspace(workspaceID); ws == nil {
		t.Fatal("workspace was torn down even though a docked tile remained")
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout was removed even though a docked tile remained")
	}
	if tiles := workspacelayout.TileIDs(snapshot.Layout); len(tiles) != 1 || !strings.HasPrefix(tiles[0], markdownTileIDPrefix) {
		t.Fatalf("layout tiles = %v, want a single %s* tile", tiles, markdownTileIDPrefix)
	}
	if panes := workspacelayout.PaneIDs(snapshot.Layout); len(panes) != 0 {
		t.Fatalf("layout panes = %v, want none after the session pane closed", panes)
	}
	if _, registered := d.workspaces.snapshot(workspaceID); !registered {
		t.Fatal("workspace dropped from the in-memory registry")
	}
	if ids := d.workspaces.sessionIDs(workspaceID); len(ids) != 0 {
		t.Fatalf("workspace still tracks sessions %v after its last session left", ids)
	}
}
