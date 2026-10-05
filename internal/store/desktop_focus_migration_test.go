package store

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/layouttree"
)

func TestDesktopFocusHistoryUpgradePreservesSelectionAndLeftFallback(t *testing.T) {
	tile := func(id string) layouttree.Node {
		return layouttree.Node{Type: "tile", TileID: id, TileKind: "browser", TileParams: "https://example.com"}
	}
	split := func(id string, direction layouttree.Direction, first, second layouttree.Node) layouttree.Node {
		return layouttree.Node{Type: "split", SplitID: id, Direction: direction, Ratio: layouttree.DefaultSplitRatio, Children: []layouttree.Node{first, second}}
	}
	for _, row := range []struct {
		name              string
		tree              layouttree.Node
		show, close, want string
	}{
		{"first selection after upgrade", split("ab", layouttree.DirectionVertical, tile("a"), tile("b")), "b", "b", "a"},
		{"left neighbour without past visits", split("ab", layouttree.DirectionVertical, tile("a"), split("bc", layouttree.DirectionVertical, tile("b"), tile("c"))), "", "c", "b"},
		{"left neighbour rather than the tile above", split("ab", layouttree.DirectionVertical, tile("a"), split("bc", layouttree.DirectionHorizontal, tile("b"), tile("c"))), "", "c", "a"},
	} {
		for _, schema := range []int{169, 170} {
			t.Run(fmt.Sprintf("%d/%s", schema, row.name), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "attn.db")
				db, err := OpenDBAtSchemaVersion(path, schema)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Close() })
				if _, err := db.Exec(`ALTER TABLE desktops DROP COLUMN focus_history`); err != nil {
					t.Fatal(err)
				}
				encoded, err := layouttree.EncodeLayout(row.tree)
				if err != nil {
					t.Fatal(err)
				}
				active := row.close
				if row.show != "" {
					active = "a"
				}
				if _, err := db.Exec(`INSERT INTO desktops (id, profile_id, order_key, tree_json, active_pane_id, created_at, updated_at)
				SELECT 'old-desktop', id, 'z', ?, ?, 'now', 'now' FROM profiles`, encoded, active); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				s, err := NewWithDB(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { s.Close() })
				desktop, err := s.GetDesktop("old-desktop")
				if err != nil {
					t.Fatal(err)
				}
				if desktop.ActivePaneID != active {
					t.Fatalf("upgrade selected %s, want %s", desktop.ActivePaneID, active)
				}
				if row.show != "" {
					if _, _, _, err := s.ShowLeaf(desktop.ID, row.show); err != nil {
						t.Fatal(err)
					}
				}
				removed, err := s.RemoveLeaf(desktop.ID, row.close, desktop.Revision)
				if err != nil {
					t.Fatal(err)
				}
				if removed.ActivePaneID != row.want {
					t.Fatalf("close %s after upgrade selected %s, want %s", row.close, removed.ActivePaneID, row.want)
				}
			})
		}
	}
}
