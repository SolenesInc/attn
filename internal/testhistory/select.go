package testhistory

import (
	"fmt"
	"sort"
	"strings"
)

type Policy string

const (
	SuccessCount  Policy = "success-count/1"
	RecentFailure Policy = "recent-failure/1"
)

func ParsePolicy(name string) (Policy, error) {
	for _, policy := range []Policy{SuccessCount, RecentFailure} {
		if name == string(policy) || name == strings.Split(string(policy), "/")[0] {
			return policy, nil
		}
	}
	return "", fmt.Errorf("unknown policy %q; policies: %s, %s", name, SuccessCount, RecentFailure)
}

type Mode string

const (
	ModeBaseline Mode = "baseline"
	ModeReplace  Mode = "replace"
	ModeExtend   Mode = "extend"
)

func ParseMode(name string) (Mode, error) {
	for _, mode := range []Mode{ModeBaseline, ModeReplace, ModeExtend} {
		if name == string(mode) {
			return mode, nil
		}
	}
	return "", fmt.Errorf("unknown mode %q; modes: baseline, replace, extend", name)
}

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
	Policy       Policy
	Mode         Mode
	AllowanceMS  *int64
	Recipes      RecipeList
	Changed      []string
	ChangedTests []string
	Admissions   map[string]Admission
	Inventory    Inventory
	Health       Health
	Costs        Costs
}

type Status string

const (
	Selected    Status = "selected"
	OverBudget  Status = "over-budget"
	Unpriced    Status = "unpriced"
	Unavailable Status = "unavailable"
)

type Choice struct {
	Unit     string    `json:"unit"`
	Status   Status    `json:"status"`
	Changed  bool      `json:"changed,omitempty"`
	Baseline bool      `json:"baseline,omitempty"`
	Standing *Standing `json:"standing,omitempty"`
	CostMS   *int64    `json:"cost_ms,omitempty"`
	Priced   bool      `json:"priced"`
	Note     string    `json:"note,omitempty"`
	Command  string    `json:"command,omitempty"`
}

type RunnerBudget struct {
	Runner     Runner   `json:"runner"`
	BudgetMS   *int64   `json:"budget_ms,omitempty"`
	BaselineMS *int64   `json:"baseline_ms,omitempty"`
	EstimateMS int64    `json:"estimate_ms"`
	Unpriced   []string `json:"unpriced,omitempty"`
	Note       string   `json:"note,omitempty"`
}

type Selection struct {
	Policy  Policy         `json:"policy"`
	Mode    Mode           `json:"mode"`
	Groups  []string       `json:"groups"`
	Budgets []RunnerBudget `json:"budgets,omitempty"`
	Choices []Choice       `json:"choices"`
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
	groups := request.Recipes.SelectedGroups(request.Changed)
	selection := Selection{Policy: request.Policy, Mode: request.Mode, Groups: []string{}}
	choices := map[string]*Choice{}
	var order []string
	choose := func(unit string) *Choice {
		if choice := choices[unit]; choice != nil {
			return choice
		}
		choice := &Choice{Unit: unit, Command: RecipeCommand(unit)}
		if cost, ok := request.Costs.UnitMS(unit); ok {
			choice.CostMS = &cost
		}
		choice.Priced = request.Costs.Priced(unit)
		choices[unit] = choice
		order = append(order, unit)
		return choice
	}
	runners := map[Runner]bool{}
	for _, group := range groups {
		selection.Groups = append(selection.Groups, group.Name)
		for _, unit := range group.Units {
			choose(unit).Baseline = true
			runners[UnitRunner(unit)] = true
		}
	}
	if request.Mode != ModeBaseline {
		for _, unit := range request.ChangedTests {
			choose(unit).Changed = true
		}
		for _, unit := range request.Health.FailureUnits() {
			if runners[UnitRunner(unit)] {
				choose(unit)
			}
		}
	}

	var historical []*Choice
	for _, unit := range order {
		choice := choices[unit]
		var admission *Admission
		if found, ok := request.Admissions[unit]; ok {
			admission = &found
		}
		if standing, ok := request.Health.Standing(unit, admission); ok {
			choice.Standing = &standing
		}
		switch {
		case request.Inventory.Unavailable(unit) != "":
			choice.Status, choice.Note = Unavailable, request.Inventory.Unavailable(unit)
		case choice.Changed:
			choice.Status, choice.Note = Selected, "changed by this landing"
		case request.Mode == ModeBaseline:
			choice.Status = Selected
		default:
			historical = append(historical, choice)
		}
	}
	sort.SliceStable(historical, func(i, j int) bool {
		return request.Policy.ranksBefore(*historical[i], *historical[j])
	})

	if request.Mode != ModeBaseline {
		baseline := map[Runner][]string{}
		for _, unit := range order {
			if choice := choices[unit]; choice.Baseline && choice.Status != Unavailable {
				baseline[UnitRunner(unit)] = append(baseline[UnitRunner(unit)], unit)
			}
		}
		for _, runner := range []Runner{RunnerGo, RunnerVitest, RunnerPlaywright} {
			if runners[runner] {
				selection.Budgets = append(selection.Budgets, request.fit(runner, baseline[runner], historical))
			}
		}
	}
	placed := map[string]bool{}
	for _, choice := range historical {
		placed[choice.Unit] = true
	}
	for _, unit := range order {
		if !placed[unit] {
			selection.Choices = append(selection.Choices, *choices[unit])
		}
	}
	for _, choice := range historical {
		selection.Choices = append(selection.Choices, *choice)
	}
	sort.SliceStable(selection.Choices, func(i, j int) bool {
		return statusOrder[selection.Choices[i].Status] < statusOrder[selection.Choices[j].Status]
	})
	return selection
}

var statusOrder = map[Status]int{Selected: 0, OverBudget: 1, Unpriced: 2, Unavailable: 3}

func (request SelectRequest) fit(runner Runner, baseline []string, ranked []*Choice) RunnerBudget {
	var candidates []*Choice
	for _, choice := range ranked {
		if UnitRunner(choice.Unit) == runner {
			candidates = append(candidates, choice)
		}
	}
	budget := RunnerBudget{Runner: runner}
	baselineMS, unpriced := request.Costs.Estimate(baseline)
	if len(unpriced) == 0 {
		budget.BaselineMS = &baselineMS
	}
	budgeted := request.Mode == ModeReplace || request.AllowanceMS != nil
	switch {
	case budgeted && len(unpriced) > 0:
		budget.Note = fmt.Sprintf("no budget: %s has no cost measurement on %s; the baseline runs", unpriced[0], request.Costs.key.Machine)
	case request.Mode == ModeReplace:
		budget.BudgetMS = &baselineMS
	case request.AllowanceMS != nil:
		extended := baselineMS + *request.AllowanceMS
		budget.BudgetMS = &extended
	}
	limit := budget.BudgetMS

	var chosen []string
	keepBaseline := request.Mode == ModeExtend || budget.Note != ""
	for _, choice := range candidates {
		if keepBaseline && choice.Baseline {
			choice.Status = Selected
			chosen = append(chosen, choice.Unit)
		}
	}
	for _, choice := range candidates {
		if keepBaseline && choice.Baseline {
			continue
		}
		if !request.Costs.Priced(choice.Unit) {
			choice.Status = Unpriced
			choice.Note = "no cost measurement on " + request.Costs.key.Machine + "; run go tool test-history measure '" + choice.Unit + "'"
			continue
		}
		if budget.Note != "" {
			choice.Status, choice.Note = OverBudget, "the baseline runs without a budget"
			continue
		}
		estimate, _ := request.Costs.Estimate(append(append([]string(nil), chosen...), choice.Unit))
		if limit != nil && estimate > *limit {
			choice.Status = OverBudget
			continue
		}
		choice.Status = Selected
		chosen = append(chosen, choice.Unit)
	}
	budget.EstimateMS, budget.Unpriced = request.Costs.Estimate(chosen)
	return budget
}

func (policy Policy) ranksBefore(a, b Choice) bool {
	if policy == SuccessCount && a.Standing.PassesSince != b.Standing.PassesSince {
		return a.Standing.PassesSince < b.Standing.PassesSince
	}
	if a.Standing.Anchor.Position != b.Standing.Anchor.Position {
		return a.Standing.Anchor.Position > b.Standing.Anchor.Position
	}
	if costA, costB := costOrMax(a), costOrMax(b); costA != costB {
		return costA < costB
	}
	return a.Unit < b.Unit
}

func costOrMax(choice Choice) int64 {
	if choice.CostMS == nil {
		return 1 << 62
	}
	return *choice.CostMS
}

func (c Costs) UnitMS(unit string) (int64, bool) {
	return median(c.units[unit])
}
