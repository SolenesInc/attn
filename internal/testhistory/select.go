package testhistory

import (
	"fmt"
	"sort"
	"strings"
)

// A failing test returned within 30 later passing main revisions in 87 of 97
// recurrences across 312 main commits of CI history (bc2d33c36..69e37edd9,
// 2026-10-31); 20 caught 81, 50 caught 91. At 30, a landing carried a median
// of 7 such tests, at most 37.
const ClearedAfter = 30

func AppTestUnit(repoPath string) (string, bool) {
	file, ok := strings.CutPrefix(repoPath, "app/")
	if !ok {
		return "", false
	}
	if strings.HasPrefix(file, "e2e/") && strings.HasSuffix(file, ".spec.ts") {
		return "playwright " + file, true
	}
	for _, root := range vitestInclude {
		if strings.HasPrefix(file, root.dir) && hasAnySuffix(file, root.suffixes) {
			return "vitest " + file, true
		}
	}
	return "", false
}

var vitestInclude = []struct {
	dir      string
	suffixes []string
}{
	{"src/", []string{".test.ts", ".test.tsx", ".test.mjs"}},
	{"scripts/real-app-harness/", []string{".test.ts", ".test.mjs"}},
	{"lint/", []string{".test.ts"}},
}

func hasAnySuffix(name string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

type Inventory map[string]bool

func (inventory Inventory) Unavailable(unit string) string {
	switch {
	case UnitRunner(unit) == RunnerNative:
		return "native scenarios have no landing recipe; broad CI runs them"
	case !inventory[unit]:
		return "not in this checkout: renamed, removed, or not a test"
	}
	return ""
}

type SelectRequest struct {
	Groups       []Group
	ChangedTests []string
	Inventory    Inventory
	Health       Health
}

type Status string

const (
	Selected    Status = "selected"
	Cleared     Status = "cleared"
	Unavailable Status = "unavailable"
	Missing     Status = "missing"
)

type Choice struct {
	Unit     string    `json:"unit"`
	Status   Status    `json:"status"`
	Baseline bool      `json:"baseline,omitempty"`
	Changed  bool      `json:"changed,omitempty"`
	Standing *Standing `json:"standing,omitempty"`
	Note     string    `json:"note,omitempty"`
	Command  string    `json:"command,omitempty"`
}

func (c Choice) PassesLeft() int {
	if c.Standing == nil {
		return 0
	}
	return max(0, ClearedAfter-c.Standing.PassesSince)
}

type Selection struct {
	ClearedAfter int      `json:"cleared_after"`
	Groups       []string `json:"groups"`
	Choices      []Choice `json:"choices"`
}

func (s Selection) Units(status Status) []string {
	var units []string
	for _, choice := range s.Choices {
		if choice.Status == status {
			units = append(units, choice.Unit)
		}
	}
	return units
}

func Select(request SelectRequest) Selection {
	selection := Selection{ClearedAfter: ClearedAfter, Groups: []string{}}
	choices := map[string]*Choice{}
	var order []string
	choose := func(unit string) *Choice {
		if choice := choices[unit]; choice != nil {
			return choice
		}
		choice := &Choice{Unit: unit, Command: RecipeCommand(unit)}
		if standing, ok := request.Health.Standing(unit); ok {
			choice.Standing = &standing
		}
		choices[unit] = choice
		order = append(order, unit)
		return choice
	}
	runners := map[Runner]bool{}
	for _, group := range request.Groups {
		selection.Groups = append(selection.Groups, group.Name)
		for _, unit := range group.Units {
			choose(unit).Baseline = true
			runners[UnitRunner(unit)] = true
		}
	}
	if len(request.Groups) == 0 {
		return selection
	}
	for _, unit := range request.ChangedTests {
		choose(unit).Changed = true
	}
	for _, unit := range request.Health.FailureUnits() {
		if runners[UnitRunner(unit)] || UnitRunner(unit) == RunnerNative {
			choose(unit)
		}
	}
	for _, unit := range order {
		choice := choices[unit]
		switch {
		case !choice.Baseline && !choice.Changed && choice.PassesLeft() == 0:
			choice.Status = Cleared
			choice.Note = fmt.Sprintf("passed on %d main revisions since, %d clear it", choice.Standing.PassesSince, ClearedAfter)
		case !choice.Baseline && !choice.Changed && UnitRunner(unit) != RunnerNative && !request.Inventory[unit]:
			choice.Status, choice.Note = Missing, "failed on main, and no longer in this checkout"
		case request.Inventory.Unavailable(unit) != "":
			choice.Status, choice.Note = Unavailable, request.Inventory.Unavailable(unit)
		default:
			choice.Status = Selected
		}
		selection.Choices = append(selection.Choices, *choice)
	}
	sort.SliceStable(selection.Choices, func(i, j int) bool {
		return statusOrder[selection.Choices[i].Status] < statusOrder[selection.Choices[j].Status]
	})
	return selection
}

var statusOrder = map[Status]int{Selected: 0, Unavailable: 1, Missing: 2, Cleared: 3}

func (c Costs) UnitMS(unit string) (int64, bool) {
	return median(c.units[unit])
}
