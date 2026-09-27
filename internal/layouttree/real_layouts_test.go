package layouttree

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"

	"pgregory.net/rapid"
)

func loadRealLayouts(t interface{ Fatalf(string, ...any) }) []Node {
	raw, err := os.ReadFile("testdata/real_layouts.json")
	if err != nil {
		t.Fatalf("reading real layouts: %v", err)
	}
	var layouts []Node
	if err := json.Unmarshal(raw, &layouts); err != nil {
		t.Fatalf("decoding real layouts: %v", err)
	}
	if len(layouts) == 0 {
		t.Fatalf("testdata/real_layouts.json holds no layouts")
	}
	return layouts
}

func prefixIDs(node Node, prefix string) Node {
	switch node.Type {
	case "pane":
		node.PaneID = prefix + node.PaneID
	case "tile":
		node.TileID = prefix + node.TileID
	case "split":
		node.SplitID = prefix + node.SplitID
		children := make([]Node, len(node.Children))
		for i, child := range node.Children {
			children[i] = prefixIDs(child, prefix)
		}
		node.Children = children
	}
	return node
}

func leafRecords(nodes ...Node) map[string]Node {
	records := make(map[string]Node)
	var walk func(Node)
	walk = func(node Node) {
		switch node.Type {
		case "pane":
			records[node.PaneID] = node
		case "tile":
			records[node.TileID] = node
		case "split":
			for _, child := range node.Children {
				walk(child)
			}
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	return records
}

func sortedLeafIDs(node Node) []string {
	ids := append(PaneIDs(node), TileIDs(node)...)
	sort.Strings(ids)
	return ids
}

func TestRealLayoutsValidate(t *testing.T) {
	for i, layout := range loadRealLayouts(t) {
		if err := Validate(layout); err != nil {
			t.Fatalf("real layout %d does not validate: %v", i, err)
		}
		encoded, err := EncodeLayout(layout)
		if err != nil {
			t.Fatalf("real layout %d does not encode: %v", i, err)
		}
		decoded, err := DecodeLayout(encoded)
		if err != nil || !reflect.DeepEqual(decoded, layout) {
			t.Fatalf("real layout %d changed across encode and decode: %v\n got: %+v\nwant: %+v", i, err, decoded, layout)
		}
	}
}

func TestRealLayoutsKeepEveryLeafAcrossMovesBetweenTwoTrees(t *testing.T) {
	layouts := loadRealLayouts(t)
	rapid.Check(t, func(t *rapid.T) {
		trees := []Node{
			prefixIDs(rapid.SampledFrom(layouts).Draw(t, "first"), "a-"),
			prefixIDs(rapid.SampledFrom(layouts).Draw(t, "second"), "b-"),
		}
		want := leafRecords(trees...)
		minted := 0

		t.Repeat(map[string]func(*rapid.T){
			"move_between": func(t *rapid.T) {
				from := rapid.IntRange(0, 1).Draw(t, "from")
				to := 1 - from
				leaves := sortedLeafIDs(trees[from])
				if len(leaves) == 0 {
					t.Skip("source tree is empty")
				}
				leaf := rapid.SampledFrom(leaves).Draw(t, "leaf")
				anchor := rapid.SampledFrom(append([]string{""}, sortedLeafIDs(trees[to])...)).Draw(t, "anchor")
				direction := rapid.SampledFrom([]Direction{DirectionVertical, DirectionHorizontal}).Draw(t, "direction")
				minted++
				moved, ok := MoveLeafBetweenLayouts(trees[from], trees[to], leaf, anchor, fmt.Sprintf("minted-%d", minted), direction, rapid.Bool().Draw(t, "before"), rapid.Float64Range(0.1, 0.9).Draw(t, "ratio"), "collision")
				if !ok {
					t.Fatalf("moving %q beside %q failed", leaf, anchor)
				}
				if moved.FinalLeafID != leaf {
					t.Fatalf("leaf %q was renamed to %q although the trees share no ids", leaf, moved.FinalLeafID)
				}
				trees[from], trees[to] = moved.SourceLayout, moved.TargetLayout
			},
			"": func(t *rapid.T) {
				for i, tree := range trees {
					if err := Validate(tree); err != nil {
						t.Fatalf("tree %d stopped validating: %v", i, err)
					}
				}
				got := leafRecords(trees...)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("leaves changed across moves:\n got: %+v\nwant: %+v", got, want)
				}
				if overlap := len(sortedLeafIDs(trees[0])) + len(sortedLeafIDs(trees[1])); overlap != len(want) {
					t.Fatalf("the two trees hold %d leaves, want %d; a leaf is duplicated or lost", overlap, len(want))
				}
			},
		})
	})
}
