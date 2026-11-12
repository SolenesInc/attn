package testhistory

import (
	"fmt"
	"sort"
	"strings"
)

// Of 50 tests that failed on main again after passing there, across 312 main
// commits of CI history (bc2d33c36..69e37edd9, 2026-10-31), 40 did so within
// 30 passing revisions; 20 caught 34, 50 caught 44. At 30 a landing added a
// median of 7 tests, at most 36; on a 10-core Mac the largest set took the Go
// landing tests from 20.6s to 38.3s.
const ClearedAfter = 30

const VitestSuiteUnit = "vitest ."

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

func CargoTestUnits(repoPath string) []string {
	rest, ok := strings.CutPrefix(repoPath, "native/crates/")
	if !ok {
		return nil
	}
	pkg, file, ok := strings.Cut(rest, "/")
	if !ok {
		return nil
	}
	switch {
	case file == "src/lib.rs":
		return []string{"cargo " + pkg + " --lib", "cargo " + pkg + " --doc"}
	case file == "src/main.rs":
		return []string{"cargo " + pkg + " --bin " + pkg}
	case strings.HasPrefix(file, "src/bin/") && strings.HasSuffix(file, ".rs") && !strings.Contains(strings.TrimPrefix(file, "src/bin/"), "/"):
		return []string{"cargo " + pkg + " --bin " + strings.TrimSuffix(strings.TrimPrefix(file, "src/bin/"), ".rs")}
	}
	target, ok := strings.CutPrefix(file, "tests/")
	if !ok {
		return nil
	}
	if dir, ok := strings.CutSuffix(target, "/main.rs"); ok && !strings.Contains(dir, "/") {
		return []string{"cargo " + pkg + " --test " + dir}
	}
	if name, ok := strings.CutSuffix(target, ".rs"); ok && !strings.Contains(name, "/") {
		return []string{"cargo " + pkg + " --test " + name}
	}
	return nil
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

func (inventory Inventory) Has(unit string) bool {
	_, present := inventory[unit]
	return present
}

func (inventory Inventory) Unavailable(unit string) string {
	switch {
	case UnitRunner(unit) == RunnerNative:
		return "native scenarios have no landing recipe; broad CI runs them"
	}
	runs, present := inventory[unit]
	switch {
	case !present:
		return "not in this checkout: renamed, removed, or not a test"
	case !runs:
		return "its test file does not build on this platform"
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
	Red         Status = "red"
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
	for _, group := range request.Groups {
		selection.Groups = append(selection.Groups, group.Name)
		for _, unit := range group.Units {
			choose(unit).Baseline = true
		}
	}
	if len(request.Groups) == 0 {
		return selection
	}
	for _, unit := range request.ChangedTests {
		choose(unit).Changed = true
	}
	for _, unit := range request.Health.FailureUnits() {
		choose(unit)
	}
	for _, unit := range order {
		choice := choices[unit]
		switch {
		case !choice.Baseline && !choice.Changed && choice.PassesLeft() == 0:
			choice.Status = Cleared
			choice.Note = fmt.Sprintf("passed on %d main revisions since, %d clear it", choice.Standing.PassesSince, ClearedAfter)
		case choice.Baseline && UnitRunner(unit) != RunnerNative && !request.Inventory.Has(unit):
			choice.Status, choice.Note = Missing, "not in this checkout; requested anyway, so the run fails until its baseline line names a test"
		case !choice.Changed && UnitRunner(unit) != RunnerNative && !request.Inventory.Has(unit):
			choice.Status, choice.Note = Missing, "failed on main, and no longer in this checkout"
		case request.Inventory.Unavailable(unit) != "":
			choice.Status, choice.Note = Unavailable, request.Inventory.Unavailable(unit)
		case !choice.Baseline && !choice.Changed && choice.Standing.PassesSince == 0:
			choice.Status, choice.Note = Red, "still red on main: the sheriff owns it; landings run it once it passes on main"
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

var statusOrder = map[Status]int{Selected: 0, Red: 1, Unavailable: 2, Missing: 3, Cleared: 4}

func (c Costs) UnitMS(unit string) (int64, bool) {
	return median(c.units[unit])
}
