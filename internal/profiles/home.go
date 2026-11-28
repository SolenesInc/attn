package profiles

import (
	"encoding/json"
	"slices"

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

// BuiltInViews are the views of attn a tile can show.
var BuiltInViews = []string{"attn", "sessions", "pull-requests"}

type namedView struct {
	View string `json:"view"`
}

// ViewParams are the params of a markdown tile that shows view.
func ViewParams(view string) string {
	encoded, _ := json.Marshal(namedView{View: view})
	return string(encoded)
}

// ViewNamed is the view that a markdown tile's params name, if they name one.
func ViewNamed(params string) (string, bool) {
	var named namedView
	if json.Unmarshal([]byte(params), &named) != nil || named.View == "" {
		return "", false
	}
	return named.View, true
}

// ViewTile reports whether a tile shows one of attn's views.
func ViewTile(tile layouttree.TileLeaf) bool {
	view, named := ViewNamed(tile.TileParams)
	return tile.TileKind == string(layouttree.TileKindMarkdown) && named && slices.Contains(BuiltInViews, view)
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
		return layouttree.Node{Type: "tile", TileID: "tile-view-" + name, TileKind: string(layouttree.TileKindMarkdown), TileParams: ViewParams(name)}
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
