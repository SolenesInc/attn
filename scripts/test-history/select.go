package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/testhistory"
)

type selectionContext struct {
	main     testhistory.Mainline
	mainRef  string
	spool    string
	lastSync *time.Time
	history  history
	key      testhistory.CostKey
	receipts []testhistory.Receipt
}

func loadContext(mainRef string) (selectionContext, error) {
	main, err := mainline(mainRef)
	if err != nil {
		return selectionContext{}, err
	}
	dir, err := spoolDir()
	if err != nil {
		return selectionContext{}, err
	}
	loaded, err := loadHistory(dir)
	if err != nil {
		return selectionContext{}, err
	}
	state, err := readSyncState(testhistory.Spool{Dir: dir})
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
	return selectionContext{main: main, mainRef: mainRef, spool: dir, lastSync: state.LastSuccess, history: loaded, key: selectionKey(), receipts: loaded.receipts}, nil
}

func (c selectionContext) request(at tree, base string, groupNames []string) (testhistory.SelectRequest, error) {
	recipes, err := at.recipes()
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
	groups := recipes.SelectedGroups(changed.paths)
	if groupNames != nil {
		if groups, err = recipes.Named(groupNames); err != nil {
			return testhistory.SelectRequest{}, err
		}
	}
	return testhistory.SelectRequest{
		Groups: groups, ChangedTests: changed.tests, Inventory: inventory,
		Health: testhistory.BuildHealth(c.receipts, c.main),
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
	LastSync       *time.Time     `json:"last_sync,omitempty"`
}

func (c selectionContext) freshness() freshness {
	f := freshness{Receipts: len(c.receipts), Skipped: c.history.skipped, MainRef: c.mainRef, Machine: c.key.Machine, Lane: c.key.Lane.String(), BehindHead: c.main.Len(), LastSync: c.lastSync}
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

type groupFlags []string

func (g *groupFlags) String() string        { return strings.Join(*g, " ") }
func (g *groupFlags) Set(name string) error { *g = append(*g, name); return nil }

func selectCommand(args []string) int {
	flags := flag.NewFlagSet("select", flag.ContinueOnError)
	mainRef := flags.String("main", "origin/main", "")
	asJSON := flags.Bool("json", false, "")
	var groups groupFlags
	flags.Var(&groups, "group", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	context, err := loadContext(*mainRef)
	if err != nil {
		return fail(err)
	}
	base, err := git("merge-base", *mainRef, "HEAD")
	if err != nil {
		return fail(fmt.Errorf("no merge base with %s: %v", *mainRef, err))
	}
	var names []string
	if len(groups) > 0 {
		names = groups
	}
	request, err := context.request(worktree, base, names)
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
	fmt.Fprintf(out, "test-history select: groups %s; a test that failed on main runs until it passes on %d main revisions\n", joinOrNone(report.Groups), report.ClearedAfter)
	printHistory(out, report.Freshness)
	for _, choice := range report.Choices {
		fmt.Fprintf(out, "%-11s %s\n", strings.ToUpper(string(choice.Status)), choice.Unit)
		for _, reason := range choiceReasons(choice) {
			fmt.Fprintf(out, "            %s\n", reason)
		}
	}
}

func printHistory(out io.Writer, f freshness) {
	evidence := "no main evidence yet"
	if f.NewestEvidence != "" {
		evidence = fmt.Sprintf("newest main evidence %s, %d commits behind %s", short(f.NewestEvidence), f.BehindHead, f.MainRef)
	}
	synced := "never synced"
	if f.LastSync != nil {
		synced = fmt.Sprintf("last sync %s (%s ago)", f.LastSync.Local().Format(time.RFC3339), time.Since(*f.LastSync).Round(time.Minute))
	}
	fmt.Fprintf(out, "history: %d receipts, %s; %s\n", f.Receipts, synced, evidence)
	for reason, count := range f.Skipped {
		fmt.Fprintf(out, "history: skipped %d receipts (%s)\n", count, reason)
	}
}

func choiceReasons(choice testhistory.Choice) []string {
	var reasons []string
	switch {
	case choice.Baseline:
		reasons = append(reasons, "in the baseline")
	case choice.Changed:
		reasons = append(reasons, "changed by this landing")
	}
	if choice.Standing != nil {
		reason := choice.Standing.Describe()
		if left := choice.PassesLeft(); left > 0 && choice.Status == testhistory.Selected {
			reason = fmt.Sprintf("%s; passed on %d main revisions since, runs until %d more pass", choice.Standing.Failure(), choice.Standing.PassesSince, left)
		}
		reasons = append(reasons, reason)
	}
	if choice.Note != "" {
		reasons = append(reasons, choice.Note)
	}
	return reasons
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
	Unit         string                `json:"unit"`
	Available    bool                  `json:"available"`
	Note         string                `json:"note,omitempty"`
	Standing     *testhistory.Standing `json:"standing,omitempty"`
	ClearedAfter int                   `json:"cleared_after"`
	CostMS       *int64                `json:"cost_ms,omitempty"`
	CostSamples  int                   `json:"cost_samples"`
	Command      string                `json:"command,omitempty"`
	Freshness    freshness             `json:"freshness"`
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
	inventory, err := worktree.inventory()
	if err != nil {
		return fail(err)
	}
	costs := testhistory.BuildCosts(context.receipts, context.key)
	result := explanation{Unit: *unit, ClearedAfter: testhistory.ClearedAfter, Command: testhistory.RecipeCommand(*unit), Freshness: context.freshness()}
	result.Note = inventory.Unavailable(*unit)
	result.Available = result.Note == ""
	if standing, ok := testhistory.BuildHealth(context.receipts, context.main).Standing(*unit); ok {
		result.Standing = &standing
	}
	if cost, ok := costs.UnitMS(*unit); ok {
		result.CostMS = &cost
	}
	result.CostSamples = costs.Samples(*unit)
	if *asJSON {
		return printJSON(result)
	}
	fmt.Println(result.Unit)
	if !result.Available {
		fmt.Println("  unavailable: " + result.Note)
	}
	if result.Standing == nil {
		fmt.Println("  no failure on main: landings run it only from the baseline or when they change it")
	} else {
		for _, lane := range result.Standing.Lanes {
			fmt.Printf("  %s: %s\n", lane.Lane, lane.Describe())
			if len(lane.Anchor.Evidence) > 0 {
				fmt.Printf("    failure receipts: %s\n", strings.Join(lane.Anchor.Evidence, " "))
			}
			for _, commit := range lane.PassRevisions {
				fmt.Printf("    pass at %s\n", short(commit))
			}
		}
		if left := result.ClearedAfter - result.Standing.PassesSince; left > 0 {
			fmt.Printf("  landings run it until it passes on %d more main revisions in %s\n", left, result.Standing.Lane)
		} else {
			fmt.Printf("  cleared: %d passing main revisions in its least cleared lane, %s\n", result.Standing.PassesSince, result.Standing.Lane)
		}
	}
	if result.CostMS != nil {
		fmt.Printf("  cost on %s: %s, median of %d samples\n", context.key.Machine, seconds(*result.CostMS), result.CostSamples)
	}
	if result.Command != "" {
		fmt.Println("  rerun: " + result.Command)
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
	Groups      []string `json:"groups"`
	Selected    []string `json:"selected"`
	Unavailable []string `json:"unavailable,omitempty"`
	EstimateMS  int64    `json:"estimate_ms"`
	Unpriced    []string `json:"unpriced,omitempty"`
	Included    []string `json:"included"`
	Missed      []string `json:"missed"`
}

type actualLanding struct {
	WallMS       int64    `json:"wall_ms"`
	Components   int      `json:"components"`
	Attempts     int      `json:"attempts"`
	Missing      []string `json:"missing,omitempty"`
	Unattributed int      `json:"unattributed"`
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
	before := selectionContext{main: c.main.Before(commit), mainRef: commit + "^", spool: c.spool, history: c.history, key: c.key, receipts: c.history.availableAt(at)}
	point.Available = len(before.receipts)
	point.Freshness = before.freshness()
	recent, err := before.request(tree{revision: commit}, commit+"^", nil)
	if err != nil {
		return replayPoint{}, err
	}
	point.Changed = recent.ChangedTests
	costs := testhistory.BuildCosts(before.receipts, c.key)
	baseline := testhistory.SelectRequest{Groups: recent.Groups, Inventory: recent.Inventory}
	for name, request := range map[string]testhistory.SelectRequest{"baseline": baseline, "landing": recent} {
		selection := testhistory.Select(request)
		result := replaySelection{
			Groups: selection.Groups, Selected: selection.Units(testhistory.Selected), Unavailable: selection.Units(testhistory.Unavailable),
			Included: []string{}, Missed: []string{},
		}
		result.EstimateMS, result.Unpriced = costs.Estimate(result.Selected)
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
		point.Selections[name] = result
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
