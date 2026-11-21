package profilemigration

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

type world struct {
	manifest Manifest
	desktops []profiles.Desktop
}

func agentPane(id string) profiles.Pane {
	return profiles.Pane{PaneID: id, Kind: profiles.PaneKindAgent, SessionID: protocol.SessionID("session-" + id), Status: profiles.PaneStatusReady}
}

func newWorld(groups int) *world {
	w := &world{}
	for i := 1; i <= groups; i++ {
		slot := 0
		if i <= profiles.LastShortcutSlot {
			slot = i
		}
		paneID := fmt.Sprintf("pane-%d", i)
		desktop := profiles.Desktop{ID: fmt.Sprintf("desktop-%d", i), ProfileID: "profile", ShortcutSlot: slot, Tree: layouttree.DefaultLayout(paneID), ActivePaneID: paneID, Panes: []profiles.Pane{agentPane(paneID)}, Revision: 1}
		w.desktops = append(w.desktops, desktop)
		w.manifest.Groups = append(w.manifest.Groups, Group{ID: fmt.Sprintf("group-%d", i), DesktopID: desktop.ID, ShortcutSlot: slot, LeafIDs: []string{paneID}})
	}
	return w
}

func (w *world) live() []GroupState {
	return LiveGroups(w.manifest, w.desktops)
}

func (w *world) desktop(id string) *profiles.Desktop {
	for i := range w.desktops {
		if w.desktops[i].ID == id {
			return &w.desktops[i]
		}
	}
	return nil
}

func (w *world) splitBeside(desktopID, anchor, paneID string) {
	d := w.desktop(desktopID)
	d.Tree, _ = layouttree.Split(d.Tree, anchor, paneID, "split-"+paneID, layouttree.DirectionVertical, 0.5)
	d.Panes = append(d.Panes, agentPane(paneID))
}

func (w *world) closePane(desktopID, paneID string) {
	d := w.desktop(desktopID)
	d.Tree, _ = layouttree.Remove(d.Tree, paneID)
	var kept []profiles.Pane
	for _, p := range d.Panes {
		if p.PaneID != paneID {
			kept = append(kept, p)
		}
	}
	d.Panes = kept
}

func draft(plan Plan, trees map[string]*Node) Plan {
	out := plan.clone()
	for i := range out.Desktops {
		if tree, ok := trees[out.Desktops[i].Key]; ok {
			out.Desktops[i].Tree = tree
		}
	}
	out.Desktops = slices.DeleteFunc(out.Desktops, func(desktop Desktop) bool {
		return desktop.ShortcutSlot == 0 && desktop.Tree == nil
	})
	return out
}

func beside(first, second string, direction layouttree.Direction, ratio float64) *Node {
	return &Node{Direction: direction, Ratio: ratio, Children: []Node{{Group: first}, {Group: second}}}
}

func counter() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("minted-%d", n)
	}
}

func keys(plan Plan) []string {
	var out []string
	for _, d := range plan.Desktops {
		out = append(out, d.Key)
	}
	return out
}

func finalLeaves(outcome Outcome) map[string]string {
	at := make(map[string]string)
	for _, d := range outcome.Desktops {
		for _, id := range leafIDs(d.Tree) {
			at[id] = fmt.Sprintf("%s/%d", d.ID, d.ShortcutSlot)
		}
	}
	return at
}

func TestInitialPlanOffersEveryShortcutSlotThenTheExtras(t *testing.T) {
	w := newWorld(11)
	w.manifest.Groups = w.manifest.Groups[:2]
	w.manifest.Groups = append(w.manifest.Groups, Group{ID: "group-10", DesktopID: "desktop-10", LeafIDs: []string{"pane-10"}})
	plan := InitialPlan(w.manifest)
	want := []string{"desktop-1", "desktop-2", "slot-3", "slot-4", "slot-5", "slot-6", "slot-7", "slot-8", "slot-9", "desktop-10"}
	if got := keys(plan); !reflect.DeepEqual(got, want) {
		t.Fatalf("initial keys = %v, want %v", got, want)
	}
	if err := plan.Check(w.live()[:2]); err == nil {
		t.Fatal("a draft placing a group that is not live passed its check")
	}
}

func TestClosedGroupsRetireFromTheDraft(t *testing.T) {
	w := newWorld(11)
	plan := draft(InitialPlan(w.manifest), map[string]*Node{"desktop-11": nil, "desktop-10": beside("group-10", "group-11", layouttree.DirectionVertical, 0.5)})
	w.closePane("desktop-10", "pane-10")
	retired := plan.Retire(w.live())
	if got := keys(retired); got[len(got)-1] != "desktop-10" || len(got) != 10 {
		t.Fatalf("keys = %v, want desktop-10 kept for group-11 and nothing else", got)
	}
	if got := retired.Desktops[9].Tree.groups(); !reflect.DeepEqual(got, []string{"group-11"}) {
		t.Fatalf("desktop-10 holds %v, want only group-11", got)
	}
}

func TestFinishKeepsConcurrentAgentsWhereTheyWereLaunched(t *testing.T) {
	w := newWorld(3)
	w.splitBeside("desktop-1", "pane-1", "pane-child-of-1")
	w.splitBeside("desktop-2", "pane-2", "pane-child-of-2")
	live := w.live()
	plan := InitialPlan(w.manifest)
	plan = draft(plan, map[string]*Node{"desktop-2": nil, "desktop-3": beside("group-2", "group-3", layouttree.DirectionVertical, 0.4)})
	outcome, err := Materialize(plan, live, w.desktops, nil, counter())
	if err != nil {
		t.Fatal(err)
	}
	at := finalLeaves(outcome)
	want := map[string]string{
		"pane-1": "desktop-1/1", "pane-child-of-1": "desktop-1/1",
		"pane-2": "desktop-3/3", "pane-3": "desktop-3/3",
		"pane-child-of-2": "desktop-2/2",
	}
	if !reflect.DeepEqual(at, want) {
		t.Fatalf("final placement = %v, want %v", at, want)
	}
	for _, d := range outcome.Desktops {
		if d.ID == "desktop-1" && !reflect.DeepEqual(d.Tree, w.desktops[0].Tree) {
			t.Fatalf("a kept desktop changed its tree: %+v", d.Tree)
		}
		if d.ID == "desktop-3" && (d.Tree.Children[0].PaneID != "pane-2" || d.Tree.Ratio != 0.4 || !d.Tree.RatioLocked) {
			t.Fatalf("merged desktop = %+v, want group-2 on the left at 40%%", d.Tree)
		}
	}
}

func TestFinishDeletesEmptiedExtrasAndCreatesDesktopsForUsedFreeSlots(t *testing.T) {
	w := newWorld(10)
	w.desktops = append(w.desktops[:2], w.desktops[9])
	w.manifest.Groups = append(w.manifest.Groups[:2], w.manifest.Groups[9])
	w.manifest.Groups[2].ShortcutSlot = 0
	w.desktops[2].ShortcutSlot = 0
	w.desktops[2].Tree = layouttree.Node{Type: "split", SplitID: "s", Direction: layouttree.DirectionVertical, Ratio: 0.5, Children: []layouttree.Node{
		layouttree.DefaultLayout("pane-10"), {Type: "tile", TileID: "tile-doc", TileKind: "markdown"},
	}}
	w.desktops[1].Tree = layouttree.Node{Type: "split", SplitID: "s2", Direction: layouttree.DirectionVertical, Ratio: 0.5, Children: []layouttree.Node{
		layouttree.DefaultLayout("pane-2"), {Type: "tile", TileID: "tile-doc", TileKind: "markdown"},
	}}
	w.manifest.Groups[1].LeafIDs = []string{"pane-2", "tile-doc"}
	w.manifest.Groups[2].LeafIDs = []string{"pane-10", "tile-doc"}
	live := w.live()
	plan := InitialPlan(w.manifest)
	plan = draft(plan, map[string]*Node{"desktop-10": nil, "slot-5": {Group: "group-10"}})
	outcome, err := Materialize(plan, live, w.desktops, nil, counter())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(outcome.Deleted, []string{"desktop-10"}) {
		t.Fatalf("deleted = %v, want the emptied extra", outcome.Deleted)
	}
	last := outcome.Desktops[len(outcome.Desktops)-1]
	if last.ID != "" || last.ShortcutSlot != 5 || !reflect.DeepEqual(layouttree.PaneIDs(last.Tree), []string{"pane-10"}) {
		t.Fatalf("new desktop = %+v, want pane-10 on a new desktop in slot 5", last)
	}

	plan = draft(plan, map[string]*Node{"slot-5": nil, "desktop-2": beside("group-2", "group-10", layouttree.DirectionHorizontal, 0.5)})
	outcome, err = Materialize(plan, live, w.desktops, nil, counter())
	if err != nil {
		t.Fatal(err)
	}
	merged := outcome.Desktops[1]
	tiles := layouttree.TileIDs(merged.Tree)
	sort.Strings(tiles)
	if !reflect.DeepEqual(tiles, []string{"tile-doc", "tile-doc-2"}) {
		t.Fatalf("merged tiles = %v, want the second copy of the document renamed", tiles)
	}
}

func TestAFreeSlotTakenDuringTheDraftReceivesTheGroupsMovedThere(t *testing.T) {
	w := newWorld(8)
	plan := draft(InitialPlan(w.manifest), map[string]*Node{"desktop-8": nil, "slot-9": {Group: "group-8"}})

	w.desktops = append(w.desktops, profiles.Desktop{ID: "desktop-new", ProfileID: "profile", ShortcutSlot: 9, Tree: layouttree.DefaultLayout("pane-launched"), ActivePaneID: "pane-launched", Panes: []profiles.Pane{agentPane("pane-launched")}})
	live := w.live()
	outcome, err := Materialize(plan.Reconcile(w.desktops).Retire(live), live, w.desktops, nil, counter())
	if err != nil {
		t.Fatal(err)
	}
	inSlot9 := 0
	for _, d := range outcome.Desktops {
		if d.ShortcutSlot != 9 {
			continue
		}
		inSlot9++
		if d.ID != "desktop-new" || len(d.Panes) != 2 {
			t.Fatalf("slot 9 = %+v, want desktop-new holding its launched agent and group-8", d)
		}
	}
	if inSlot9 != 1 {
		t.Fatalf("slot 9 appears %d times in the outcome", inSlot9)
	}
}

func TestASlotFreedDuringTheDraftBecomesATargetAgain(t *testing.T) {
	w := newWorld(4)
	plan := InitialPlan(w.manifest)
	w.desktops[2].ShortcutSlot = 0
	live := w.live()
	plan = plan.Reconcile(w.desktops).Retire(live)
	if err := plan.Check(live); err != nil {
		t.Fatalf("reconciled draft: %v", err)
	}
	if got := keys(plan); !reflect.DeepEqual(got, []string{"desktop-1", "desktop-2", "slot-3", "desktop-4", "slot-5", "slot-6", "slot-7", "slot-8", "slot-9", "desktop-3"}) {
		t.Fatalf("keys = %v, want slot 3 free again and desktop-3 among the extras", got)
	}
	plan = draft(plan, map[string]*Node{"desktop-4": nil, "slot-3": {Group: "group-4"}})
	outcome, err := Materialize(plan, live, w.desktops, nil, counter())
	if err != nil {
		t.Fatal(err)
	}
	if at := finalLeaves(outcome); at["pane-4"] != "/3" || at["pane-3"] != "desktop-3/0" {
		t.Fatalf("final placement = %v, want group-4 on a new slot-3 desktop and desktop-3 kept as an extra", at)
	}
}
