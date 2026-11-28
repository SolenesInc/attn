package profiles

import (
	"encoding/json"

	"github.com/victorarias/attn/internal/layouttree"
)

type DesktopKind string

const (
	DesktopKindDesktop DesktopKind = "desktop"
	DesktopKindHome    DesktopKind = "home"
)

const HomeDesktopLabel = "Home"

func HomeDesktopID(profileID string) string {
	return profileID + "/home"
}

// homeAttnShare is the part of Home's first column the attn view takes above Sessions. The screenshot
// tour's home-desktop scene (852 pt column) gives attn 207 pt at 0.25, what its brand line and the
// what's-new banner take.
const homeAttnShare = 0.25

// ViewTile reports whether a tile shows one of attn's views: a markdown tile whose params name the view.
func ViewTile(tile layouttree.TileLeaf) bool {
	var named struct {
		View string `json:"view"`
	}
	return tile.TileKind == string(layouttree.TileKindMarkdown) && json.Unmarshal([]byte(tile.TileParams), &named) == nil && named.View != ""
}

// ResetHomeTree is tree with its views back where HomeTree places them, and its other tiles in columns after them.
func ResetHomeTree(tree layouttree.Node, newSplitID func() string) layouttree.Node {
	for _, tile := range layouttree.TileLeaves(tree) {
		if ViewTile(tile) {
			tree, _ = layouttree.Remove(tree, tile.TileID)
		}
	}
	return layouttree.FromColumns(append(layouttree.Columns(HomeTree()), layouttree.Columns(tree)...), newSplitID)
}

// HomeTree is what Home holds by default: the attn view stacked above Sessions, and Pull requests in the next column.
func HomeTree() layouttree.Node {
	view := func(name string) layouttree.Node {
		return layouttree.Node{Type: "tile", TileID: "tile-view-" + name, TileKind: string(layouttree.TileKindMarkdown), TileParams: `{"view":"` + name + `"}`}
	}
	return layouttree.Node{
		Type: "split", SplitID: "home-row", Direction: layouttree.DirectionVertical, Ratio: 0.5, RatioMode: layouttree.RatioModeAutomatic,
		Children: []layouttree.Node{
			{
				Type: "split", SplitID: "home-first-column", Direction: layouttree.DirectionHorizontal, Ratio: homeAttnShare,
				Children: []layouttree.Node{view("attn"), view("sessions")},
			},
			view("pull-requests"),
		},
	}
}
