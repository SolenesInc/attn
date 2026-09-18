package layouttree

import (
	"encoding/json"
	"slices"
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
	return Rebalance(normalized)
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

func Rebalance(node Node) Node {
	if node.Type != "split" || len(node.Children) < 2 {
		return node
	}

	firstChild := Rebalance(node.Children[0])
	secondChild := Rebalance(node.Children[1])
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

func ratioOrDefault(ratio float64) float64 {
	if ratio <= 0 || ratio >= 1 {
		return DefaultSplitRatio
	}
	return ratio
}

func directionOrDefault(direction Direction) Direction {
	if direction != DirectionVertical && direction != DirectionHorizontal {
		return DirectionVertical
	}
	return direction
}

func splitIDOrDefault(splitID string) string {
	if strings.TrimSpace(splitID) == "" {
		return "split"
	}
	return splitID
}

func leafIDOf(node Node) string {
	switch node.Type {
	case "pane":
		return node.PaneID
	case "tile":
		return node.TileID
	}
	return ""
}

func isLeaf(node Node) bool {
	return node.Type == "pane" || node.Type == "tile"
}

func walk(node Node, visit func(Node)) {
	visit(node)
	if node.Type != "split" {
		return
	}
	for _, child := range node.Children {
		walk(child, visit)
	}
}

func findNode(node Node, match func(Node) bool) (Node, bool) {
	if match(node) {
		return node, true
	}
	if node.Type != "split" {
		return Node{}, false
	}
	for _, child := range node.Children {
		if found, ok := findNode(child, match); ok {
			return found, true
		}
	}
	return Node{}, false
}

func rewriteFirst(node Node, rewrite func(Node) (Node, bool)) (Node, bool) {
	if next, ok := rewrite(node); ok {
		return next, true
	}
	if node.Type != "split" {
		return node, false
	}
	for i, child := range node.Children {
		next, ok := rewriteFirst(child, rewrite)
		if !ok {
			continue
		}
		children := slices.Clone(node.Children)
		children[i] = next
		node.Children = children
		return node, true
	}
	return node, false
}

func isPane(paneID string) func(Node) bool {
	return func(node Node) bool { return node.Type == "pane" && node.PaneID == paneID }
}

func isTile(tileID string) func(Node) bool {
	return func(node Node) bool { return node.Type == "tile" && node.TileID == tileID }
}

func isLeafWithID(id string) func(Node) bool {
	return func(node Node) bool { return isLeaf(node) && leafIDOf(node) == id }
}

func normalizeNode[P any](node Node, panesByID map[string]P) (Node, bool) {
	switch node.Type {
	case "pane":
		if _, ok := panesByID[node.PaneID]; !ok {
			return Node{}, true
		}
		return Node{Type: "pane", PaneID: node.PaneID}, false
	case "tile":
		return normalizeTile(node)
	case "split":
		return normalizeSplit(node, panesByID)
	}
	return Node{}, true
}

func normalizeTile(node Node) (Node, bool) {
	tileID := strings.TrimSpace(node.TileID)
	tileKind := strings.TrimSpace(node.TileKind)
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
}

func normalizeSplit[P any](node Node, panesByID map[string]P) (Node, bool) {
	children := make([]Node, 0, 2)
	for _, child := range node.Children {
		if next, empty := normalizeNode(child, panesByID); !empty {
			children = append(children, next)
		}
	}
	if len(children) < 2 {
		return collapsed(children)
	}
	ratioMode := RatioModeAutomatic
	if node.RatioMode == RatioModePreferred {
		ratioMode = RatioModePreferred
	}
	return Node{
		Type:        "split",
		SplitID:     splitIDOrDefault(strings.TrimSpace(node.SplitID)),
		Direction:   directionOrDefault(node.Direction),
		Ratio:       ratioOrDefault(node.Ratio),
		RatioLocked: node.RatioLocked,
		RatioMode:   ratioMode,
		Children:    children[:2],
	}, false
}

func collapsed(children []Node) (Node, bool) {
	if len(children) == 0 {
		return Node{}, true
	}
	return children[0], false
}

func Split(node Node, targetPaneID, newPaneID, splitID string, direction Direction, ratio float64) (Node, bool) {
	target := isPane(targetPaneID)
	return rewriteFirst(node, func(candidate Node) (Node, bool) {
		if !target(candidate) {
			return candidate, false
		}
		return Node{
			Type:      "split",
			SplitID:   splitID,
			Direction: directionOrDefault(direction),
			Ratio:     ratioOrDefault(ratio),
			RatioMode: RatioModeAutomatic,
			Children: []Node{
				{Type: "pane", PaneID: targetPaneID},
				{Type: "pane", PaneID: newPaneID},
			},
		}, true
	})
}

func SetSplitRatio(node Node, splitID string, ratio float64) (Node, bool) {
	const margin = 0.05
	ratio = min(max(ratio, margin), 1-margin)
	return rewriteFirst(node, func(candidate Node) (Node, bool) {
		if candidate.Type != "split" || len(candidate.Children) < 2 || candidate.SplitID != splitID {
			return candidate, false
		}
		candidate.Ratio = ratio
		candidate.RatioLocked = true
		candidate.RatioMode = RatioModePreferred
		return candidate, true
	})
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

func removeNode(node Node, id string) (Node, bool, bool) {
	if isLeaf(node) {
		if leafIDOf(node) == id {
			return Node{}, true, true
		}
		return node, false, false
	}
	if node.Type != "split" {
		return Node{}, false, true
	}
	children := make([]Node, 0, 2)
	removed := false
	for _, child := range node.Children {
		next, childRemoved, empty := removeNode(child, id)
		removed = removed || childRemoved
		if !empty {
			children = append(children, next)
		}
	}
	if len(children) < 2 {
		survivor, empty := collapsed(children)
		return survivor, removed, empty
	}
	node.Children = children[:2]
	return node, removed, false
}

func HasPane(node Node, paneID string) bool {
	_, found := findNode(node, isPane(paneID))
	return found
}

func PaneIDs(node Node) []string {
	var ids []string
	walk(node, func(visited Node) {
		if visited.Type == "pane" {
			ids = append(ids, visited.PaneID)
		}
	})
	return ids
}

func HasTile(node Node, tileID string) bool {
	_, found := findNode(node, isTile(tileID))
	return found
}

func TileIDs(node Node) []string {
	var ids []string
	walk(node, func(visited Node) {
		if visited.Type == "tile" {
			ids = append(ids, visited.TileID)
		}
	})
	return ids
}

func hasLeaf(node Node, leafID string) bool {
	return HasPane(node, leafID) || HasTile(node, leafID)
}

// Run LayoutEmpty on a normalized layout.
func LayoutEmpty(node Node) bool {
	return len(PaneIDs(node)) == 0 && len(TileIDs(node)) == 0
}

func findLeaf(node Node, id string) (Node, bool) {
	return findNode(node, isLeafWithID(id))
}

func TileParamsByID(node Node, tileID string) (string, bool) {
	tile, found := findNode(node, isTile(tileID))
	return tile.TileParams, found
}

func TileSessionIDByID(node Node, tileID string) (string, bool) {
	tile, found := findNode(node, isTile(tileID))
	return tile.TileSessionID, found
}

func updateTile(node Node, tileID string, update func(*Node)) (Node, bool) {
	target := isTile(tileID)
	return rewriteFirst(node, func(candidate Node) (Node, bool) {
		if !target(candidate) {
			return candidate, false
		}
		update(&candidate)
		return candidate, true
	})
}

func UpdateTileSessionID(node Node, tileID, sessionID string) (Node, bool) {
	return updateTile(node, tileID, func(tile *Node) { tile.TileSessionID = strings.TrimSpace(sessionID) })
}

func UpdateTileParams(node Node, tileID, tileParams string) (Node, bool) {
	return updateTile(node, tileID, func(tile *Node) { tile.TileParams = strings.TrimSpace(tileParams) })
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
	walk(node, func(visited Node) {
		if visited.Type != "tile" {
			return
		}
		leaves = append(leaves, TileLeaf{
			TileID:        visited.TileID,
			TileKind:      visited.TileKind,
			TileParams:    visited.TileParams,
			TileSessionID: visited.TileSessionID,
		})
	})
	return leaves
}

// `ratio` is the children[0] fraction. An empty tileSessionID carries any
// existing binding forward, so moving a tile never silently drops its session.
func DockTile(node Node, anchorID string, direction Direction, before bool, splitID, tileID, tileKind, tileParams, tileSessionID string, ratio float64) (Node, bool) {
	tileID = strings.TrimSpace(tileID)
	tileKind = strings.TrimSpace(tileKind)
	anchorID = strings.TrimSpace(anchorID)
	tileSessionID = strings.TrimSpace(tileSessionID)
	if tileID == "" || tileKind == "" || anchorID == "" || anchorID == tileID || HasPane(node, tileID) {
		return node, false
	}
	if existing, ok := findLeaf(node, tileID); ok && tileSessionID == "" {
		tileSessionID = existing.TileSessionID
	}
	cleaned := node
	if next, removed := Remove(node, tileID); removed {
		cleaned = next
	}
	tile := Node{Type: "tile", TileID: tileID, TileKind: tileKind, TileParams: strings.TrimSpace(tileParams), TileSessionID: tileSessionID}
	next, ok := insertBesideLeaf(cleaned, anchorID, newPlacement(direction, before, splitID, ratio), tile)
	if !ok {
		return node, false
	}
	return next, true
}

type placement struct {
	direction Direction
	before    bool
	splitID   string
	ratio     float64
}

func newPlacement(direction Direction, before bool, splitID string, ratio float64) placement {
	return placement{
		direction: directionOrDefault(direction),
		before:    before,
		splitID:   splitIDOrDefault(splitID),
		ratio:     ratioOrDefault(ratio),
	}
}

func insertBesideLeaf(node Node, anchorID string, at placement, incoming Node) (Node, bool) {
	anchor := isLeafWithID(anchorID)
	return rewriteFirst(node, func(candidate Node) (Node, bool) {
		if !anchor(candidate) {
			return candidate, false
		}
		return lockedSplit(candidate, incoming, at), true
	})
}

func placeLeaf(layout Node, anchorID string, at placement, incoming Node) (Node, bool) {
	if anchorID == "" {
		return lockedSplit(layout, incoming, at), true
	}
	return insertBesideLeaf(layout, anchorID, at, incoming)
}

func lockedSplit(existing, incoming Node, at placement) Node {
	children := []Node{existing, incoming}
	if at.before {
		children = []Node{incoming, existing}
	}
	return Node{
		Type:        "split",
		SplitID:     at.splitID,
		Direction:   at.direction,
		Ratio:       at.ratio,
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
	cleaned, _ := Remove(node, leafID)
	if cleaned.Type == "" {
		return node, false
	}
	next, ok := placeLeaf(cleaned, anchorID, newPlacement(direction, before, splitID, ratio), moved)
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
	moved, found := findLeaf(source, leafID)
	if leafID == "" || !found {
		return MoveBetweenLayoutsResult{}, false
	}
	cleanedSource, _ := Remove(source, leafID)

	moved = renamedToAvoid(target, moved, conflictSuffix)
	if hasLeaf(target, leafIDOf(moved)) {
		return MoveBetweenLayoutsResult{}, false
	}

	nextTarget := moved
	if !LayoutEmpty(target) {
		placed, ok := placeLeaf(target, strings.TrimSpace(anchorID), newPlacement(direction, before, splitID, ratio), moved)
		if !ok {
			return MoveBetweenLayoutsResult{}, false
		}
		nextTarget = placed
	}
	return MoveBetweenLayoutsResult{
		SourceLayout: cleanedSource,
		TargetLayout: nextTarget,
		Leaf:         moved,
		FinalLeafID:  leafIDOf(moved),
	}, true
}

func renamedToAvoid(target, leaf Node, conflictSuffix string) Node {
	if !hasLeaf(target, leafIDOf(leaf)) {
		return leaf
	}
	suffix := strings.TrimSpace(conflictSuffix)
	if suffix == "" {
		suffix = "moved"
	}
	renamed := leafIDOf(leaf) + "-" + suffix
	if leaf.Type == "pane" {
		leaf.PaneID = renamed
	} else {
		leaf.TileID = renamed
	}
	return leaf
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

func claimNodeID(seen map[string]string, path, id, what string) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(id) != id {
		return &ValidationError{Path: path, Reason: what + " id is empty or padded"}
	}
	if previous, ok := seen[id]; ok {
		return &ValidationError{Path: path, Reason: what + " id " + id + " is already used at " + previous}
	}
	seen[id] = path
	return nil
}

func validateSplit(node Node, path string, seen map[string]string) error {
	if err := claimNodeID(seen, path, node.SplitID, "split"); err != nil {
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
}

func validateNode(node Node, path string, seen map[string]string) error {
	switch node.Type {
	case "pane":
		return claimNodeID(seen, path, node.PaneID, "pane")
	case "tile":
		if strings.TrimSpace(node.TileKind) == "" {
			return &ValidationError{Path: path, Reason: "tile " + node.TileID + " has no kind"}
		}
		return claimNodeID(seen, path, node.TileID, "tile")
	case "split":
		return validateSplit(node, path, seen)
	default:
		return &ValidationError{Path: path, Reason: "unknown node type " + node.Type}
	}
}
