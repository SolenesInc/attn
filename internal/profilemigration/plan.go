package profilemigration

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
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

type Plan struct {
	Desktops []Desktop `json:"desktops"`
}

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
	var plan Plan
	for slot := profiles.FirstShortcutSlot; slot <= profiles.LastShortcutSlot; slot++ {
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

func (p Plan) clone() Plan {
	var out Plan
	for _, desktop := range p.Desktops {
		desktop.Tree = desktop.Tree.clone()
		out.Desktops = append(out.Desktops, desktop)
	}
	return out
}

func (p *Plan) remove(groupID string) {
	for i := range p.Desktops {
		p.Desktops[i].Tree = p.Desktops[i].Tree.without(groupID)
	}
	p.Desktops = slices.DeleteFunc(p.Desktops, func(desktop Desktop) bool {
		return desktop.ShortcutSlot == 0 && desktop.Tree == nil
	})
}

func (p Plan) Retire(live []GroupState) Plan {
	set := liveSet(live)
	out := p.clone()
	var placed []string
	for _, desktop := range out.Desktops {
		placed = append(placed, desktop.Tree.groups()...)
	}
	for _, id := range placed {
		if _, ok := set[id]; !ok {
			out.remove(id)
		}
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

func (p Plan) Reconcile(current []profiles.Desktop) Plan {
	byID := desktopsByID(current)
	bySlot := make(map[int]string)
	for _, desktop := range current {
		if desktop.ShortcutSlot != 0 {
			bySlot[desktop.ShortcutSlot] = desktop.ID
		}
	}
	out := p.clone()
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
	ordered := make([]Desktop, 0, profiles.LastShortcutSlot+len(extras))
	for slot := profiles.FirstShortcutSlot; slot <= profiles.LastShortcutSlot; slot++ {
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
			return profiles.Errorf(profiles.CodeInvalid, "draft desktop key %q is empty or repeated", desktop.Key)
		}
		keys[desktop.Key] = true
		if desktop.ShortcutSlot != 0 {
			if err := profiles.ValidateShortcutSlot(desktop.ShortcutSlot); err != nil {
				return err
			}
			if slots[desktop.ShortcutSlot] {
				return profiles.Errorf(profiles.CodeInvalid, "draft holds shortcut slot %d twice", desktop.ShortcutSlot)
			}
			slots[desktop.ShortcutSlot] = true
		}
		if desktop.Tree == nil {
			continue
		}
		if err := desktop.Tree.check(desktop.Key); err != nil {
			return profiles.Errorf(profiles.CodeInvalid, "draft tree invalid at %v", err)
		}
		for _, id := range desktop.Tree.groups() {
			if previous, dup := placed[id]; dup {
				return profiles.Errorf(profiles.CodeInvalid, "draft places group %s on %s and %s", id, previous, desktop.Key)
			}
			placed[id] = desktop.Key
		}
	}
	for slot := profiles.FirstShortcutSlot; slot <= profiles.LastShortcutSlot; slot++ {
		if !slots[slot] {
			return profiles.Errorf(profiles.CodeInvalid, "draft has no desktop for shortcut slot %d", slot)
		}
	}
	for _, group := range live {
		if _, ok := placed[group.ID]; !ok {
			return profiles.Errorf(profiles.CodeInvalid, "draft does not place imported group %s", group.ID)
		}
	}
	if len(placed) != len(live) {
		return profiles.Errorf(profiles.CodeInvalid, "draft places %d groups but %d are waiting for placement", len(placed), len(live))
	}
	return nil
}
