package layouttree

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"pgregory.net/rapid"
)

func generatedTree(t *rapid.T) Node {
	next := 0
	mint := func(prefix string) string {
		next++
		return fmt.Sprintf("%s%d", prefix, next)
	}
	var build func(depth int) Node
	build = func(depth int) Node {
		if depth == 0 || rapid.IntRange(0, 3).Draw(t, "kind") == 0 {
			if rapid.Bool().Draw(t, "pane") {
				return Node{Type: "pane", PaneID: mint("pane-")}
			}
			return Node{Type: "tile", TileID: mint("tile-"), TileKind: "browser", WantedWidth: WantedWidth{WantedCols: rapid.IntRange(0, 200).Draw(t, "wanted"), WantedAreaCols: rapid.IntRange(0, 400).Draw(t, "wanted area")}}
		}
		return Node{
			Type:        "split",
			SplitID:     mint("split-"),
			Direction:   rapid.SampledFrom([]Direction{DirectionVertical, DirectionHorizontal}).Draw(t, "direction"),
			Ratio:       rapid.Float64Range(0.1, 0.9).Draw(t, "ratio"),
			RatioLocked: rapid.Bool().Draw(t, "locked"),
			RatioMode:   RatioModeAutomatic,
			Children:    []Node{build(depth - 1), build(depth - 1)},
		}
	}
	return build(rapid.IntRange(0, 6).Draw(t, "depth"))
}

func requireRebuiltRowKeepsEverything(t interface {
	Fatalf(string, ...any)
}, tree Node) {
	minted := 0
	rebuilt := FromColumns(Columns(tree), func() string {
		minted++
		return fmt.Sprintf("rebuilt-%d", minted)
	})
	if !slices.Equal(LeafIDs(rebuilt), LeafIDs(tree)) {
		t.Fatalf("leaves went from %v to %v", LeafIDs(tree), LeafIDs(rebuilt))
	}
	if !reflect.DeepEqual(Columns(rebuilt), Columns(tree)) {
		t.Fatalf("columns went from %+v to %+v", Columns(tree), Columns(rebuilt))
	}
	if err := Validate(rebuilt); err != nil {
		t.Fatalf("the rebuilt tree does not validate: %v", err)
	}
}

func TestRebuildingARowFromItsColumnsKeepsEveryLeafTheirOrderAndEachColumn(t *testing.T) {
	for _, layout := range loadRealLayouts(t) {
		requireRebuiltRowKeepsEverything(t, layout)
	}
	rapid.Check(t, func(t *rapid.T) {
		requireRebuiltRowKeepsEverything(t, generatedTree(t))
	})
}
