package setupmigration

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/setups"
)

type Node struct {
	Group     string               `json:"group,omitempty"`
	Direction layouttree.Direction `json:"direction,omitempty"`
	Ratio     float64              `json:"ratio,omitempty"`
	Children  []Node               `json:"children,omitempty"`
}

type Desktop struct {
	Key          string `json:"key"`
	DesktopID    string `json:"desktop_id,omitempty"`
	ShortcutSlot int    `json:"shortcut_slot,omitempty"`
	Tree         *Node  `json:"tree,omitempty"`
}

type Arrangement struct {
	Desktops  []Desktop `json:"desktops"`
	Confirmed []string  `json:"confirmed"`
	Suggested bool      `json:"suggested,omitempty"`
}

type Plan struct {
	Arrangement
	History []Arrangement `json:"history,omitempty"`
}

type Edge string

const (
	EdgeLeft   Edge = "left"
	EdgeRight  Edge = "right"
	EdgeTop    Edge = "top"
	EdgeBottom Edge = "bottom"
)

func virtualSlotKey(slot int) string {
	return fmt.Sprintf("slot-%d", slot)
}

func InitialPlan(m Manifest) Plan {
	bySlot := make(map[int]Desktop)
	var extras []Desktop
	for _, group := range m.Groups {
		desktop := Desktop{Key: group.DesktopID, DesktopID: group.DesktopID, ShortcutSlot: group.ShortcutSlot, Tree: &Node{Group: group.ID}}
		if group.ShortcutSlot == 0 {
			extras = append(extras, desktop)
			continue
		}
		bySlot[group.ShortcutSlot] = desktop
	}
	plan := Plan{Arrangement: Arrangement{Confirmed: []string{}}}
	for slot := setups.FirstShortcutSlot; slot <= setups.LastShortcutSlot; slot++ {
		desktop, ok := bySlot[slot]
		if !ok {
			desktop = Desktop{Key: virtualSlotKey(slot), ShortcutSlot: slot}
		}
		plan.Desktops = append(plan.Desktops, desktop)
	}
	plan.Desktops = append(plan.Desktops, extras...)
	return plan
}

func EncodePlan(p Plan) (string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("encoding the migration draft: %w", err)
	}
	return string(data), nil
}

func DecodePlan(raw string) (Plan, error) {
	var p Plan
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return Plan{}, fmt.Errorf("the stored migration draft does not decode: %w", err)
	}
	return p, nil
}

func (n *Node) groups() []string {
	if n == nil {
		return nil
	}
	if n.Group != "" {
		return []string{n.Group}
	}
	var ids []string
	for i := range n.Children {
		ids = append(ids, n.Children[i].groups()...)
	}
	return ids
}

func (n *Node) without(groupID string) *Node {
	if n == nil {
		return nil
	}
	if n.Group != "" {
		if n.Group == groupID {
			return nil
		}
		return n
	}
	var kept []Node
	for i := range n.Children {
		if child := n.Children[i].without(groupID); child != nil {
			kept = append(kept, *child)
		}
	}
	switch len(kept) {
	case 0:
		return nil
	case 1:
		return &kept[0]
	}
	return &Node{Direction: n.Direction, Ratio: n.Ratio, Children: kept}
}

func (n *Node) clone() *Node {
	if n == nil {
		return nil
	}
	out := *n
	out.Children = make([]Node, len(n.Children))
	for i := range n.Children {
		out.Children[i] = *n.Children[i].clone()
	}
	if len(n.Children) == 0 {
		out.Children = nil
	}
	return &out
}

func (a Arrangement) clone() Arrangement {
	out := Arrangement{Confirmed: slices.Clone(a.Confirmed), Suggested: a.Suggested}
	if out.Confirmed == nil {
		out.Confirmed = []string{}
	}
	for _, desktop := range a.Desktops {
		desktop.Tree = desktop.Tree.clone()
		out.Desktops = append(out.Desktops, desktop)
	}
	return out
}

func (a Arrangement) desktopIndex(key string) int {
	for i, desktop := range a.Desktops {
		if desktop.Key == key {
			return i
		}
	}
	return -1
}

func (a *Arrangement) remove(groupID string) {
	for i := range a.Desktops {
		a.Desktops[i].Tree = a.Desktops[i].Tree.without(groupID)
	}
	a.Desktops = slices.DeleteFunc(a.Desktops, func(desktop Desktop) bool {
		return desktop.ShortcutSlot == 0 && desktop.Tree == nil
	})
}

func (a *Arrangement) confirm(groupID string) {
	if !slices.Contains(a.Confirmed, groupID) {
		a.Confirmed = append(a.Confirmed, groupID)
	}
}

func (a Arrangement) retire(live map[string]GroupState) Arrangement {
	out := a.clone()
	var placed []string
	for _, desktop := range out.Desktops {
		placed = append(placed, desktop.Tree.groups()...)
	}
	for _, id := range placed {
		if _, ok := live[id]; !ok {
			out.remove(id)
		}
	}
	out.Confirmed = slices.DeleteFunc(out.Confirmed, func(id string) bool {
		_, ok := live[id]
		return !ok
	})
	return out
}

func (p Plan) Retire(live []GroupState) Plan {
	set := liveSet(live)
	out := Plan{Arrangement: p.Arrangement.retire(set)}
	for _, snapshot := range p.History {
		out.History = append(out.History, snapshot.retire(set))
	}
	return out
}

func mergeTrees(first, second *Node) *Node {
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	}
	return &Node{Direction: layouttree.DirectionVertical, Ratio: layouttree.DefaultSplitRatio, Children: []Node{*first, *second}}
}

func (a Arrangement) reconcile(current []setups.Desktop) Arrangement {
	byID := desktopsByID(current)
	bySlot := make(map[int]string)
	for _, desktop := range current {
		if desktop.ShortcutSlot != 0 {
			bySlot[desktop.ShortcutSlot] = desktop.ID
		}
	}
	out := a.clone()
	kept := make([]Desktop, 0, len(out.Desktops))
	position := make(map[string]int)
	for _, entry := range out.Desktops {
		if live, exists := byID[entry.DesktopID]; exists {
			entry.ShortcutSlot = live.ShortcutSlot
		} else if holder, held := bySlot[entry.ShortcutSlot]; held && entry.ShortcutSlot != 0 {
			entry.DesktopID, entry.Key = holder, holder
		} else {
			entry.DesktopID = ""
			if entry.ShortcutSlot != 0 {
				entry.Key = virtualSlotKey(entry.ShortcutSlot)
			}
		}
		if entry.DesktopID != "" {
			if at, seen := position[entry.DesktopID]; seen {
				kept[at].Tree = mergeTrees(kept[at].Tree, entry.Tree)
				continue
			}
			position[entry.DesktopID] = len(kept)
		}
		kept = append(kept, entry)
	}
	out.Desktops = withEveryShortcutSlot(kept, bySlot)
	return out
}

func withEveryShortcutSlot(desktops []Desktop, heldBy map[int]string) []Desktop {
	bySlot := make(map[int]Desktop)
	var extras []Desktop
	for _, desktop := range desktops {
		if desktop.ShortcutSlot == 0 {
			extras = append(extras, desktop)
			continue
		}
		bySlot[desktop.ShortcutSlot] = desktop
	}
	ordered := make([]Desktop, 0, setups.LastShortcutSlot+len(extras))
	for slot := setups.FirstShortcutSlot; slot <= setups.LastShortcutSlot; slot++ {
		desktop, represented := bySlot[slot]
		if !represented {
			desktop = Desktop{Key: virtualSlotKey(slot), ShortcutSlot: slot}
			if holder, held := heldBy[slot]; held {
				desktop.Key, desktop.DesktopID = holder, holder
			}
		}
		ordered = append(ordered, desktop)
	}
	return append(ordered, extras...)
}

func (p Plan) Reconcile(current []setups.Desktop) Plan {
	out := Plan{Arrangement: p.Arrangement.reconcile(current)}
	for _, snapshot := range p.History {
		out.History = append(out.History, snapshot.reconcile(current))
	}
	return out
}

func (p Plan) begin() Plan {
	next := Plan{Arrangement: p.Arrangement.clone(), History: append(slices.Clone(p.History), p.Arrangement.clone())}
	return next
}

func requireLive(live map[string]GroupState, groupID string) error {
	if _, ok := live[groupID]; !ok {
		return setups.Errorf(setups.CodeNotFound, "imported group %q is not waiting for placement; it was closed or never imported", groupID)
	}
	return nil
}

func (p Plan) Keep(live []GroupState, groupIDs []string) (Plan, error) {
	set := liveSet(live)
	if len(groupIDs) == 0 {
		return p, setups.Errorf(setups.CodeInvalid, "keep names no imported group")
	}
	next := p.begin()
	for _, id := range groupIDs {
		if err := requireLive(set, id); err != nil {
			return p, err
		}
		next.confirm(id)
	}
	return next, nil
}

func splitSides(edge Edge) (layouttree.Direction, bool, error) {
	switch edge {
	case EdgeLeft:
		return layouttree.DirectionVertical, true, nil
	case EdgeRight:
		return layouttree.DirectionVertical, false, nil
	case EdgeTop:
		return layouttree.DirectionHorizontal, true, nil
	case EdgeBottom:
		return layouttree.DirectionHorizontal, false, nil
	}
	return "", false, setups.Errorf(setups.CodeInvalid, "edge %q is not one of left, right, top, bottom", edge)
}

func join(existing *Node, groupID string, edge Edge, share float64) (*Node, error) {
	incoming := &Node{Group: groupID}
	if existing == nil {
		return incoming, nil
	}
	direction, before, err := splitSides(edge)
	if err != nil {
		return nil, err
	}
	if share == 0 {
		share = layouttree.DefaultSplitRatio
	}
	if !(share > 0 && share < 1) {
		return nil, setups.Errorf(setups.CodeInvalid, "share %v is outside (0,1); both sides of a split need space", share)
	}
	if before {
		return &Node{Direction: direction, Ratio: share, Children: []Node{*incoming, *existing}}, nil
	}
	return &Node{Direction: direction, Ratio: 1 - share, Children: []Node{*existing, *incoming}}, nil
}

func (n *Node) joinBeside(anchor, groupID string, edge Edge, share float64) (*Node, bool, error) {
	if n == nil {
		return nil, false, nil
	}
	if n.Group != "" {
		if n.Group != anchor {
			return n, false, nil
		}
		joined, err := join(n, groupID, edge, share)
		return joined, err == nil, err
	}
	out := n.clone()
	for i := range out.Children {
		child, found, err := out.Children[i].joinBeside(anchor, groupID, edge, share)
		if err != nil || found {
			if found {
				out.Children[i] = *child
			}
			return out, found, err
		}
	}
	return out, false, nil
}

func (a *Arrangement) insert(groupID, targetKey, anchorGroupID string, edge Edge, share float64) error {
	index := a.desktopIndex(targetKey)
	if index < 0 {
		return setups.Errorf(setups.CodeNotFound, "desktop %q is not in the draft; it may have been an extra desktop that only held the group being moved", targetKey)
	}
	target := &a.Desktops[index]
	if anchorGroupID == "" || target.Tree == nil {
		joined, err := join(target.Tree, groupID, edge, share)
		if err != nil {
			return err
		}
		target.Tree = joined
		return nil
	}
	joined, found, err := target.Tree.joinBeside(anchorGroupID, groupID, edge, share)
	if err != nil {
		return err
	}
	if !found {
		return setups.Errorf(setups.CodeNotFound, "group %q is not on desktop %q, so nothing can be placed beside it there", anchorGroupID, targetKey)
	}
	target.Tree = joined
	return nil
}

func (p Plan) Move(live []GroupState, groupID, targetKey, anchorGroupID string, edge Edge, share float64) (Plan, error) {
	set := liveSet(live)
	if err := requireLive(set, groupID); err != nil {
		return p, err
	}
	if anchorGroupID == groupID {
		return p, setups.Errorf(setups.CodeInvalid, "group %q cannot be placed beside itself", groupID)
	}
	next := p.begin()
	next.remove(groupID)
	if err := next.insert(groupID, strings.TrimSpace(targetKey), strings.TrimSpace(anchorGroupID), edge, share); err != nil {
		return p, err
	}
	next.confirm(groupID)
	return next, nil
}

func (a Arrangement) weight(desktop Desktop, live map[string]GroupState) int {
	total := 0
	for _, id := range desktop.Tree.groups() {
		total += live[id].LeafCount()
	}
	return total
}

func (a Arrangement) unconfirmedExtras() []string {
	var ids []string
	for _, desktop := range a.Desktops {
		if desktop.ShortcutSlot != 0 {
			continue
		}
		for _, id := range desktop.Tree.groups() {
			if !slices.Contains(a.Confirmed, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (a Arrangement) lightestSlot(live map[string]GroupState) (int, int) {
	best, bestWeight := -1, 0
	for i, desktop := range a.Desktops {
		if desktop.ShortcutSlot == 0 {
			continue
		}
		if weight := a.weight(desktop, live); best < 0 || weight < bestWeight {
			best, bestWeight = i, weight
		}
	}
	return best, bestWeight
}

func (p Plan) SuggestionAvailable() bool {
	return !p.Suggested && len(p.unconfirmedExtras()) > 0
}

func (p Plan) Suggest(live []GroupState) (Plan, error) {
	if p.Suggested {
		return p, setups.Errorf(setups.CodeInvalid, "the suggestion was already applied; undo it to suggest again")
	}
	extras := p.unconfirmedExtras()
	if len(extras) == 0 {
		return p, setups.Errorf(setups.CodeInvalid, "every extra desktop is already confirmed, so there is nothing to suggest")
	}
	set := liveSet(live)
	next := p.begin()
	for _, id := range extras {
		next.remove(id)
		index, load := next.lightestSlot(set)
		size := set[id].LeafCount()
		share := float64(size) / float64(load+size)
		if err := next.insert(id, next.Desktops[index].Key, "", EdgeRight, share); err != nil {
			return p, err
		}
		next.confirm(id)
	}
	next.Suggested = true
	return next, nil
}

func (p Plan) CanUndo() bool {
	return len(p.History) > 0
}

func (p Plan) Undo() (Plan, error) {
	if !p.CanUndo() {
		return p, setups.Errorf(setups.CodeInvalid, "there is nothing to undo")
	}
	last := len(p.History) - 1
	return Plan{Arrangement: p.History[last].clone(), History: slices.Clone(p.History[:last])}, nil
}

func (p Plan) Unconfirmed(live []GroupState) []string {
	var ids []string
	for _, group := range live {
		if !slices.Contains(p.Confirmed, group.ID) {
			ids = append(ids, group.ID)
		}
	}
	return ids
}

func (n *Node) check(path string) error {
	if n.Group != "" {
		if len(n.Children) != 0 {
			return fmt.Errorf("%s: group %s has children", path, n.Group)
		}
		return nil
	}
	if n.Direction != layouttree.DirectionVertical && n.Direction != layouttree.DirectionHorizontal {
		return fmt.Errorf("%s: split direction %q", path, n.Direction)
	}
	if !(n.Ratio > 0 && n.Ratio < 1) || len(n.Children) != 2 {
		return fmt.Errorf("%s: a split needs two children and a ratio in (0,1)", path)
	}
	for i := range n.Children {
		if err := n.Children[i].check(fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return err
		}
	}
	return nil
}

func (p Plan) Check(live []GroupState) error {
	placed := make(map[string]string)
	keys := make(map[string]bool)
	slots := make(map[int]bool)
	for _, desktop := range p.Desktops {
		if keys[desktop.Key] || desktop.Key == "" {
			return setups.Errorf(setups.CodeInvalid, "draft desktop key %q is empty or repeated", desktop.Key)
		}
		keys[desktop.Key] = true
		if desktop.ShortcutSlot != 0 {
			if err := setups.ValidateShortcutSlot(desktop.ShortcutSlot); err != nil {
				return err
			}
			if slots[desktop.ShortcutSlot] {
				return setups.Errorf(setups.CodeInvalid, "draft holds shortcut slot %d twice", desktop.ShortcutSlot)
			}
			slots[desktop.ShortcutSlot] = true
		}
		if desktop.Tree == nil {
			continue
		}
		if err := desktop.Tree.check(desktop.Key); err != nil {
			return setups.Errorf(setups.CodeInvalid, "draft tree invalid at %v", err)
		}
		for _, id := range desktop.Tree.groups() {
			if previous, dup := placed[id]; dup {
				return setups.Errorf(setups.CodeInvalid, "draft places group %s on %s and %s", id, previous, desktop.Key)
			}
			placed[id] = desktop.Key
		}
	}
	for slot := setups.FirstShortcutSlot; slot <= setups.LastShortcutSlot; slot++ {
		if !slots[slot] {
			return setups.Errorf(setups.CodeInvalid, "draft has no desktop for shortcut slot %d", slot)
		}
	}
	for _, group := range live {
		if _, ok := placed[group.ID]; !ok {
			return setups.Errorf(setups.CodeInvalid, "draft does not place imported group %s", group.ID)
		}
	}
	if len(placed) != len(live) {
		return setups.Errorf(setups.CodeInvalid, "draft places %d groups but %d are waiting for placement", len(placed), len(live))
	}
	return nil
}
