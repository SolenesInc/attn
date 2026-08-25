package garden

import (
	"fmt"
	"slices"
	"strings"
)

// Some kinds are declared ahead of their verbs, so a body written by a later attn stays readable.
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
		return "", fmt.Errorf("%q is a real edge kind but nothing links one yet; the kinds you can link are %s",
			kind, strings.Join(LinkableKinds, " and "))
	}
	return "", fmt.Errorf("%q is not how two seeds relate; the kinds are %s", raw, strings.Join(LinkableKinds, " and "))
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
		if parent, ok := parentOf(from); ok {
			return Seed{}, false, fmt.Errorf(
				"%s is already part of %s, and a seed sits in one plot: `attn seed unlink %s part-of %s` first, then link it to %s",
				from.ID, parent, from.ID, parent, to.ID)
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
		return Seed{}, fmt.Errorf("%s does not %s %s%s", from.ID, kind, to.ID, edgeInventory(from))
	}
	next := from
	next.Edges = slices.Delete(slices.Clone(from.Edges), index, index+1)
	return next, nil
}

func linkEnds(seeds []Seed, fromID, kind, toID string) (Seed, Seed, error) {
	from, ok := find(seeds, fromID)
	if !ok {
		return Seed{}, Seed{}, fmt.Errorf("no seed %s is planted here; `attn seed ls` lists the garden", fromID)
	}
	to, ok := find(seeds, toID)
	if !ok {
		return Seed{}, Seed{}, fmt.Errorf("no seed %s is planted here; `attn seed ls` lists the garden", toID)
	}
	if from.ID == to.ID {
		return Seed{}, Seed{}, fmt.Errorf("%s cannot %s itself", from.ID, kind)
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
		return fmt.Errorf(
			"%s is already inside %s (%s), so making %s part of %s would put the plot inside itself.\n"+
				"Unlink an edge in that chain first: attn seed unlink %s part-of %s",
			toID, fromID, chain, fromID, toID, toID, path[0])
	}
	return fmt.Errorf(
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

// sessionLive feeds Tender.Holds, the same rule `tend` claims under, so a seed offered here is one `tend` accepts.
// `tend` claims under, so a seed offered here is one `tend` accepts.
func Ready(seeds []Seed, sessionLive func(sessionID string) bool) []Seed {
	index := byID(seeds)
	blocked := blockedIDs(seeds)
	parents := parentIDs(seeds)
	ready := make([]Seed, 0, len(seeds))
	for _, seed := range seeds {
		switch {
		case Closed(seed.Status), seed.Status == StatusDormant:
			continue
		case parents[seed.ID], blocked[seed.ID], seed.Gate:
			continue
		case underTemplate(index, seed):
			continue
		}
		if seed.Tender().Holds(sessionLive) {
			continue
		}
		ready = append(ready, seed)
	}
	return ready
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

func InPlot(seeds []Seed, crownID string) []Seed {
	inside := map[string]bool{crownID: true}
	// Repeat until nothing new joins: a child may be listed before its parent.
	for grew := true; grew; {
		grew = false
		for _, seed := range seeds {
			if inside[seed.ID] {
				continue
			}
			if parent, ok := parentOf(seed); ok && inside[parent] {
				inside[seed.ID] = true
				grew = true
			}
		}
	}
	out := make([]Seed, 0, len(inside))
	for _, seed := range seeds {
		if inside[seed.ID] {
			out = append(out, seed)
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
			parent, hasParent := parentOf(seed)
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

type TreeRow struct {
	Seed  Seed
	Depth int
}

func Tree(seeds []Seed) []TreeRow {
	children := map[string][]Seed{}
	roots := make([]Seed, 0, len(seeds))
	present := byID(seeds)
	for _, seed := range seeds {
		parent, ok := parentOf(seed)
		if _, inScope := present[parent]; ok && inScope {
			children[parent] = append(children[parent], seed)
			continue
		}
		roots = append(roots, seed)
	}
	rows := make([]TreeRow, 0, len(seeds))
	placed := map[string]bool{}
	var place func(seed Seed, depth int)
	place = func(seed Seed, depth int) {
		// A stored cycle would otherwise recurse forever.
		if placed[seed.ID] {
			return
		}
		placed[seed.ID] = true
		rows = append(rows, TreeRow{Seed: seed, Depth: depth})
		for _, child := range children[seed.ID] {
			place(child, depth+1)
		}
	}
	for _, root := range roots {
		place(root, 0)
	}
	for _, seed := range seeds {
		place(seed, 0)
	}
	return rows
}

func parentOf(seed Seed) (string, bool) {
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
		parent, ok := parentOf(seed)
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
		if parent, ok := parentOf(seed); ok {
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
