package workspacelayout

import (
	. "github.com/victorarias/attn/internal/layouttree"
	"math"
	"slices"
	"testing"
)

func TestDockTilePersistsTileParams(t *testing.T) {
	path := "/Users/me/project/README.md"
	docked, ok := DockTile(DefaultLayout("pane-root"), "pane-root", DirectionVertical, false, "split-md", "tile-md", "markdown", path, "", 0.68)
	if !ok {
		t.Fatal("dock failed")
	}
	if params, ok := TileParamsByID(docked, "tile-md"); !ok || params != path {
		t.Fatalf("TileParamsByID = (%q, %v), want (%q, true)", params, ok, path)
	}

	snapshot := NormalizeWorkspaceLayout(WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-root",
		Layout:       docked,
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
		},
	})
	if params, _ := TileParamsByID(snapshot.Layout, "tile-md"); params != path {
		t.Fatalf("params lost in normalization: %q", params)
	}
	encoded, err := EncodeLayout(snapshot.Layout)
	if err != nil {
		t.Fatalf("EncodeLayout: %v", err)
	}
	decoded, err := DecodeLayout(encoded)
	if err != nil {
		t.Fatalf("DecodeLayout: %v", err)
	}
	if params, _ := TileParamsByID(decoded, "tile-md"); params != path {
		t.Fatalf("params lost in encode/decode: %q", params)
	}

	moved, ok := DockTile(decoded, "pane-root", DirectionVertical, false, "split-md", "tile-md", "markdown", "/other/notes.md", "", 0.5)
	if !ok {
		t.Fatal("re-dock failed")
	}
	if params, _ := TileParamsByID(moved, "tile-md"); params != "/other/notes.md" {
		t.Fatalf("re-dock did not retarget params: %q", params)
	}
	if leaves := TileLeaves(moved); len(leaves) != 1 {
		t.Fatalf("re-dock should keep a single tile, got %d", len(leaves))
	}
}

func TestNormalizeWorkspaceLayoutPreservesTileLeaf(t *testing.T) {
	docked, _ := DockTile(DefaultLayout("pane-root"), "pane-root", DirectionVertical, false, "split-md", "tile-md", "markdown", "", "", 0.68)
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-root",
		Layout:       docked,
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
		},
	}

	normalized := NormalizeWorkspaceLayout(snapshot)
	if !HasTile(normalized.Layout, "tile-md") {
		t.Fatal("tile pruned during normalization")
	}
	if ids := PaneIDs(normalized.Layout); !slices.Equal(ids, []string{"pane-root"}) {
		t.Fatalf("pane ids = %v, want only pane-root (tile excluded)", ids)
	}
	if len(normalized.Panes) != 1 {
		t.Fatalf("normalized panes = %+v, want only the agent pane", normalized.Panes)
	}
}

func TestNormalizeDropsMalformedTile(t *testing.T) {
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-root",
		Layout: Node{
			Type:      "split",
			SplitID:   "root",
			Direction: DirectionVertical,
			Ratio:     DefaultSplitRatio,
			Children: []Node{
				{Type: "pane", PaneID: "pane-root"},
				{Type: "tile", TileID: "tile-md"},
			},
		},
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
		},
	}
	normalized := NormalizeWorkspaceLayout(snapshot)
	if HasTile(normalized.Layout, "tile-md") {
		t.Fatal("malformed tile should be dropped during normalization")
	}
	if normalized.Layout.Type != "pane" || normalized.Layout.PaneID != "pane-root" {
		t.Fatalf("layout should collapse to the lone pane; got %+v", normalized.Layout)
	}
}

func TestDockedTileIsOpaqueToTerminalRebalance(t *testing.T) {
	docked, _ := DockTile(DefaultLayout("pane-a"), "pane-a", DirectionVertical, false, "split-md", "tile-md", "markdown", "", "", 0.7)
	withSecond, changed := Split(docked, "pane-a", "pane-b", "split-terminals", DirectionVertical, DefaultSplitRatio)
	if !changed {
		t.Fatal("splitting the terminal pane failed")
	}
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-a",
		Layout:       withSecond,
		Panes: []Pane{
			{PaneID: "pane-a", RuntimeID: "sess-a", SessionID: "sess-a", Kind: PaneKindAgent, Title: "A"},
			{PaneID: "pane-b", RuntimeID: "sess-b", SessionID: "sess-b", Kind: PaneKindAgent, Title: "B"},
		},
	}
	normalized := NormalizeWorkspaceLayout(snapshot)
	if !normalized.Layout.RatioLocked || math.Abs(normalized.Layout.Ratio-0.7) > 1e-9 {
		t.Fatalf("tile split ratio = %v (locked=%v), want preserved 0.7", normalized.Layout.Ratio, normalized.Layout.RatioLocked)
	}
	if normalized.Layout.RatioMode != RatioModeAutomatic {
		t.Fatalf("tile split ratio mode = %q, want automatic", normalized.Layout.RatioMode)
	}
}

func TestTileSessionIDSurvivesNormalizeAndEncode(t *testing.T) {
	docked, _ := DockTile(DefaultLayout("pane-root"), "pane-root", DirectionVertical, false, "split-md", "tile-md", "markdown", "/tmp/doc.md", "session-1", 0.68)
	snapshot := NormalizeWorkspaceLayout(WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-root",
		Layout:       docked,
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-a", SessionID: "sess-a", Kind: PaneKindAgent, Title: "A"},
		},
	})
	if sessionID, _ := TileSessionIDByID(snapshot.Layout, "tile-md"); sessionID != "session-1" {
		t.Fatalf("session lost in normalization: %q", sessionID)
	}
	encoded, err := EncodeLayout(snapshot.Layout)
	if err != nil {
		t.Fatalf("EncodeLayout: %v", err)
	}
	decoded, err := DecodeLayout(encoded)
	if err != nil {
		t.Fatalf("DecodeLayout: %v", err)
	}
	if sessionID, _ := TileSessionIDByID(decoded, "tile-md"); sessionID != "session-1" {
		t.Fatalf("session lost in encode/decode round-trip: %q", sessionID)
	}
	leaves := TileLeaves(decoded)
	if len(leaves) != 1 || leaves[0].TileSessionID != "session-1" {
		t.Fatalf("TileLeaves = %+v, want the bound session on the leaf", leaves)
	}
}
