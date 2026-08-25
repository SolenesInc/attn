package garden

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Ready, Blocked, Growing and Dormant do NOT partition the open set — a planted seed
// whose plot-mate holds the only open path is in none — so Total is carried.
type Progress struct {
	Total    int
	Done     int
	Withered int
	Growing  int
	Dormant  int
	Ready    int
	Blocked  int
}

func PlotProgress(seeds []Seed, crownID string, ready map[string]bool) Progress {
	blocked := blockedIDs(seeds)
	var p Progress
	for _, seed := range InPlot(seeds, crownID) {
		if seed.ID == crownID {
			continue
		}
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
		if !Closed(seed.Status) && blocked[seed.ID] {
			p.Blocked++
		}
	}
	return p
}

// Measured 2026-08-14 on production ticket activity: 276 gaps, p50 0.3h, p99 45h,
// max 356h. A week is a tripwire ~3.7x past the p99.
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
	Blocks []string `json:"blocks,omitempty"`
}

type PlotSpec struct {
	Title    string          `json:"title"`
	Body     string          `json:"body,omitempty"`
	Children []PlotChildSpec `json:"children"`
}

// Refuses unknown keys: a typo'd "block" would silently plant an unsequenced plot.
func ParsePlotSpec(raw []byte) (PlotSpec, error) {
	var spec PlotSpec
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return PlotSpec{}, fmt.Errorf("this is not a plot payload: %w (the shape is {\"title\": …, \"body\": …, \"children\": [{\"title\": …, \"blocks\": [\"<sibling step slug>\"]}]})", err)
	}
	if err := ValidatePlotSpec(spec); err != nil {
		return PlotSpec{}, err
	}
	return spec, nil
}

func ValidatePlotSpec(spec PlotSpec) error {
	if err := ValidatePlant(spec.Title, spec.Body); err != nil {
		return fmt.Errorf("crown: %w", err)
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
			return fmt.Errorf("children %d and %d both derive the step slug %q; blocks address siblings by slug, so retitle one", prev+1, i+1, slug)
		}
		slugs[slug] = i
	}
	for i, child := range spec.Children {
		for _, target := range child.Blocks {
			if _, known := slugs[strings.TrimSpace(target)]; !known {
				return fmt.Errorf("child %d blocks %q, which is no sibling's step slug; the slugs here are %s", i+1, target, strings.Join(slugList(spec.Children), ", "))
			}
		}
	}
	if from, to, cyclic := blocksCycle(spec.Children, slugs); cyclic {
		return fmt.Errorf("the blocks edges cycle through %q and %q, so neither child would ever be ready; sequence one way only", from, to)
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

func blocksCycle(children []PlotChildSpec, slugs map[string]int) (string, string, bool) {
	const unseen, visiting, done = 0, 1, 2
	state := make([]int, len(children))
	var walk func(i int) (string, string, bool)
	walk = func(i int) (string, string, bool) {
		state[i] = visiting
		for _, target := range children[i].Blocks {
			j := slugs[strings.TrimSpace(target)]
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
