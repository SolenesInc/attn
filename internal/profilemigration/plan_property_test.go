package profilemigration

import (
	"fmt"
	"testing"

	"pgregory.net/rapid"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
)

func drawSavedDraft(t *rapid.T, plan Plan, live []GroupState) Plan {
	for _, group := range live {
		if !rapid.Bool().Draw(t, "moved") {
			continue
		}
		plan.remove(group.ID)
		target := &plan.Desktops[rapid.IntRange(0, len(plan.Desktops)-1).Draw(t, "target")]
		moved := Node{Group: group.ID}
		if target.Tree == nil {
			target.Tree = &moved
			continue
		}
		children := []Node{*target.Tree, moved}
		if rapid.Bool().Draw(t, "before") {
			children[0], children[1] = children[1], children[0]
		}
		direction := rapid.SampledFrom([]layouttree.Direction{layouttree.DirectionVertical, layouttree.DirectionHorizontal}).Draw(t, "direction")
		target.Tree = &Node{Direction: direction, Ratio: rapid.Float64Range(0.05, 0.95).Draw(t, "ratio"), Children: children}
	}
	if err := plan.Check(live); err != nil {
		t.Fatalf("the drawn draft is invalid: %v", err)
	}
	return plan
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

func applyRandomChange(t *rapid.T, w *world, plan Plan, launched *int) Plan {
	switch rapid.IntRange(0, 4).Draw(t, "change") {
	case 0:
		*launched++
		paneID := fmt.Sprintf("launched-%d", *launched)
		slot := rapid.IntRange(0, profiles.LastShortcutSlot).Draw(t, "new desktop slot")
		for _, d := range w.desktops {
			if slot != 0 && d.ShortcutSlot == slot {
				return plan
			}
		}
		w.desktops = append(w.desktops, profiles.Desktop{ID: fmt.Sprintf("desktop-new-%d", *launched), ShortcutSlot: slot, Tree: layouttree.DefaultLayout(paneID), ActivePaneID: paneID, Panes: []profiles.Pane{agentPane(paneID)}})
		return plan.Reconcile(w.desktops).Retire(w.live())
	case 1:
		d := &w.desktops[rapid.IntRange(0, len(w.desktops)-1).Draw(t, "reslotted")]
		slot := rapid.IntRange(0, profiles.LastShortcutSlot).Draw(t, "slot")
		for _, other := range w.desktops {
			if slot != 0 && other.ShortcutSlot == slot {
				return plan
			}
		}
		d.ShortcutSlot = slot
		return plan.Reconcile(w.desktops).Retire(w.live())
	case 2:
		if len(w.desktops) < 2 {
			return plan
		}
		gone := rapid.IntRange(0, len(w.desktops)-1).Draw(t, "deleted desktop")
		w.desktops = append(w.desktops[:gone:gone], w.desktops[gone+1:]...)
		return plan.Reconcile(w.desktops).Retire(w.live())
	case 3:
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
		plan := drawSavedDraft(t, InitialPlan(w.manifest), w.live())
		launched := 0
		steps := rapid.IntRange(0, 25).Draw(t, "steps")
		for i := 0; i < steps; i++ {
			plan = applyRandomChange(t, w, plan, &launched)
		}
		live := w.live()
		plan = plan.Reconcile(w.desktops).Retire(live)
		before := make(map[string]int)
		for _, d := range w.desktops {
			for _, id := range layouttree.PaneIDs(d.Tree) {
				before[id]++
			}
		}
		outcome, err := Materialize(plan, live, w.desktops, nil, counter())
		if err != nil {
			t.Fatalf("finish refused a reconciled draft: %v", err)
		}
		after := make(map[string]int)
		for _, d := range outcome.Desktops {
			if err := profiles.CheckDesktop(d); err != nil {
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
