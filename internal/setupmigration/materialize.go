package setupmigration

import (
	"fmt"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/setups"
)

type Outcome struct {
	Desktops []setups.Desktop
	Deleted  []string
}

type materializer struct {
	live       map[string]GroupState
	current    map[string]setups.Desktop
	panes      map[string]setups.Pane
	claimed    map[string]bool
	homeKept   map[string]bool
	newSplitID func() string
}

func newMaterializer(live []GroupState, current []setups.Desktop, newSplitID func() string) *materializer {
	m := &materializer{
		live:       liveSet(live),
		current:    desktopsByID(current),
		panes:      make(map[string]setups.Pane),
		claimed:    make(map[string]bool),
		homeKept:   make(map[string]bool),
		newSplitID: newSplitID,
	}
	for _, desktop := range current {
		for _, pane := range desktop.Panes {
			m.panes[pane.PaneID] = pane
		}
	}
	for _, group := range live {
		for _, id := range leafIDs(group.Tree) {
			m.claimed[id] = true
		}
	}
	return m
}

func (m *materializer) unclaimed(desktopID string) map[string]bool {
	keep := make(map[string]bool)
	for _, id := range leafIDs(m.current[desktopID].Tree) {
		if !m.claimed[id] {
			keep[id] = true
		}
	}
	return keep
}

func (m *materializer) groupTree(groupID, destinationID string) layouttree.Node {
	group := m.live[groupID]
	if group.DesktopID != destinationID {
		return group.Tree
	}
	m.homeKept[destinationID] = true
	keep := m.unclaimed(destinationID)
	for _, id := range leafIDs(group.Tree) {
		keep[id] = true
	}
	return Prune(m.current[destinationID].Tree, keep)
}

func (m *materializer) split(first, second layouttree.Node, direction layouttree.Direction, ratio float64) layouttree.Node {
	switch {
	case layouttree.LayoutEmpty(first):
		return second
	case layouttree.LayoutEmpty(second):
		return first
	}
	return layouttree.Node{
		Type:        "split",
		SplitID:     m.newSplitID(),
		Direction:   direction,
		Ratio:       ratio,
		RatioLocked: true,
		RatioMode:   layouttree.RatioModePreferred,
		Children:    []layouttree.Node{first, second},
	}
}

func (m *materializer) tree(node *Node, destinationID string) layouttree.Node {
	if node == nil {
		return layouttree.Node{}
	}
	if node.Group != "" {
		return m.groupTree(node.Group, destinationID)
	}
	first := m.tree(&node.Children[0], destinationID)
	second := m.tree(&node.Children[1], destinationID)
	return m.split(first, second, node.Direction, node.Ratio)
}

func (m *materializer) leftover(desktopID string) layouttree.Node {
	if m.homeKept[desktopID] {
		return layouttree.Node{}
	}
	return Prune(m.current[desktopID].Tree, m.unclaimed(desktopID))
}

func renameTileCollisions(node layouttree.Node, taken map[string]bool) layouttree.Node {
	if node.Type == "tile" {
		id := node.TileID
		for n := 2; taken[id]; n++ {
			id = fmt.Sprintf("%s-%d", node.TileID, n)
		}
		node.TileID = id
		taken[id] = true
		return node
	}
	if len(node.Children) == 0 {
		return node
	}
	children := make([]layouttree.Node, len(node.Children))
	for i, child := range node.Children {
		children[i] = renameTileCollisions(child, taken)
	}
	node.Children = children
	return node
}

func (m *materializer) finalDesktop(existing setups.Desktop, slot int, tree layouttree.Node) setups.Desktop {
	desktop := existing
	desktop.ShortcutSlot = slot
	taken := make(map[string]bool)
	for _, id := range layouttree.PaneIDs(tree) {
		taken[id] = true
	}
	desktop.Tree = renameTileCollisions(tree, taken)
	desktop.Panes = nil
	for _, id := range layouttree.PaneIDs(desktop.Tree) {
		desktop.Panes = append(desktop.Panes, m.panes[id])
	}
	return setups.Settle(desktop)
}

func Materialize(plan Plan, live []GroupState, current []setups.Desktop, newSplitID func() string) (Outcome, error) {
	if err := plan.Check(live); err != nil {
		return Outcome{}, err
	}
	if pending := plan.Unconfirmed(live); len(pending) > 0 {
		return Outcome{}, setups.Errorf(setups.CodeInvalid, "%d imported group(s) still need a confirmation before finishing: %v", len(pending), pending)
	}
	m := newMaterializer(live, current, newSplitID)
	var outcome Outcome
	inPlan := make(map[string]bool)
	for _, planned := range plan.Desktops {
		tree := m.tree(planned.Tree, planned.DesktopID)
		existing := setups.Desktop{}
		if planned.DesktopID != "" {
			inPlan[planned.DesktopID] = true
			existing = m.current[planned.DesktopID]
			tree = m.split(tree, m.leftover(planned.DesktopID), layouttree.DirectionVertical, layouttree.DefaultSplitRatio)
		}
		if planned.DesktopID == "" && layouttree.LayoutEmpty(tree) {
			continue
		}
		outcome.Desktops = append(outcome.Desktops, m.finalDesktop(existing, planned.ShortcutSlot, tree))
	}
	for _, desktop := range current {
		if inPlan[desktop.ID] {
			continue
		}
		leftover := m.leftover(desktop.ID)
		if layouttree.LayoutEmpty(leftover) {
			outcome.Deleted = append(outcome.Deleted, desktop.ID)
			continue
		}
		outcome.Desktops = append(outcome.Desktops, m.finalDesktop(desktop, desktop.ShortcutSlot, leftover))
	}
	for _, desktop := range outcome.Desktops {
		if err := layouttree.Validate(desktop.Tree); err != nil {
			return Outcome{}, setups.Errorf(setups.CodeInvalid, "finishing the migration would write an invalid desktop %q: %v", desktop.ID, err)
		}
	}
	return outcome, nil
}
