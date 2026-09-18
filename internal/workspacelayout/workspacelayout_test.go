package workspacelayout

import (
	. "github.com/victorarias/attn/internal/layouttree"
	"math"
	"testing"
)

func TestNormalizeWorkspaceLayoutPrunesMissingPanes(t *testing.T) {
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-gone",
		Layout: Node{
			Type:      "split",
			SplitID:   "root",
			Direction: DirectionVertical,
			Ratio:     DefaultSplitRatio,
			Children: []Node{
				{Type: "pane", PaneID: "pane-root"},
				{Type: "pane", PaneID: "pane-gone"},
			},
		},
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
		},
	}

	normalized := NormalizeWorkspaceLayout(snapshot)
	if normalized.Layout.Type != "pane" || normalized.Layout.PaneID != "pane-root" {
		t.Fatalf("normalized layout = %+v, want single agent pane", normalized.Layout)
	}
	if normalized.ActivePaneID != "pane-root" {
		t.Fatalf("active pane = %q, want pane-root", normalized.ActivePaneID)
	}
	if len(normalized.Panes) != 1 || normalized.Panes[0].PaneID != "pane-root" {
		t.Fatalf("normalized panes = %+v, want only pane-root", normalized.Panes)
	}
}

func TestNormalizeWorkspaceLayoutRebalancesSameDirectionChains(t *testing.T) {
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-b",
		Layout: Node{
			Type:      "split",
			SplitID:   "root",
			Direction: DirectionVertical,
			Ratio:     DefaultSplitRatio,
			Children: []Node{
				{
					Type:      "split",
					SplitID:   "left",
					Direction: DirectionVertical,
					Ratio:     DefaultSplitRatio,
					Children: []Node{
						{Type: "pane", PaneID: "pane-root"},
						{Type: "pane", PaneID: "pane-a"},
					},
				},
				{Type: "pane", PaneID: "pane-b"},
			},
		},
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
			{PaneID: "pane-a", RuntimeID: "sess-a", SessionID: "sess-a", Kind: PaneKindAgent, Title: "Agent 1"},
			{PaneID: "pane-b", RuntimeID: "sess-b", SessionID: "sess-b", Kind: PaneKindAgent, Title: "Agent 2"},
		},
	}

	normalized := NormalizeWorkspaceLayout(snapshot)
	if normalized.Layout.Type != "split" {
		t.Fatalf("normalized layout = %+v, want split root", normalized.Layout)
	}
	if math.Abs(normalized.Layout.Ratio-(2.0/3.0)) > 1e-9 {
		t.Fatalf("root ratio = %v, want %v", normalized.Layout.Ratio, 2.0/3.0)
	}
	left := normalized.Layout.Children[0]
	if left.Type != "split" || math.Abs(left.Ratio-0.5) > 1e-9 {
		t.Fatalf("left split = %+v, want balanced nested split", left)
	}
}

func TestNormalizeWorkspaceLayoutRebalancesAfterRemovingPaneFromChain(t *testing.T) {
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-b",
		Layout: Node{
			Type:      "split",
			SplitID:   "root",
			Direction: DirectionVertical,
			Ratio:     2.0 / 3.0,
			Children: []Node{
				{
					Type:      "split",
					SplitID:   "left",
					Direction: DirectionVertical,
					Ratio:     DefaultSplitRatio,
					Children: []Node{
						{Type: "pane", PaneID: "pane-root"},
						{Type: "pane", PaneID: "pane-gone"},
					},
				},
				{Type: "pane", PaneID: "pane-b"},
			},
		},
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
			{PaneID: "pane-b", RuntimeID: "sess-b", SessionID: "sess-b", Kind: PaneKindAgent, Title: "Agent 2"},
		},
	}

	normalized := NormalizeWorkspaceLayout(snapshot)
	if normalized.Layout.Type != "split" {
		t.Fatalf("normalized layout = %+v, want split root", normalized.Layout)
	}
	if math.Abs(normalized.Layout.Ratio-0.5) > 1e-9 {
		t.Fatalf("root ratio = %v, want 0.5", normalized.Layout.Ratio)
	}
}

func TestNormalizeWorkspaceLayoutPreservesLockedRatio(t *testing.T) {
	snapshot := WorkspaceLayout{
		WorkspaceID:  "workspace-1",
		ActivePaneID: "pane-a",
		Layout: Node{
			Type:        "split",
			SplitID:     "root",
			Direction:   DirectionVertical,
			Ratio:       0.72,
			RatioLocked: true,
			Children: []Node{
				{Type: "pane", PaneID: "pane-root"},
				{Type: "pane", PaneID: "pane-a"},
			},
		},
		Panes: []Pane{
			{PaneID: "pane-root", RuntimeID: "sess-1", SessionID: "sess-1", Kind: PaneKindAgent, Title: DefaultPaneTitle},
			{PaneID: "pane-a", RuntimeID: "sess-a", SessionID: "sess-a", Kind: PaneKindAgent, Title: "Agent 1"},
		},
	}

	normalized := NormalizeWorkspaceLayout(snapshot)
	if normalized.Layout.Type != "split" {
		t.Fatalf("normalized layout = %+v, want split root", normalized.Layout)
	}
	if !normalized.Layout.RatioLocked {
		t.Fatalf("locked flag lost during normalization")
	}
	if math.Abs(normalized.Layout.Ratio-0.72) > 1e-9 {
		t.Fatalf("locked ratio = %v, want 0.72 (must not rebalance to 0.5)", normalized.Layout.Ratio)
	}
}

func TestNormalizeLayoutDefaultsMissingRatioModeToAutomatic(t *testing.T) {
	root := Node{
		Type:        "split",
		SplitID:     "root",
		Direction:   DirectionVertical,
		Ratio:       0.68,
		RatioLocked: true,
		Children: []Node{
			{Type: "tile", TileID: "doc", TileKind: "markdown"},
			{Type: "pane", PaneID: "pane"},
		},
	}

	normalized := NormalizeLayout(root, map[string]Pane{
		"pane": {PaneID: "pane", RuntimeID: "runtime", SessionID: "session"},
	})
	if normalized.RatioMode != RatioModeAutomatic {
		t.Fatalf("ratio mode = %q, want automatic", normalized.RatioMode)
	}
}
