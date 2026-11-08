package garden

import (
	"fmt"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

const (
	EdgeBlocks         = "blocks"
	EdgePartOf         = "part-of"
	EdgeSownFrom       = "sown-from"
	EdgeDiscoveredFrom = "discovered-from"
	EdgeRelatesTo      = "relates-to"
)

var LinkableKinds = []string{EdgeBlocks, EdgePartOf, EdgeDiscoveredFrom}

func ParseEdgeKind(raw string) (string, error) {
	kind := strings.TrimSpace(strings.ToLower(raw))
	if slices.Contains(LinkableKinds, kind) {
		return kind, nil
	}
	if slices.Contains([]string{EdgeSownFrom, EdgeRelatesTo}, kind) {
		return "", protocol.Refuse(protocol.RefusalInvalidInput, "%q is a real edge kind but nothing links one yet; the kinds you can link are %s",
			kind, strings.Join(LinkableKinds, " and "))
	}
	return "", protocol.Refuse(protocol.RefusalInvalidInput, "%q is not how two seeds relate; the kinds are %s", raw, strings.Join(LinkableKinds, " and "))
}

func Link(seeds []Seed, fromID, kind, toID string) (Seed, bool, error) {
	from, to, err := linkEnds(seeds, fromID, kind, toID)
	if err != nil {
		return Seed{}, false, err
	}
	if slices.Contains(from.Edges, Edge{Kind: kind, To: to.ID}) {
		return from, false, nil
	}
	if kind == EdgePartOf {
		if parent, ok := ParentOf(from); ok {
			return Seed{}, false, protocol.Refuse(protocol.RefusalWrongState,
				"%s is already part of %s, and a seed sits in one plot: `attn seed unlink %s part-of %s` first, then link it to %s",
				from.ID, parent, from.ID, parent, to.ID)
		}
	}
	if kind == EdgePartOf {
		tree := NewPlotTree(seeds)
		moving := tree.checksUnder(from.ID)
		if checkID, owner, repeated := tree.UsedCheckID(to.ID, moving); repeated {
			joined := max(highestCheckNumber(tree.checksUnder(tree.root(to.ID))), highestCheckNumber(moving)) + 1
			return Seed{}, false, protocol.Refuse(protocol.RefusalConflict,
				"%s and %s would both be in one plot tree with check %s, which %s already uses; one id names one statement in a plot tree, so they cannot share a plot. The next free id across both is %s",
				from.ID, to.ID, checkID, owner, checkIDNumbered(joined))
		}
	}
	if kind == EdgeBlocks || kind == EdgePartOf {
		if path := reaches(seeds, to.ID, kind, from.ID); len(path) > 0 {
			return Seed{}, false, cycleRefusal(from.ID, kind, to.ID, path)
		}
	}
	next := from
	next.Edges = append(slices.Clone(from.Edges), Edge{Kind: kind, To: to.ID})
	return next, true, nil
}

func Unlink(seeds []Seed, fromID, kind, toID string) (Seed, error) {
	from, to, err := linkEnds(seeds, fromID, kind, toID)
	if err != nil {
		return Seed{}, err
	}
	edge := Edge{Kind: kind, To: to.ID}
	index := slices.Index(from.Edges, edge)
	if index < 0 {
		return Seed{}, protocol.Refuse(protocol.RefusalNotFound, "%s does not %s %s%s", from.ID, kind, to.ID, edgeInventory(from))
	}
	next := from
	next.Edges = slices.Delete(slices.Clone(from.Edges), index, index+1)
	return next, nil
}

func linkEnds(seeds []Seed, fromID, kind, toID string) (Seed, Seed, error) {
	from, ok := find(seeds, fromID)
	if !ok {
		return Seed{}, Seed{}, protocol.Refuse(protocol.RefusalNotFound, "no seed %s is planted here; `attn seed ls` lists the garden", fromID)
	}
	to, ok := find(seeds, toID)
	if !ok {
		return Seed{}, Seed{}, protocol.Refuse(protocol.RefusalNotFound, "no seed %s is planted here; `attn seed ls` lists the garden", toID)
	}
	if from.ID == to.ID {
		return Seed{}, Seed{}, protocol.Refuse(protocol.RefusalInvalidInput, "%s cannot %s itself", from.ID, kind)
	}
	return from, to, nil
}

func edgeInventory(seed Seed) string {
	if len(seed.Edges) == 0 {
		return "; it has no edges at all"
	}
	rendered := make([]string, 0, len(seed.Edges))
	for _, edge := range seed.Edges {
		rendered = append(rendered, fmt.Sprintf("%s %s", edge.Kind, edge.To))
	}
	return fmt.Sprintf("; it is linked %s", strings.Join(rendered, ", "))
}

func cycleRefusal(fromID, kind, toID string, path []string) error {
	chain := strings.Join(append([]string{toID}, path...), " → ")
	if kind == EdgePartOf {
		return protocol.Refuse(protocol.RefusalWrongState,
			"%s is already inside %s (%s), so making %s part of %s would put the plot inside itself.\n"+
				"Unlink an edge in that chain first: attn seed unlink %s part-of %s",
			toID, fromID, chain, fromID, toID, toID, path[0])
	}
	return protocol.Refuse(protocol.RefusalWrongState,
		"%s already blocks %s (%s), so %s blocking %s would deadlock them — neither would ever be ready.\n"+
			"Unlink an edge in that chain first: attn seed unlink %s blocks %s",
		toID, fromID, chain, fromID, toID, toID, path[0])
}

func reaches(seeds []Seed, start, kind, target string) []string {
	index := byID(seeds)
	visited := map[string]bool{start: true}
	var walk func(id string) []string
	walk = func(id string) []string {
		for _, edge := range index[id].Edges {
			if edge.Kind != kind {
				continue
			}
			if edge.To == target {
				return []string{target}
			}
			if visited[edge.To] {
				continue
			}
			visited[edge.To] = true
			if rest := walk(edge.To); rest != nil {
				return append([]string{edge.To}, rest...)
			}
		}
		return nil
	}
	return walk(start)
}

func Ready(seeds []Seed, claimHolds func(Seed) bool) []Seed {
	graph := readiness(seeds)
	ready := make([]Seed, 0, len(seeds))
	for _, seed := range seeds {
		if !graph.open(seed) || claimHolds(seed) {
			continue
		}
		ready = append(ready, seed)
	}
	return ready
}

func Unblocks(seeds []Seed, closedID string) []Seed {
	closed, ok := find(seeds, closedID)
	if !ok || !Closed(closed.Status) {
		return nil
	}
	freed := map[string]bool{}
	for _, edge := range closed.Edges {
		if edge.Kind == EdgeBlocks {
			freed[edge.To] = true
		}
	}
	graph := readiness(seeds)
	out := []Seed{}
	for _, seed := range seeds {
		if freed[seed.ID] && graph.open(seed) {
			out = append(out, seed)
		}
	}
	return out
}

type readinessGraph struct {
	index   map[string]Seed
	blocked map[string]bool
	parents map[string]bool
}

func readiness(seeds []Seed) readinessGraph {
	return readinessGraph{index: byID(seeds), blocked: blockedIDs(seeds), parents: parentIDs(seeds)}
}

func (g readinessGraph) open(seed Seed) bool {
	switch {
	case Closed(seed.Status), seed.Status == StatusParked:
		return false
	case g.parents[seed.ID], g.blocked[seed.ID], seed.Gate:
		return false
	case underTemplate(g.index, seed):
		return false
	}
	return true
}

func Blockers(seeds []Seed, id string) []string {
	out := []string{}
	for _, seed := range seeds {
		if Closed(seed.Status) {
			continue
		}
		for _, edge := range seed.Edges {
			if edge.Kind == EdgeBlocks && edge.To == id {
				out = append(out, seed.ID)
			}
		}
	}
	return out
}

type Relation struct {
	Label string
	Seed  string
}

var inboundLabels = map[string]string{
	EdgeBlocks:         "blocked-by",
	EdgePartOf:         "has-part",
	EdgeDiscoveredFrom: "discovered",
}

func inboundLabel(kind string) string {
	if label, ok := inboundLabels[kind]; ok {
		return label
	}
	return "inbound " + kind
}

func Relations(seeds []Seed, id string) []Relation {
	out := []Relation{}
	if seed, ok := find(seeds, id); ok {
		for _, edge := range seed.Edges {
			out = append(out, Relation{Label: edge.Kind, Seed: edge.To})
		}
	}
	for _, seed := range seeds {
		if seed.ID == id {
			continue
		}
		for _, edge := range seed.Edges {
			if edge.To == id {
				out = append(out, Relation{Label: inboundLabel(edge.Kind), Seed: seed.ID})
			}
		}
	}
	return out
}

func PlotHeaders(seeds []Seed, selected map[string]bool) []Seed {
	index := byID(seeds)
	headers := map[string]bool{}
	for id := range selected {
		seed, ok := index[id]
		seen := map[string]bool{}
		for ok {
			parent, hasParent := ParentOf(seed)
			if !hasParent || seen[parent] {
				break
			}
			seen[parent] = true
			seed, ok = index[parent]
			if ok {
				headers[parent] = true
			}
		}
	}
	out := make([]Seed, 0, len(headers))
	for _, seed := range seeds {
		if headers[seed.ID] {
			out = append(out, seed)
		}
	}
	return out
}

type TreeNode struct {
	ID     string
	Parent string
}

type TreeRow struct {
	Index int
	Depth int
}

func Tree(nodes []TreeNode) []TreeRow {
	index := make(map[string]int, len(nodes))
	for i, node := range nodes {
		index[node.ID] = i
	}
	children := map[int][]int{}
	roots := make([]int, 0, len(nodes))
	for i, node := range nodes {
		if parent, inScope := index[node.Parent]; node.Parent != "" && inScope {
			children[parent] = append(children[parent], i)
			continue
		}
		roots = append(roots, i)
	}
	rows := make([]TreeRow, 0, len(nodes))
	placed := make([]bool, len(nodes))
	var place func(i, depth int)
	place = func(i, depth int) {
		if placed[i] {
			return
		}
		placed[i] = true
		rows = append(rows, TreeRow{Index: i, Depth: depth})
		for _, child := range children[i] {
			place(child, depth+1)
		}
	}
	for _, root := range roots {
		place(root, 0)
	}
	for i := range nodes {
		place(i, 0)
	}
	return rows
}

func ParentOf(seed Seed) (string, bool) {
	for _, edge := range seed.Edges {
		if edge.Kind == EdgePartOf {
			return edge.To, true
		}
	}
	return "", false
}

func underTemplate(index map[string]Seed, seed Seed) bool {
	seen := map[string]bool{}
	for {
		if seed.Template {
			return true
		}
		parent, ok := ParentOf(seed)
		if !ok || seen[parent] {
			return false
		}
		seen[parent] = true
		seed, ok = index[parent]
		if !ok {
			return false
		}
	}
}

func blockedIDs(seeds []Seed) map[string]bool {
	blocked := map[string]bool{}
	for _, seed := range seeds {
		if Closed(seed.Status) {
			continue
		}
		for _, edge := range seed.Edges {
			if edge.Kind == EdgeBlocks {
				blocked[edge.To] = true
			}
		}
	}
	return blocked
}

func parentIDs(seeds []Seed) map[string]bool {
	parents := map[string]bool{}
	for _, seed := range seeds {
		if parent, ok := ParentOf(seed); ok {
			parents[parent] = true
		}
	}
	return parents
}

func byID(seeds []Seed) map[string]Seed {
	index := make(map[string]Seed, len(seeds))
	for _, seed := range seeds {
		index[seed.ID] = seed
	}
	return index
}

func find(seeds []Seed, id string) (Seed, bool) {
	for _, seed := range seeds {
		if seed.ID == id {
			return seed, true
		}
	}
	return Seed{}, false
}
