package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/testhistory"
)

type selectionContext struct {
	main     testhistory.Mainline
	mainRef  string
	history  history
	key      testhistory.CostKey
	receipts []testhistory.Receipt
}

func loadContext(mainRef string) (selectionContext, error) {
	main, err := mainline(mainRef)
	if err != nil {
		return selectionContext{}, err
	}
	loaded, err := loadHistory()
	if err != nil {
		return selectionContext{}, err
	}
	root, err := repoRoot()
	if err != nil {
		return selectionContext{}, err
	}
	if err := os.Chdir(root); err != nil {
		return selectionContext{}, err
	}
	return selectionContext{main: main, mainRef: mainRef, history: loaded, key: selectionKey(), receipts: loaded.receipts}, nil
}

func (c selectionContext) request(at tree, base string, policy testhistory.Policy, mode testhistory.Mode, allowance *int64) (testhistory.SelectRequest, error) {
	recipes, err := at.recipes()
	if err != nil {
		return testhistory.SelectRequest{}, err
	}
	admissions, err := at.admissions(recipes, c.main)
	if err != nil {
		return testhistory.SelectRequest{}, err
	}
	inventory, err := at.inventory()
	if err != nil {
		return testhistory.SelectRequest{}, err
	}
	changed, err := changesBetween(base, at)
	if err != nil {
		return testhistory.SelectRequest{}, err
	}
	return testhistory.SelectRequest{
		Policy: policy, Mode: mode, AllowanceMS: allowance,
		Recipes: recipes, Changed: changed.paths, ChangedTests: changed.tests,
		Admissions: admissions, Inventory: inventory,
		Health: testhistory.BuildHealth(c.receipts, c.main),
		Costs:  testhistory.BuildCosts(c.receipts, c.key),
	}, nil
}

type freshness struct {
	Receipts       int            `json:"receipts"`
	Skipped        map[string]int `json:"skipped,omitempty"`
	MainRef        string         `json:"main_ref"`
	MainHead       string         `json:"main_head"`
	NewestEvidence string         `json:"newest_evidence,omitempty"`
	BehindHead     int            `json:"behind_head"`
	Machine        string         `json:"machine"`
	Lane           string         `json:"lane"`
}

func (c selectionContext) freshness() freshness {
	f := freshness{Receipts: len(c.receipts), Skipped: c.history.skipped, MainRef: c.mainRef, Machine: c.key.Machine, Lane: c.key.Lane.String(), BehindHead: c.main.Len()}
	if c.main.Len() > 0 {
		f.MainHead = c.main.Commit(c.main.Len() - 1)
	}
	newest := -1
	for _, receipt := range c.receipts {
		if position, ok := testhistory.HealthEvidence(receipt, c.main); ok && position > newest {
			newest = position
		}
	}
	if newest >= 0 {
		f.NewestEvidence = c.main.Commit(newest)
		f.BehindHead = c.main.Len() - 1 - newest
	}
	return f
}

type selectReport struct {
	testhistory.Selection
	Freshness freshness `json:"freshness"`
}

func selectCommand(args []string) int {
	flags := flag.NewFlagSet("select", flag.ContinueOnError)
	mainRef := flags.String("main", "origin/main", "")
	modeName := flags.String("mode", "replace", "")
	policyName := flags.String("policy", string(testhistory.SuccessCount), "")
	allowance := flags.Duration("allowance", 0, "")
	asJSON := flags.Bool("json", false, "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	mode, err := testhistory.ParseMode(*modeName)
	if err != nil {
		return fail(err)
	}
	policy, err := testhistory.ParsePolicy(*policyName)
	if err != nil {
		return fail(err)
	}
	if *allowance != 0 && mode != testhistory.ModeExtend {
		return fail(errors.New("--allowance applies only to --mode extend"))
	}
	var allowanceMS *int64
	if *allowance > 0 {
		ms := allowance.Milliseconds()
		allowanceMS = &ms
	}
	context, err := loadContext(*mainRef)
	if err != nil {
		return fail(err)
	}
	base, err := git("merge-base", *mainRef, "HEAD")
	if err != nil {
		return fail(fmt.Errorf("no merge base with %s: %v", *mainRef, err))
	}
	request, err := context.request(worktree, base, policy, mode, allowanceMS)
	if err != nil {
		return fail(err)
	}
	report := selectReport{Selection: testhistory.Select(request), Freshness: context.freshness()}
	if *asJSON {
		return printJSON(report)
	}
	printSelection(os.Stdout, report)
	return 0
}

func printSelection(out io.Writer, report selectReport) {
	f := report.Freshness
	fmt.Fprintf(out, "test-history select: %s, mode %s, groups %s\n", report.Policy, report.Mode, joinOrNone(report.Groups))
	evidence := "no main evidence yet"
	if f.NewestEvidence != "" {
		evidence = fmt.Sprintf("newest main evidence %s, %d commits behind %s", short(f.NewestEvidence), f.BehindHead, f.MainRef)
	}
	fmt.Fprintf(out, "history: %d receipts; %s; costs for %s on %s\n", f.Receipts, evidence, f.Lane, f.Machine)
	for reason, count := range f.Skipped {
		fmt.Fprintf(out, "history: skipped %d receipts (%s)\n", count, reason)
	}
	for _, budget := range report.Budgets {
		line := fmt.Sprintf("budget %s: selected %s", budget.Runner, seconds(budget.EstimateMS))
		if budget.BaselineMS != nil {
			line += ", baseline " + seconds(*budget.BaselineMS)
		}
		if budget.BudgetMS != nil {
			line += ", limit " + seconds(*budget.BudgetMS)
		}
		if len(budget.Unpriced) > 0 {
			line += "; unpriced: " + strings.Join(budget.Unpriced, ", ")
		}
		if budget.Note != "" {
			line += "; " + budget.Note
		}
		fmt.Fprintln(out, line)
	}
	for _, choice := range report.Choices {
		fmt.Fprintf(out, "%-11s %s", strings.ToUpper(string(choice.Status)), choice.Unit)
		if choice.CostMS != nil {
			fmt.Fprintf(out, " (%s)", seconds(*choice.CostMS))
		}
		if choice.Status == testhistory.Selected && !choice.Priced {
			fmt.Fprint(out, " (unpriced)")
		}
		fmt.Fprintln(out)
		var reasons []string
		if choice.Changed {
			reasons = append(reasons, "changed by this landing")
		}
		if choice.Standing != nil {
			reasons = append(reasons, choice.Standing.Describe())
		}
		if choice.Note != "" && !choice.Changed {
			reasons = append(reasons, choice.Note)
		}
		for _, reason := range reasons {
			fmt.Fprintf(out, "            %s\n", reason)
		}
	}
}

func joinOrNone(list []string) string {
	if len(list) == 0 {
		return "(none)"
	}
	return strings.Join(list, " ")
}

func seconds(ms int64) string { return strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64) + "s" }

func short(commit string) string {
	if len(commit) > 9 {
		return commit[:9]
	}
	return commit
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "test-history:", err)
	return 1
}

func printJSON(value any) int {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fail(err)
	}
	return 0
}

type explanation struct {
	Unit        string                `json:"unit"`
	Available   bool                  `json:"available"`
	Note        string                `json:"note,omitempty"`
	Baseline    bool                  `json:"baseline"`
	Standing    *testhistory.Standing `json:"standing,omitempty"`
	CostMS      *int64                `json:"cost_ms,omitempty"`
	CostSamples int                   `json:"cost_samples"`
	Priced      bool                  `json:"priced"`
	Command     string                `json:"command,omitempty"`
	Policy      testhistory.Policy    `json:"policy"`
	Freshness   freshness             `json:"freshness"`
}

func explainCommand(args []string) int {
	flags := flag.NewFlagSet("explain", flag.ContinueOnError)
	mainRef := flags.String("main", "origin/main", "")
	unit := flags.String("test", "", "")
	asJSON := flags.Bool("json", false, "")
	if err := flags.Parse(args); err != nil || *unit == "" || flags.NArg() != 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	context, err := loadContext(*mainRef)
	if err != nil {
		return fail(err)
	}
	request, err := context.request(worktree, context.mainRef, testhistory.SuccessCount, testhistory.ModeReplace, nil)
	if err != nil {
		return fail(err)
	}
	result := explanation{Unit: *unit, Policy: testhistory.SuccessCount, Command: testhistory.RecipeCommand(*unit), Freshness: context.freshness()}
	result.Note = request.Inventory.Unavailable(*unit)
	result.Available = result.Note == ""
	var admission *testhistory.Admission
	if found, ok := request.Admissions[*unit]; ok {
		result.Baseline, admission = true, &found
	}
	if standing, ok := request.Health.Standing(*unit, admission); ok {
		result.Standing = &standing
	}
	if cost, ok := request.Costs.UnitMS(*unit); ok {
		result.CostMS = &cost
	}
	result.CostSamples = request.Costs.Samples(*unit)
	result.Priced = request.Costs.Priced(*unit)
	if *asJSON {
		return printJSON(result)
	}
	fmt.Printf("%s (%s)\n", result.Unit, result.Policy)
	switch {
	case !result.Available:
		fmt.Println("  unavailable: " + result.Note)
	case result.Baseline:
		fmt.Println("  in the baseline list")
	}
	if result.Standing == nil {
		fmt.Println("  no failure on main and not in the baseline: not a historical candidate")
	} else {
		for _, lane := range append([]testhistory.LaneStanding{}, result.Standing.Lanes...) {
			fmt.Printf("  %s: %s\n", lane.Lane, lane.Describe())
			if len(lane.Anchor.Evidence) > 0 {
				fmt.Printf("    failure receipts: %s\n", strings.Join(lane.Anchor.Evidence, " "))
			}
			for _, commit := range lane.PassRevisions {
				fmt.Printf("    pass at %s\n", short(commit))
			}
		}
		if len(result.Standing.Lanes) == 0 {
			fmt.Println("  " + result.Standing.Describe() + "; no observations yet")
		} else {
			fmt.Printf("  ranks by %s, its least cleared lane\n", result.Standing.Lane)
		}
	}
	switch {
	case result.Priced:
		fmt.Printf("  cost on %s: %s, median of %d samples\n", context.key.Machine, seconds(*result.CostMS), result.CostSamples)
	default:
		fmt.Printf("  cost on %s: unpriced; measure with: go tool test-history measure '%s'\n", context.key.Machine, result.Unit)
	}
	if result.Command != "" {
		fmt.Println("  rerun: " + result.Command)
	}
	return 0
}

type pendingUnit struct {
	Unit    string `json:"unit"`
	Reason  string `json:"reason"`
	Measure string `json:"measure,omitempty"`
}

func pendingCommand(args []string) int {
	flags := flag.NewFlagSet("pending", flag.ContinueOnError)
	mainRef := flags.String("main", "origin/main", "")
	asJSON := flags.Bool("json", false, "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	context, err := loadContext(*mainRef)
	if err != nil {
		return fail(err)
	}
	request, err := context.request(worktree, context.mainRef, testhistory.SuccessCount, testhistory.ModeReplace, nil)
	if err != nil {
		return fail(err)
	}
	candidates := map[string]bool{}
	for _, recipe := range request.Recipes.Recipes {
		candidates[recipe.Unit] = true
	}
	for _, unit := range request.Health.FailureUnits() {
		candidates[unit] = true
	}
	pending := []pendingUnit{}
	for _, unit := range keys(candidates) {
		switch {
		case request.Inventory.Unavailable(unit) != "":
			pending = append(pending, pendingUnit{Unit: unit, Reason: "unavailable: " + request.Inventory.Unavailable(unit)})
		case !request.Costs.Priced(unit):
			pending = append(pending, pendingUnit{Unit: unit, Reason: "unpriced on " + context.key.Machine, Measure: "go tool test-history measure '" + unit + "'"})
		}
	}
	if *asJSON {
		return printJSON(pending)
	}
	for _, unit := range pending {
		fmt.Printf("%s\n  %s\n", unit.Unit, unit.Reason)
		if unit.Measure != "" {
			fmt.Printf("  %s\n", unit.Measure)
		}
	}
	if len(pending) == 0 {
		fmt.Println("no pending candidates")
	}
	return 0
}

func measureCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	root, err := repoRoot()
	if err != nil {
		return fail(err)
	}
	command := []string{root + "/scripts/landing-tests.sh"}
	for _, unit := range args {
		command = append(command, "--unit", unit)
	}
	run := exec.Command(command[0], command[1:]...)
	run.Dir, run.Stdin, run.Stdout, run.Stderr = root, os.Stdin, os.Stdout, os.Stderr
	if err := run.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return fail(err)
	}
	return 0
}

type replayPoint struct {
	Commit     string                     `json:"commit"`
	SelectedAt time.Time                  `json:"selected_at"`
	Available  int                        `json:"available_receipts"`
	Observed   int                        `json:"observed_receipts"`
	Failures   map[string][]string        `json:"failures"`
	Changed    []string                   `json:"changed_tests"`
	Selections map[string]replaySelection `json:"selections"`
	Actual     *actualLanding             `json:"actual_landing,omitempty"`
	Freshness  freshness                  `json:"freshness"`
}

type replaySelection struct {
	Policy     testhistory.Policy         `json:"policy"`
	Groups     []string                   `json:"groups"`
	Budgets    []testhistory.RunnerBudget `json:"budgets,omitempty"`
	Selected   []string                   `json:"selected"`
	Pending    []string                   `json:"pending,omitempty"`
	EstimateMS int64                      `json:"estimate_ms"`
	Unpriced   []string                   `json:"unpriced,omitempty"`
	Included   []string                   `json:"included"`
	Missed     []string                   `json:"missed"`
}

type actualLanding struct {
	WallMS       int64    `json:"wall_ms"`
	Components   int      `json:"components"`
	Attempts     int      `json:"attempts"`
	Missing      []string `json:"missing,omitempty"`
	Unattributed int      `json:"unattributed"`
}

var replayStrategies = []struct {
	name   string
	policy testhistory.Policy
	mode   testhistory.Mode
}{
	{"baseline", testhistory.SuccessCount, testhistory.ModeBaseline},
	{"success-count", testhistory.SuccessCount, testhistory.ModeReplace},
	{"recent-failure", testhistory.RecentFailure, testhistory.ModeReplace},
	{"extend-all", testhistory.SuccessCount, testhistory.ModeExtend},
}

func replayCommand(args []string) int {
	flags := flag.NewFlagSet("replay", flag.ContinueOnError)
	mainRef := flags.String("main", "origin/main", "")
	from := flags.String("from", "", "")
	machine := flags.String("machine", "", "")
	if err := flags.Parse(args); err != nil || *from == "" || flags.NArg() != 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	context, err := loadContext(*mainRef)
	if err != nil {
		return fail(err)
	}
	if *machine != "" {
		context.key.Machine = *machine
	}
	log, err := git("log", "--first-parent", "--reverse", "--format=%H %ct", *from+".."+*mainRef)
	if err != nil {
		return fail(err)
	}
	observed := testhistory.BuildHealth(context.receipts, context.main)
	encoder := json.NewEncoder(os.Stdout)
	for _, line := range strings.Split(log, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		unix, _ := strconv.ParseInt(fields[1], 10, 64)
		point, err := context.replayAt(observed, fields[0], time.Unix(unix, 0).UTC())
		if err != nil {
			return fail(fmt.Errorf("replay at %s: %w", short(fields[0]), err))
		}
		if err := encoder.Encode(point); err != nil {
			return fail(err)
		}
	}
	return 0
}

func (c selectionContext) replayAt(observed testhistory.Health, commit string, at time.Time) (replayPoint, error) {
	point := replayPoint{Commit: commit, SelectedAt: at, Selections: map[string]replaySelection{}}
	position, _ := c.main.Position(commit)
	point.Failures = observed.FailedAt(position)
	var landed []testhistory.Receipt
	for _, receipt := range c.receipts {
		evidence, ok := testhistory.HealthEvidence(receipt, c.main)
		if !ok || evidence != position {
			continue
		}
		point.Observed++
		landed = append(landed, receipt)
	}
	before := selectionContext{main: c.main.Before(commit), mainRef: commit + "^", history: c.history, key: c.key, receipts: c.history.availableAt(at)}
	point.Available = len(before.receipts)
	point.Freshness = before.freshness()
	shared, err := before.request(tree{revision: commit}, commit+"^", testhistory.SuccessCount, testhistory.ModeBaseline, nil)
	if err != nil {
		return replayPoint{}, err
	}
	point.Changed = shared.ChangedTests
	for _, strategy := range replayStrategies {
		request := shared
		request.Policy, request.Mode = strategy.policy, strategy.mode
		selection := testhistory.Select(request)
		result := replaySelection{
			Policy: selection.Policy, Groups: selection.Groups, Budgets: selection.Budgets,
			Selected: selection.Units(testhistory.Selected), Included: []string{}, Missed: []string{},
			Pending: append(selection.Units(testhistory.Unpriced), selection.Units(testhistory.Unavailable)...),
		}
		result.EstimateMS, result.Unpriced = request.Costs.Estimate(result.Selected)
		selected := map[string]bool{}
		for _, unit := range result.Selected {
			selected[unit] = true
		}
		for _, unit := range keys(point.Failures) {
			if selected[unit] {
				result.Included = append(result.Included, unit)
			} else {
				result.Missed = append(result.Missed, unit)
			}
		}
		point.Selections[strategy.name] = result
	}
	point.Actual = c.actualBaseline(landed, point.Selections["baseline"].Selected)
	return point, nil
}

func (c selectionContext) actualBaseline(receipts []testhistory.Receipt, baseline []string) *actualLanding {
	if len(baseline) == 0 {
		return nil
	}
	components := map[string][]int64{}
	var goUnits []string
	for _, unit := range baseline {
		if testhistory.UnitRunner(unit) == testhistory.RunnerGo {
			goUnits = append(goUnits, unit)
		} else {
			components[unit] = nil
		}
	}
	sort.Strings(goUnits)
	if len(goUnits) > 0 {
		components[strings.Join(goUnits, "\n")] = nil
	}
	actual := &actualLanding{Components: len(components)}
	for _, receipt := range receipts {
		if len(receipt.Command.RequestedUnits) == 0 || receipt.Diagnostics.MachineClass != c.key.Machine || receipt.Command.WallMS == nil {
			continue
		}
		requested := append([]string(nil), receipt.Command.RequestedUnits...)
		sort.Strings(requested)
		key := strings.Join(requested, "\n")
		if _, baselineRun := components[key]; !baselineRun {
			actual.Unattributed++
			continue
		}
		components[key] = append(components[key], *receipt.Command.WallMS)
		actual.Attempts++
	}
	for key, walls := range components {
		if len(walls) == 0 {
			actual.Missing = append(actual.Missing, strings.ReplaceAll(key, "\n", ", "))
			continue
		}
		sort.Slice(walls, func(i, j int) bool { return walls[i] < walls[j] })
		actual.WallMS += walls[len(walls)/2]
	}
	sort.Strings(actual.Missing)
	if actual.Attempts == 0 && actual.Unattributed == 0 {
		return nil
	}
	return actual
}
