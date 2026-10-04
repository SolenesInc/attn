package layouttree

import "math"

type leafBounds struct {
	id                       string
	left, top, right, bottom float64
}

func LeftNeighbour(tree Node, leafID string, surviving map[string]bool) string {
	var leaves []leafBounds
	collectLeafBounds(tree, leafBounds{right: 1, bottom: 1}, &leaves)
	var current leafBounds
	found := false
	for _, leaf := range leaves {
		if leaf.id == leafID {
			current, found = leaf, true
			break
		}
	}
	if !found {
		return ""
	}
	bestID := ""
	bestGap, bestAlignment := math.Inf(1), math.Inf(1)
	for _, candidate := range leaves {
		if !surviving[candidate.id] || candidate.id == leafID {
			continue
		}
		gap := current.left - candidate.right
		if gap < 0 || math.Min(current.bottom, candidate.bottom) <= math.Max(current.top, candidate.top) {
			continue
		}
		alignment := math.Abs((current.top + current.bottom) - (candidate.top + candidate.bottom))
		if gap < bestGap || (gap == bestGap && (alignment < bestAlignment || (alignment == bestAlignment && candidate.id < bestID))) {
			bestID, bestGap, bestAlignment = candidate.id, gap, alignment
		}
	}
	return bestID
}

func collectLeafBounds(tree Node, bounds leafBounds, leaves *[]leafBounds) {
	if isLeaf(tree) {
		bounds.id = leafIDOf(tree)
		*leaves = append(*leaves, bounds)
		return
	}
	if tree.Type != "split" {
		return
	}
	first, second := bounds, bounds
	if tree.Direction == DirectionVertical {
		first.right = bounds.left + (bounds.right-bounds.left)*tree.Ratio
		second.left = first.right
	} else {
		first.bottom = bounds.top + (bounds.bottom-bounds.top)*tree.Ratio
		second.top = first.bottom
	}
	collectLeafBounds(tree.Children[0], first, leaves)
	collectLeafBounds(tree.Children[1], second, leaves)
}
