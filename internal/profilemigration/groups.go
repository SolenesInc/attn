package profilemigration

import (
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
)

type GroupState struct {
	Group
	Tree  layouttree.Node
	Panes []profiles.Pane
}

func (g GroupState) LeafCount() int {
	return len(leafIDs(g.Tree))
}

func leafIDs(tree layouttree.Node) []string {
	return append(layouttree.PaneIDs(tree), layouttree.TileIDs(tree)...)
}

func Prune(tree layouttree.Node, keep map[string]bool) layouttree.Node {
	for _, id := range leafIDs(tree) {
		if !keep[id] {
			tree, _ = layouttree.Remove(tree, id)
		}
	}
	return tree
}

func desktopsByID(desktops []profiles.Desktop) map[string]profiles.Desktop {
	byID := make(map[string]profiles.Desktop, len(desktops))
	for _, desktop := range desktops {
		byID[desktop.ID] = desktop
	}
	return byID
}

func survivingLeaves(group Group, source profiles.Desktop) map[string]bool {
	present := make(map[string]bool)
	for _, id := range leafIDs(source.Tree) {
		present[id] = true
	}
	surviving := make(map[string]bool, len(group.LeafIDs))
	for _, id := range group.LeafIDs {
		if present[id] {
			surviving[id] = true
		}
	}
	return surviving
}

func LiveGroups(m Manifest, current []profiles.Desktop) []GroupState {
	byID := desktopsByID(current)
	var live []GroupState
	for _, group := range m.Groups {
		source, ok := byID[group.DesktopID]
		if !ok {
			continue
		}
		keep := survivingLeaves(group, source)
		state := GroupState{Group: group, Tree: Prune(source.Tree, keep)}
		for _, pane := range source.Panes {
			if keep[pane.PaneID] {
				state.Panes = append(state.Panes, pane)
			}
		}
		if len(state.Panes) == 0 {
			continue
		}
		live = append(live, state)
	}
	return live
}

func liveSet(live []GroupState) map[string]GroupState {
	byID := make(map[string]GroupState, len(live))
	for _, group := range live {
		byID[group.ID] = group
	}
	return byID
}
