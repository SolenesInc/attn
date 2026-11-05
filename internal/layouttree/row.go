package layouttree

import "slices"

func Columns(tree Node) []Node {
	if tree.Type == "" {
		return nil
	}
	if tree.Type == "split" && tree.Direction == DirectionVertical && len(tree.Children) == 2 {
		return append(Columns(tree.Children[0]), Columns(tree.Children[1])...)
	}
	return []Node{tree}
}

func FromColumns(columns []Node, newSplitID func() string) Node {
	if len(columns) == 0 {
		return Node{}
	}
	tree := columns[len(columns)-1]
	for i := len(columns) - 2; i >= 0; i-- {
		tree = Node{
			Type:      "split",
			SplitID:   newSplitID(),
			Direction: DirectionVertical,
			Ratio:     1 / float64(len(columns)-i),
			RatioMode: RatioModeAutomatic,
			Children:  []Node{columns[i], tree},
		}
	}
	return tree
}

func ColumnOf(tree Node, leafID string) (index int, ok bool) {
	for i, column := range Columns(tree) {
		if _, found := findLeaf(column, leafID); found {
			return i, true
		}
	}
	return 0, false
}

func NextInPosition(previous Node, leafID string, live map[string]bool, history []string) string {
	at, ok := ColumnOf(previous, leafID)
	if !ok {
		return ""
	}
	columns := Columns(previous)
	stack := LeafIDs(columns[at])
	position := slices.Index(stack, leafID)
	for _, id := range stack[position+1:] {
		if live[id] {
			return id
		}
	}
	for _, id := range slices.Backward(stack[:position]) {
		if live[id] {
			return id
		}
	}
	for _, column := range columns[at+1:] {
		if id := mostRecentlyUsedLeaf(column, live, history); id != "" {
			return id
		}
	}
	for _, column := range slices.Backward(columns[:at]) {
		if id := mostRecentlyUsedLeaf(column, live, history); id != "" {
			return id
		}
	}
	return ""
}

func mostRecentlyUsedLeaf(column Node, live map[string]bool, history []string) string {
	leaves := slices.DeleteFunc(LeafIDs(column), func(id string) bool { return !live[id] })
	for _, id := range history {
		if slices.Contains(leaves, id) {
			return id
		}
	}
	if len(leaves) > 0 {
		return leaves[0]
	}
	return ""
}

func SetWantedCols(tree Node, leafID string, cols int) (Node, bool) {
	at, ok := ColumnOf(tree, leafID)
	if !ok {
		return tree, false
	}
	stack := LeafIDs(Columns(tree)[at])
	for _, id := range stack {
		wanted := 0
		if id == stack[0] {
			wanted = cols
		}
		target := isLeafWithID(id)
		tree, _ = rewriteFirst(tree, func(candidate Node) (Node, bool) {
			if !target(candidate) {
				return candidate, false
			}
			candidate.WantedCols = wanted
			return candidate, true
		})
	}
	return tree, true
}

func PlaceColumn(tree Node, anchorLeafID string, before bool, column Node, newSplitID func() string) (Node, bool) {
	columns := Columns(tree)
	at := len(columns)
	if before {
		at = 0
	}
	if anchorLeafID != "" {
		anchor, ok := ColumnOf(tree, anchorLeafID)
		if !ok {
			return tree, false
		}
		at = anchor
		if !before {
			at++
		}
	}
	return FromColumns(slices.Insert(columns, at, column), newSplitID), true
}

type RowMove struct {
	LeafID      string
	AnchorID    string
	WholeColumn bool
	Direction   Direction
	Before      bool
	Ratio       float64
	NewSplitID  func() string
}

func (m RowMove) take(tree Node) (rest, moved Node, ok bool) {
	if !m.WholeColumn {
		moved, ok = findLeaf(tree, m.LeafID)
		if !ok {
			return tree, Node{}, false
		}
		rest, _ = Remove(tree, m.LeafID)
		return rest, moved, true
	}
	at, ok := ColumnOf(tree, m.LeafID)
	if !ok {
		return tree, Node{}, false
	}
	moved = Columns(tree)[at]
	rest = tree
	for _, id := range LeafIDs(moved) {
		rest, _ = Remove(rest, id)
	}
	return rest, moved, true
}

func (m RowMove) place(tree, moved Node) (Node, bool) {
	if m.Direction == DirectionHorizontal && m.AnchorID != "" {
		return insertBesideLeaf(tree, m.AnchorID, newPlacement(m.Direction, m.Before, m.NewSplitID(), m.Ratio), moved)
	}
	return PlaceColumn(tree, m.AnchorID, m.Before, moved, m.NewSplitID)
}

func MoveWithinRow(tree Node, m RowMove) (Node, bool) {
	if !m.WholeColumn && m.LeafID == m.AnchorID {
		return tree, false
	}
	rest, moved, ok := m.take(tree)
	if !ok {
		return tree, false
	}
	next, ok := m.place(rest, moved)
	if !ok {
		return tree, false
	}
	return next, true
}

type MovedLeaf struct {
	From string
	To   string
}

type RowMoveBetweenLayouts struct {
	SourceLayout Node
	TargetLayout Node
	Moved        []MovedLeaf
}

func MoveBetweenRows(source, target Node, m RowMove, conflictSuffix string) (RowMoveBetweenLayouts, bool) {
	rest, moved, ok := m.take(source)
	if !ok {
		return RowMoveBetweenLayouts{}, false
	}
	var leaves []MovedLeaf
	moved = renamedSubtreeToAvoid(target, moved, conflictSuffix, &leaves)
	next, ok := m.place(target, moved)
	if !ok {
		return RowMoveBetweenLayouts{}, false
	}
	return RowMoveBetweenLayouts{SourceLayout: rest, TargetLayout: next, Moved: leaves}, true
}

func renamedSubtreeToAvoid(target, node Node, conflictSuffix string, leaves *[]MovedLeaf) Node {
	if isLeaf(node) {
		renamed := renamedToAvoid(target, node, conflictSuffix)
		*leaves = append(*leaves, MovedLeaf{From: leafIDOf(node), To: leafIDOf(renamed)})
		return renamed
	}
	children := slices.Clone(node.Children)
	for i, child := range children {
		children[i] = renamedSubtreeToAvoid(target, child, conflictSuffix, leaves)
	}
	node.Children = children
	return node
}
