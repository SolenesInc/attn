package setupmigration

import (
	"fmt"
	"testing"

	"pgregory.net/rapid"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/setups"
)

func drawEdge(t *rapid.T) Edge {
	return rapid.SampledFrom([]Edge{EdgeLeft, EdgeRight, EdgeTop, EdgeBottom}).Draw(t, "edge")
}

func drawPaneOf(t *rapid.T, w *world, label string) (string, string, bool) {
	var candidates [][2]string
	for _, d := range w.desktops {
		for _, p := range d.Panes {
			candidates = append(candidates, [2]string{d.ID, p.PaneID})
		}
	}
	if len(candidates) == 0 {
		return "", "", false
	}
	pick := rapid.SampledFrom(candidates).Draw(t, label)
	return pick[0], pick[1], true
}

func applyRandomEdit(t *rapid.T, w *world, plan Plan, launched *int) Plan {
	live := w.live()
	ids := make([]string, 0, len(live))
	for _, g := range live {
		ids = append(ids, g.ID)
	}
	var next Plan
	var err error
	switch rapid.IntRange(0, 8).Draw(t, "edit") {
	case 6:
		*launched++
		paneID := fmt.Sprintf("launched-%d", *launched)
		slot := rapid.IntRange(0, setups.LastShortcutSlot).Draw(t, "new desktop slot")
		for _, d := range w.desktops {
			if slot != 0 && d.ShortcutSlot == slot {
				return plan
			}
		}
		w.desktops = append(w.desktops, setups.Desktop{ID: fmt.Sprintf("desktop-new-%d", *launched), ShortcutSlot: slot, Tree: layouttree.DefaultLayout(paneID), ActivePaneID: paneID, Panes: []setups.Pane{agentPane(paneID)}})
		return plan.Reconcile(w.desktops).Retire(w.live())
	case 7:
		d := &w.desktops[rapid.IntRange(0, len(w.desktops)-1).Draw(t, "reslotted")]
		slot := rapid.IntRange(0, setups.LastShortcutSlot).Draw(t, "slot")
		for _, other := range w.desktops {
			if slot != 0 && other.ShortcutSlot == slot {
				return plan
			}
		}
		d.ShortcutSlot = slot
		return plan.Reconcile(w.desktops).Retire(w.live())
	case 8:
		if len(w.desktops) < 2 {
			return plan
		}
		gone := rapid.IntRange(0, len(w.desktops)-1).Draw(t, "deleted desktop")
		w.desktops = append(w.desktops[:gone:gone], w.desktops[gone+1:]...)
		return plan.Reconcile(w.desktops).Retire(w.live())
	case 0:
		if len(ids) == 0 {
			return plan
		}
		next, err = plan.Keep(live, rapid.SliceOfNDistinct(rapid.SampledFrom(ids), 1, len(ids), rapid.ID[string]).Draw(t, "keep"))
	case 1:
		if len(ids) == 0 {
			return plan
		}
		group := rapid.SampledFrom(ids).Draw(t, "group")
		target := rapid.SampledFrom(plan.Desktops).Draw(t, "target").Key
		anchor := ""
		if rapid.Bool().Draw(t, "anchored") {
			anchor = rapid.SampledFrom(ids).Draw(t, "anchor")
		}
		next, err = plan.Move(live, group, target, anchor, drawEdge(t), rapid.Float64Range(0.05, 0.95).Draw(t, "share"))
	case 2:
		next, err = plan.Suggest(live)
	case 3:
		next, err = plan.Undo()
	case 4:
		if desktopID, paneID, ok := drawPaneOf(t, w, "close"); ok {
			w.closePane(desktopID, paneID)
		}
		return plan.Retire(w.live())
	default:
		if desktopID, paneID, ok := drawPaneOf(t, w, "launch beside"); ok {
			*launched++
			w.splitBeside(desktopID, paneID, fmt.Sprintf("launched-%d", *launched))
		}
		return plan
	}
	if err != nil {
		return plan
	}
	if err := next.Check(live); err != nil {
		t.Fatalf("an accepted edit left an invalid draft: %v", err)
	}
	return next
}

func TestFinishNeverLosesDuplicatesOrResurrectsALeaf(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := newWorld(rapid.IntRange(1, 13).Draw(t, "groups"))
		for i, group := range w.manifest.Groups {
			if rapid.Bool().Draw(t, "tile") {
				tileID := fmt.Sprintf("tile-%d", rapid.IntRange(0, 2).Draw(t, "tile id"))
				d := &w.desktops[i]
				d.Tree = layouttree.Node{Type: "split", SplitID: fmt.Sprintf("split-tile-%d", i), Direction: layouttree.DirectionHorizontal, Ratio: 0.5,
					Children: []layouttree.Node{d.Tree, {Type: "tile", TileID: tileID, TileKind: "markdown"}}}
				w.manifest.Groups[i].LeafIDs = append(group.LeafIDs, tileID)
			}
		}
		plan := InitialPlan(w.manifest)
		launched := 0
		steps := rapid.IntRange(0, 25).Draw(t, "steps")
		for i := 0; i < steps; i++ {
			plan = applyRandomEdit(t, w, plan, &launched)
		}
		live := w.live()
		plan = plan.Reconcile(w.desktops).Retire(live)
		if unconfirmed := plan.Unconfirmed(live); len(unconfirmed) > 0 {
			var err error
			if plan, err = plan.Keep(live, unconfirmed); err != nil {
				t.Fatal(err)
			}
		}
		before := make(map[string]int)
		for _, d := range w.desktops {
			for _, id := range layouttree.PaneIDs(d.Tree) {
				before[id]++
			}
		}
		outcome, err := Materialize(plan, live, w.desktops, counter())
		if err != nil {
			t.Fatalf("finish refused a checked, confirmed draft: %v", err)
		}
		after := make(map[string]int)
		for _, d := range outcome.Desktops {
			if err := setups.CheckDesktop(d); err != nil {
				t.Fatalf("finish produced an invalid desktop: %v", err)
			}
			for _, id := range layouttree.PaneIDs(d.Tree) {
				after[id]++
			}
		}
		for id, n := range after {
			if n != 1 || before[id] != 1 {
				t.Fatalf("pane %s appears %d times after finish and %d before", id, n, before[id])
			}
		}
		if len(after) != len(before) {
			t.Fatalf("finish placed %d panes, the desktops held %d", len(after), len(before))
		}
		slots := make(map[int]bool)
		for _, d := range outcome.Desktops {
			if d.ShortcutSlot != 0 && slots[d.ShortcutSlot] {
				t.Fatalf("slot %d is used twice", d.ShortcutSlot)
			}
			slots[d.ShortcutSlot] = true
		}
	})
}
