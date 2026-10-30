package garden

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Progress struct {
	Total    int
	Done     int
	Withered int
	Growing  int
	Dormant  int
	Ready    int
	Blocked  int
	Checks   CheckTally
}

type PlotTree struct {
	seeds    []Seed
	byID     map[string]Seed
	children map[string][]Seed
	blocked  map[string]bool
}

func NewPlotTree(seeds []Seed) PlotTree {
	children := map[string][]Seed{}
	for _, seed := range seeds {
		if parent, ok := ParentOf(seed); ok {
			children[parent] = append(children[parent], seed)
		}
	}
	return PlotTree{seeds: seeds, byID: byID(seeds), children: children, blocked: blockedIDs(seeds)}
}

func (t PlotTree) descendants(crownID string) []Seed {
	if len(t.children[crownID]) == 0 {
		return nil
	}
	seen := map[string]bool{crownID: true}
	out := []Seed{}
	for next := []string{crownID}; len(next) > 0; {
		id := next[len(next)-1]
		next = next[:len(next)-1]
		for _, child := range t.children[id] {
			if seen[child.ID] {
				continue
			}
			seen[child.ID] = true
			out = append(out, child)
			next = append(next, child.ID)
		}
	}
	return out
}

func (t PlotTree) Children(plotID string) []Seed {
	return t.children[plotID]
}

func (t PlotTree) InPlot(crownID string) []Seed {
	inside := map[string]bool{crownID: true}
	for _, seed := range t.descendants(crownID) {
		inside[seed.ID] = true
	}
	out := make([]Seed, 0, len(inside))
	for _, seed := range t.seeds {
		if inside[seed.ID] {
			out = append(out, seed)
		}
	}
	return out
}

func (t PlotTree) Progress(crownID string, ready map[string]bool) Progress {
	var p Progress
	for _, seed := range t.descendants(crownID) {
		p.Total++
		switch {
		case seed.Status == StatusHarvested:
			p.Done++
		case seed.Status == StatusWithered:
			p.Withered++
		case seed.Status == StatusGrowing:
			p.Growing++
		case seed.Status == StatusDormant:
			p.Dormant++
		}
		if ready[seed.ID] {
			p.Ready++
		}
		if !Closed(seed.Status) && t.blocked[seed.ID] {
			p.Blocked++
		}
	}
	if p.Total > 0 {
		p.Checks = t.CheckTally(crownID)
	}
	return p
}

const DefaultStaleWindow = 7 * 24 * time.Hour

func Stale(seeds []Seed, lastMoved map[string]time.Time, window time.Duration, now time.Time) []Seed {
	out := []Seed{}
	for _, seed := range seeds {
		if Closed(seed.Status) {
			continue
		}
		moved, known := lastMoved[seed.ID]
		if !known {
			continue
		}
		if now.Sub(moved) >= window {
			out = append(out, seed)
		}
	}
	return out
}

type PlotChildSpec struct {
	Title  string   `json:"title"`
	Body   string   `json:"body,omitempty"`
	Checks []Check  `json:"checks,omitempty"`
	After  []string `json:"after,omitempty"`
}

type PlotSpec struct {
	Title          string          `json:"title"`
	Body           string          `json:"body,omitempty"`
	Checks         []Check         `json:"checks,omitempty"`
	PartOf         string          `json:"part_of,omitempty"`
	DiscoveredFrom string          `json:"discovered_from,omitempty"`
	Children       []PlotChildSpec `json:"children"`
}

const plotPayloadShape = `{"title": …, "body": …, "part_of": "<plot>", "discovered_from": "<seed>", "children": [{"title": …, "after": ["<sibling slug>"]}]}`

func ParsePlotSpec(raw []byte) (PlotSpec, error) {
	if err := refuseBlocksInPlotPayload(raw); err != nil {
		return PlotSpec{}, err
	}
	var spec PlotSpec
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return PlotSpec{}, fmt.Errorf("this is not a plot payload: %w (the shape is %s)", err, plotPayloadShape)
	}
	if err := ValidatePlotSpec(spec); err != nil {
		return PlotSpec{}, err
	}
	return spec, nil
}

func refuseBlocksInPlotPayload(raw []byte) error {
	var probe struct {
		Children []struct {
			Title  string    `json:"title"`
			Blocks *[]string `json:"blocks"`
		} `json:"children"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return nil
	}
	for i, child := range probe.Children {
		if child.Blocks == nil {
			continue
		}
		rewrite := "delete it"
		if len(*child.Blocks) > 0 {
			rewrite = fmt.Sprintf(`add "after": [%q] to %s`, StepSlug(child.Title), strings.Join(quoted(*child.Blocks), " and "))
		}
		return fmt.Errorf(`child %d (%q) has "blocks", which plot payloads do not take; a child names the siblings it waits for with "after". Here: %s`, i+1, child.Title, rewrite)
	}
	return nil
}

func quoted(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = strconv.Quote(value)
	}
	return out
}

func ValidatePlotSpec(spec PlotSpec) error {
	if err := ValidatePlant(spec.Title, spec.Body); err != nil {
		return fmt.Errorf("crown: %w", err)
	}
	if err := validatePlotChecks(spec); err != nil {
		return err
	}
	if len(spec.Children) == 0 {
		return fmt.Errorf("a plot is a crown with children and this payload has none; to plant one seed use `attn seed plant`")
	}
	slugs := make(map[string]int, len(spec.Children))
	for i, child := range spec.Children {
		if err := ValidatePlant(child.Title, child.Body); err != nil {
			return fmt.Errorf("child %d: %w", i+1, err)
		}
		slug := StepSlug(child.Title)
		if prev, taken := slugs[slug]; taken {
			return fmt.Errorf("children %d and %d both derive the step slug %q; after addresses siblings by slug, so retitle one", prev+1, i+1, slug)
		}
		slugs[slug] = i
	}
	for i, child := range spec.Children {
		for _, sibling := range child.After {
			if _, known := slugs[StepSlug(sibling)]; !known {
				return fmt.Errorf("child %d waits for %q, which is no sibling's title or step slug; the slugs here are %s", i+1, sibling, strings.Join(slugList(spec.Children), ", "))
			}
		}
	}
	if from, to, cyclic := afterCycle(spec.Children, slugs); cyclic {
		return fmt.Errorf("%q and %q wait for each other through after, so neither child would ever be ready; sequence one way only", from, to)
	}
	return nil
}

func validatePlotChecks(spec PlotSpec) error {
	if err := ValidateCheckDefinitions(spec.Checks); err != nil {
		return fmt.Errorf("crown: %w", err)
	}
	usedBy := map[string]string{}
	for _, check := range spec.Checks {
		usedBy[check.ID] = "the crown"
	}
	for i, child := range spec.Children {
		if err := ValidateCheckDefinitions(child.Checks); err != nil {
			return fmt.Errorf("child %d: %w", i+1, err)
		}
		for _, check := range child.Checks {
			if owner, taken := usedBy[check.ID]; taken {
				return fmt.Errorf("child %d and %s both define %s; one id names one statement in a plot tree", i+1, owner, check.ID)
			}
			usedBy[check.ID] = fmt.Sprintf("child %d", i+1)
		}
	}
	return nil
}

func slugList(children []PlotChildSpec) []string {
	out := make([]string, 0, len(children))
	for _, child := range children {
		out = append(out, StepSlug(child.Title))
	}
	return out
}

func afterCycle(children []PlotChildSpec, slugs map[string]int) (string, string, bool) {
	const unseen, visiting, done = 0, 1, 2
	state := make([]int, len(children))
	var walk func(i int) (string, string, bool)
	walk = func(i int) (string, string, bool) {
		state[i] = visiting
		for _, sibling := range children[i].After {
			j := slugs[StepSlug(sibling)]
			if state[j] == visiting {
				return StepSlug(children[i].Title), StepSlug(children[j].Title), true
			}
			if state[j] == unseen {
				if from, to, cyclic := walk(j); cyclic {
					return from, to, true
				}
			}
		}
		state[i] = done
		return "", "", false
	}
	for i := range children {
		if state[i] == unseen {
			if from, to, cyclic := walk(i); cyclic {
				return from, to, true
			}
		}
	}
	return "", "", false
}

func OpenChildren(seeds []Seed, plotID string) []Seed {
	var children []Seed
	for _, seed := range seeds {
		if parent, ok := ParentOf(seed); ok && parent == plotID && !Closed(seed.Status) {
			children = append(children, seed)
		}
	}
	return children
}

type OpenChildrenError struct {
	PlotID   string
	Children []Seed
}

func (e *OpenChildrenError) Error() string {
	children := make([]string, len(e.Children))
	for i, child := range e.Children {
		children[i] = child.ID + " " + child.Status
	}
	return fmt.Sprintf("%s has %d open child seeds (%s); a plot closes after them: `attn seed replant %s` hands it back if your part is done, or close or move those seeds first", e.PlotID, len(children), strings.Join(children, ", "), e.PlotID)
}

func CheckPlotClose(seed Seed, openChildren []Seed) error {
	if len(openChildren) > 0 {
		return &OpenChildrenError{PlotID: seed.ID, Children: openChildren}
	}
	return nil
}
