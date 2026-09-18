package layouttree

import (
	"encoding/json"
	"sort"
	"strings"
)

const (
	DefaultSplitRatio = 0.5
)

type Direction string

const (
	DirectionVertical   Direction = "vertical"
	DirectionHorizontal Direction = "horizontal"
)

type RatioMode string

const (
	RatioModeAutomatic RatioMode = "automatic"
	RatioModePreferred RatioMode = "preferred"
)

type TileKind string

const (
	TileKindMarkdown TileKind = "markdown"
	TileKindBrowser  TileKind = "browser"
	TileKindSeed     TileKind = "seed"
	TileKindNotebook TileKind = "notebook"
)

type Node struct {
	Type          string    `json:"type"`
	PaneID        string    `json:"pane_id,omitempty"`
	TileID        string    `json:"tile_id,omitempty"`
	TileKind      string    `json:"tile_kind,omitempty"`
	TileParams    string    `json:"tile_params,omitempty"`
	TileSessionID string    `json:"tile_session_id,omitempty"`
	SplitID       string    `json:"split_id,omitempty"`
	Direction     Direction `json:"direction,omitempty"`
	Ratio         float64   `json:"ratio,omitempty"`
	// RatioLocked survives normalization instead of being rebalanced to an equal split.
	RatioLocked bool      `json:"ratio_locked,omitempty"`
	RatioMode   RatioMode `json:"ratio_mode,omitempty"`
	Children    []Node    `json:"children,omitempty"`
}

func DefaultLayout(paneID string) Node {
	return Node{
		Type:   "pane",
		PaneID: paneID,
	}
}

func EncodeLayout(node Node) (string, error) {
	data, err := json.Marshal(node)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func DecodeLayout(layoutJSON string) (Node, error) {
	if strings.TrimSpace(layoutJSON) == "" {
		return Node{}, nil
	}
	var node Node
	if err := json.Unmarshal([]byte(layoutJSON), &node); err != nil {
		return Node{}, err
	}
	return node, nil
}

func NormalizeLayout[P any](node Node, panesByID map[string]P) Node {
	normalized, empty := normalizeNode(node, panesByID)
	if empty {
		return fallbackLayout(panesByID)
	}
	return rebalanceSplitChains(normalized)
}

func fallbackLayout[P any](panesByID map[string]P) Node {
	paneIDs := make([]string, 0, len(panesByID))
	for paneID := range panesByID {
		paneIDs = append(paneIDs, paneID)
	}
	sort.Strings(paneIDs)
	if len(paneIDs) == 0 {
		return Node{}
	}
	return DefaultLayout(paneIDs[0])
}

func rebalanceSplitChains(node Node) Node {
	if node.Type != "split" || len(node.Children) < 2 {
		return node
	}

	firstChild := rebalanceSplitChains(node.Children[0])
	secondChild := rebalanceSplitChains(node.Children[1])
	node.Children = []Node{firstChild, secondChild}

	if node.RatioLocked {
		return node
	}

	firstSpan := splitChainSpanCount(firstChild, node.Direction)
	secondSpan := splitChainSpanCount(secondChild, node.Direction)
	if totalSpan := firstSpan + secondSpan; totalSpan > 0 {
		node.Ratio = float64(firstSpan) / float64(totalSpan)
	}
	return node
}

func splitChainSpanCount(node Node, direction Direction) int {
	// A locked split is an opaque unit: an enclosing chain must not redistribute
	// space through it.
	if node.Type != "split" || node.Direction != direction || len(node.Children) < 2 || node.RatioLocked {
		return 1
	}
	return splitChainSpanCount(node.Children[0], direction) + splitChainSpanCount(node.Children[1], direction)
}

func normalizeNode[P any](node Node, panesByID map[string]P) (Node, bool) {
	switch node.Type {
	case "pane":
		if _, ok := panesByID[node.PaneID]; !ok {
			return Node{}, true
		}
		return Node{
			Type:   "pane",
			PaneID: node.PaneID,
		}, false
	case "tile":
		tileID := strings.TrimSpace(node.TileID)
		tileKind := strings.TrimSpace(node.TileKind)
		// Drop an identity-less tile so it cannot wedge the layout. Tiles are
		// otherwise never pruned by pane bookkeeping: they have no panesByID entry.
		if tileID == "" || tileKind == "" {
			return Node{}, true
		}
		return Node{
			Type:          "tile",
			TileID:        tileID,
			TileKind:      tileKind,
			TileParams:    node.TileParams,
			TileSessionID: strings.TrimSpace(node.TileSessionID),
		}, false
	case "split":
		children := make([]Node, 0, 2)
		for _, child := range node.Children {
			next, empty := normalizeNode(child, panesByID)
			if !empty {
				children = append(children, next)
			}
		}
		switch len(children) {
		case 0:
			return Node{}, true
		case 1:
			return children[0], false
		default:
			direction := node.Direction
			if direction != DirectionVertical && direction != DirectionHorizontal {
				direction = DirectionVertical
			}
			ratio := node.Ratio
			if ratio <= 0 || ratio >= 1 {
				ratio = DefaultSplitRatio
			}
			ratioMode := RatioModeAutomatic
			if node.RatioMode == RatioModePreferred {
				ratioMode = RatioModePreferred
			}
			splitID := strings.TrimSpace(node.SplitID)
			if splitID == "" {
				splitID = "split"
			}
			return Node{
				Type:        "split",
				SplitID:     splitID,
				Direction:   direction,
				Ratio:       ratio,
				RatioLocked: node.RatioLocked,
				RatioMode:   ratioMode,
				Children:    children[:2],
			}, false
		}
	default:
		return Node{}, true
	}
}

func Split(node Node, targetPaneID, newPaneID, splitID string, direction Direction, ratio float64) (Node, bool) {
	if ratio <= 0 || ratio >= 1 {
		ratio = DefaultSplitRatio
	}
	if direction != DirectionVertical && direction != DirectionHorizontal {
		direction = DirectionVertical
	}

	switch node.Type {
	case "pane":
		if node.PaneID != targetPaneID {
			return node, false
		}
		return Node{
			Type:      "split",
			SplitID:   splitID,
			Direction: direction,
			Ratio:     ratio,
			RatioMode: RatioModeAutomatic,
			Children: []Node{
				{Type: "pane", PaneID: targetPaneID},
				{Type: "pane", PaneID: newPaneID},
			},
		}, true
	case "split":
		children := make([]Node, len(node.Children))
		copy(children, node.Children)
		for i, child := range children {
			next, changed := Split(child, targetPaneID, newPaneID, splitID, direction, ratio)
			if changed {
				children[i] = next
				node.Children = children
				return node, true
			}
		}
	}
	return node, false
}

func SetSplitRatio(node Node, splitID string, ratio float64) (Node, bool) {
	const margin = 0.05
	if ratio < margin {
		ratio = margin
	} else if ratio > 1-margin {
		ratio = 1 - margin
	}
	if node.Type != "split" || len(node.Children) < 2 {
		return node, false
	}
	if node.SplitID == splitID {
		node.Ratio = ratio
		node.RatioLocked = true
		node.RatioMode = RatioModePreferred
		return node, true
	}
	children := make([]Node, len(node.Children))
	copy(children, node.Children)
	for i, child := range children {
		next, changed := SetSplitRatio(child, splitID, ratio)
		if changed {
			children[i] = next
			node.Children = children
			return node, true
		}
	}
	return node, false
}

func Remove(node Node, paneID string) (Node, bool) {
	next, removed, empty := removeNode(node, paneID)
	if !removed {
		return node, false
	}
	if empty {
		return Node{}, true
	}
	return next, true
}

func removeNode(node Node, leafID string) (Node, bool, bool) {
	switch node.Type {
	case "pane":
		if node.PaneID == leafID {
			return Node{}, true, true
		}
		return node, false, false
	case "tile":
		if node.TileID == leafID {
			return Node{}, true, true
		}
		return node, false, false
	case "split":
		children := make([]Node, 0, 2)
		removed := false
		for _, child := range node.Children {
			next, childRemoved, empty := removeNode(child, leafID)
			removed = removed || childRemoved
			if !empty {
				children = append(children, next)
			}
		}
		switch len(children) {
		case 0:
			return Node{}, removed, true
		case 1:
			return children[0], removed, false
		default:
			node.Children = children[:2]
			return node, removed, false
		}
	default:
		return Node{}, false, true
	}
}

func HasPane(node Node, paneID string) bool {
	switch node.Type {
	case "pane":
		return node.PaneID == paneID
	case "split":
		for _, child := range node.Children {
			if HasPane(child, paneID) {
				return true
			}
		}
	}
	return false
}

func PaneIDs(node Node) []string {
	var ids []string
	collectPaneIDs(node, &ids)
	return ids
}

func collectPaneIDs(node Node, ids *[]string) {
	switch node.Type {
	case "pane":
		*ids = append(*ids, node.PaneID)
	case "split":
		for _, child := range node.Children {
			collectPaneIDs(child, ids)
		}
	}
}

func HasTile(node Node, tileID string) bool {
	switch node.Type {
	case "tile":
		return node.TileID == tileID
	case "split":
		for _, child := range node.Children {
			if HasTile(child, tileID) {
				return true
			}
		}
	}
	return false
}

func TileIDs(node Node) []string {
	var ids []string
	collectTileIDs(node, &ids)
	return ids
}

func collectTileIDs(node Node, ids *[]string) {
	switch node.Type {
	case "tile":
		*ids = append(*ids, node.TileID)
	case "split":
		for _, child := range node.Children {
			collectTileIDs(child, ids)
		}
	}
}

func hasLeaf(node Node, leafID string) bool {
	return HasPane(node, leafID) || HasTile(node, leafID)
}

// Run LayoutEmpty on a normalized layout.
func LayoutEmpty(node Node) bool {
	return len(PaneIDs(node)) == 0 && len(TileIDs(node)) == 0
}

func findLeaf(node Node, leafID string) (Node, bool) {
	switch node.Type {
	case "pane":
		if node.PaneID == leafID {
			return node, true
		}
	case "tile":
		if node.TileID == leafID {
			return node, true
		}
	case "split":
		for _, child := range node.Children {
			if leaf, ok := findLeaf(child, leafID); ok {
				return leaf, true
			}
		}
	}
	return Node{}, false
}

func TileParamsByID(node Node, tileID string) (string, bool) {
	switch node.Type {
	case "tile":
		if node.TileID == tileID {
			return node.TileParams, true
		}
	case "split":
		for _, child := range node.Children {
			if params, ok := TileParamsByID(child, tileID); ok {
				return params, true
			}
		}
	}
	return "", false
}

func TileSessionIDByID(node Node, tileID string) (string, bool) {
	switch node.Type {
	case "tile":
		if node.TileID == tileID {
			return node.TileSessionID, true
		}
	case "split":
		for _, child := range node.Children {
			if sessionID, ok := TileSessionIDByID(child, tileID); ok {
				return sessionID, true
			}
		}
	}
	return "", false
}

func UpdateTileSessionID(node Node, tileID, sessionID string) (Node, bool) {
	switch node.Type {
	case "tile":
		if node.TileID != tileID {
			return node, false
		}
		node.TileSessionID = strings.TrimSpace(sessionID)
		return node, true
	case "split":
		children := make([]Node, len(node.Children))
		copy(children, node.Children)
		for index, child := range children {
			updated, ok := UpdateTileSessionID(child, tileID, sessionID)
			if !ok {
				continue
			}
			children[index] = updated
			node.Children = children
			return node, true
		}
	}
	return node, false
}

func UpdateTileParams(node Node, tileID, tileParams string) (Node, bool) {
	switch node.Type {
	case "tile":
		if node.TileID != tileID {
			return node, false
		}
		node.TileParams = strings.TrimSpace(tileParams)
		return node, true
	case "split":
		children := make([]Node, len(node.Children))
		copy(children, node.Children)
		for index, child := range children {
			updated, ok := UpdateTileParams(child, tileID, tileParams)
			if !ok {
				continue
			}
			children[index] = updated
			node.Children = children
			return node, true
		}
	}
	return node, false
}

func TileFractionByID(node Node, tileID string) (float64, bool) {
	if node.Type != "split" {
		return 0, false
	}
	if len(node.Children) == 2 {
		if node.Children[0].Type == "tile" && node.Children[0].TileID == tileID {
			return node.Ratio, true
		}
		if node.Children[1].Type == "tile" && node.Children[1].TileID == tileID {
			return 1 - node.Ratio, true
		}
	}
	for _, child := range node.Children {
		if fraction, ok := TileFractionByID(child, tileID); ok {
			return fraction, true
		}
	}
	return 0, false
}

type TileLeaf struct {
	TileID        string
	TileKind      string
	TileParams    string
	TileSessionID string
}

func TileLeaves(node Node) []TileLeaf {
	var leaves []TileLeaf
	collectTileLeaves(node, &leaves)
	return leaves
}

func collectTileLeaves(node Node, leaves *[]TileLeaf) {
	switch node.Type {
	case "tile":
		*leaves = append(*leaves, TileLeaf{
			TileID:        node.TileID,
			TileKind:      node.TileKind,
			TileParams:    node.TileParams,
			TileSessionID: node.TileSessionID,
		})
	case "split":
		for _, child := range node.Children {
			collectTileLeaves(child, leaves)
		}
	}
}

// `ratio` is the children[0] fraction. An empty tileSessionID carries any
// existing binding forward, so moving a tile never silently drops its session.
func DockTile(node Node, anchorID string, direction Direction, before bool, splitID, tileID, tileKind, tileParams, tileSessionID string, ratio float64) (Node, bool) {
	tileID = strings.TrimSpace(tileID)
	tileKind = strings.TrimSpace(tileKind)
	anchorID = strings.TrimSpace(anchorID)
	tileSessionID = strings.TrimSpace(tileSessionID)
	if tileID == "" || tileKind == "" || anchorID == "" || anchorID == tileID {
		return node, false
	}
	if ratio <= 0 || ratio >= 1 {
		ratio = DefaultSplitRatio
	}
	if direction != DirectionVertical && direction != DirectionHorizontal {
		direction = DirectionVertical
	}
	if strings.TrimSpace(splitID) == "" {
		splitID = "split"
	}
	if HasPane(node, tileID) {
		return node, false
	}

	if tileSessionID == "" {
		if existing, ok := findLeaf(node, tileID); ok {
			tileSessionID = existing.TileSessionID
		}
	}

	cleaned := node
	if next, removed := Remove(node, tileID); removed {
		cleaned = next
	}
	if cleaned.Type == "" || !hasLeaf(cleaned, anchorID) {
		return node, false
	}

	tile := Node{Type: "tile", TileID: tileID, TileKind: tileKind, TileParams: strings.TrimSpace(tileParams), TileSessionID: tileSessionID}
	next, ok := insertBesideLeaf(cleaned, anchorID, direction, before, splitID, ratio, tile)
	if !ok {
		return node, false
	}
	return next, true
}

func insertBesideLeaf(node Node, anchorID string, direction Direction, before bool, splitID string, ratio float64, tile Node) (Node, bool) {
	switch node.Type {
	case "pane":
		if node.PaneID != anchorID {
			return node, false
		}
		return lockedSplit(node, tile, direction, before, splitID, ratio), true
	case "tile":
		if node.TileID != anchorID {
			return node, false
		}
		return lockedSplit(node, tile, direction, before, splitID, ratio), true
	case "split":
		children := make([]Node, len(node.Children))
		copy(children, node.Children)
		for i, child := range children {
			next, changed := insertBesideLeaf(child, anchorID, direction, before, splitID, ratio, tile)
			if changed {
				children[i] = next
				node.Children = children
				return node, true
			}
		}
	}
	return node, false
}

func lockedSplit(existing, incoming Node, direction Direction, before bool, splitID string, ratio float64) Node {
	children := []Node{existing, incoming}
	if before {
		children = []Node{incoming, existing}
	}
	return Node{
		Type:        "split",
		SplitID:     splitID,
		Direction:   direction,
		Ratio:       ratio,
		RatioLocked: true,
		RatioMode:   RatioModeAutomatic,
		Children:    children,
	}
}

func MoveLeaf(node Node, leafID, anchorID, splitID string, direction Direction, before bool, ratio float64) (Node, bool) {
	leafID = strings.TrimSpace(leafID)
	anchorID = strings.TrimSpace(anchorID)
	if leafID == "" || leafID == anchorID {
		return node, false
	}
	moved, found := findLeaf(node, leafID)
	if !found {
		return node, false
	}
	if ratio <= 0 || ratio >= 1 {
		ratio = DefaultSplitRatio
	}
	if direction != DirectionVertical && direction != DirectionHorizontal {
		direction = DirectionVertical
	}
	if strings.TrimSpace(splitID) == "" {
		splitID = "split"
	}

	cleaned, removed := Remove(node, leafID)
	if !removed || cleaned.Type == "" {
		return node, false
	}

	if anchorID == "" {
		return lockedSplit(cleaned, moved, direction, before, splitID, ratio), true
	}
	if !hasLeaf(cleaned, anchorID) {
		return node, false
	}
	next, ok := insertBesideLeaf(cleaned, anchorID, direction, before, splitID, ratio, moved)
	if !ok {
		return node, false
	}
	return next, true
}

type MoveBetweenLayoutsResult struct {
	SourceLayout Node
	TargetLayout Node
	Leaf         Node
	FinalLeafID  string
}

func MoveLeafBetweenLayouts(source, target Node, leafID, anchorID, splitID string, direction Direction, before bool, ratio float64, conflictSuffix string) (MoveBetweenLayoutsResult, bool) {
	leafID = strings.TrimSpace(leafID)
	anchorID = strings.TrimSpace(anchorID)
	if leafID == "" {
		return MoveBetweenLayoutsResult{}, false
	}
	moved, found := findLeaf(source, leafID)
	if !found {
		return MoveBetweenLayoutsResult{}, false
	}
	if ratio <= 0 || ratio >= 1 {
		ratio = DefaultSplitRatio
	}
	if direction != DirectionVertical && direction != DirectionHorizontal {
		direction = DirectionVertical
	}
	if strings.TrimSpace(splitID) == "" {
		splitID = "split"
	}

	cleanedSource, removed := Remove(source, leafID)
	if !removed {
		return MoveBetweenLayoutsResult{}, false
	}

	finalLeafID := leafID
	if hasLeaf(target, finalLeafID) {
		suffix := strings.TrimSpace(conflictSuffix)
		if suffix == "" {
			suffix = "moved"
		}
		finalLeafID = finalLeafID + "-" + suffix
		if moved.Type == "pane" {
			moved.PaneID = finalLeafID
		} else if moved.Type == "tile" {
			moved.TileID = finalLeafID
		}
	}
	if hasLeaf(target, finalLeafID) {
		return MoveBetweenLayoutsResult{}, false
	}

	var nextTarget Node
	if target.Type == "" || LayoutEmpty(target) {
		nextTarget = moved
	} else if anchorID == "" {
		nextTarget = lockedSplit(target, moved, direction, before, splitID, ratio)
	} else {
		if !hasLeaf(target, anchorID) {
			return MoveBetweenLayoutsResult{}, false
		}
		inserted, ok := insertBesideLeaf(target, anchorID, direction, before, splitID, ratio, moved)
		if !ok {
			return MoveBetweenLayoutsResult{}, false
		}
		nextTarget = inserted
	}

	return MoveBetweenLayoutsResult{
		SourceLayout: cleanedSource,
		TargetLayout: nextTarget,
		Leaf:         moved,
		FinalLeafID:  finalLeafID,
	}, true
}

func UndockTile(node Node, tileID string) (Node, bool) {
	if !HasTile(node, strings.TrimSpace(tileID)) {
		return node, false
	}
	next, _ := Remove(node, strings.TrimSpace(tileID))
	return next, true
}

type ValidationError struct {
	Path   string
	Reason string
}

func (e *ValidationError) Error() string {
	return "layout tree invalid at " + e.Path + ": " + e.Reason
}

func Validate(node Node) error {
	if LayoutEmpty(node) && node.Type == "" {
		return nil
	}
	seen := make(map[string]string)
	return validateNode(node, "root", seen)
}

func validateNode(node Node, path string, seen map[string]string) error {
	claim := func(id, what string) error {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(id) != id {
			return &ValidationError{Path: path, Reason: what + " id is empty or padded"}
		}
		if previous, ok := seen[id]; ok {
			return &ValidationError{Path: path, Reason: what + " id " + id + " is already used at " + previous}
		}
		seen[id] = path
		return nil
	}
	switch node.Type {
	case "pane":
		return claim(node.PaneID, "pane")
	case "tile":
		if strings.TrimSpace(node.TileKind) == "" {
			return &ValidationError{Path: path, Reason: "tile " + node.TileID + " has no kind"}
		}
		return claim(node.TileID, "tile")
	case "split":
		if err := claim(node.SplitID, "split"); err != nil {
			return err
		}
		if node.Direction != DirectionVertical && node.Direction != DirectionHorizontal {
			return &ValidationError{Path: path, Reason: "split " + node.SplitID + " has direction " + string(node.Direction)}
		}
		if !(node.Ratio > 0 && node.Ratio < 1) {
			return &ValidationError{Path: path, Reason: "split " + node.SplitID + " has a ratio outside (0,1)"}
		}
		if len(node.Children) != 2 {
			return &ValidationError{Path: path, Reason: "split " + node.SplitID + " does not have exactly two children"}
		}
		for i, child := range node.Children {
			if err := validateNode(child, path+"/"+node.SplitID+"["+string(rune('0'+i))+"]", seen); err != nil {
				return err
			}
		}
		return nil
	default:
		return &ValidationError{Path: path, Reason: "unknown node type " + node.Type}
	}
}
