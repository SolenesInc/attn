package testhistory_test

import (
	"fmt"
	"reflect"
	"testing"

	"pgregory.net/rapid"

	th "github.com/victorarias/attn/internal/testhistory"
)

var (
	mac       = th.Lane{OS: "darwin", Arch: "arm64"}
	linux     = th.Lane{OS: "linux", Arch: "amd64"}
	raceLinux = th.Lane{OS: "linux", Arch: "amd64", Race: true}
)

const machine = "local 10cpu/32GB"

func commits(n int) []string {
	list := make([]string, n)
	for i := range list {
		list[i] = fmt.Sprintf("c%02d", i)
	}
	return list
}

type at struct {
	commit string
	lane   th.Lane
	dirty  bool
}

func on(commit string, lane th.Lane, dirty ...bool) at {
	return at{commit: commit, lane: lane, dirty: len(dirty) > 0 && dirty[0]}
}

func receipt(where at, results ...th.Result) th.Receipt {
	r := th.Receipt{
		Schema: th.Schema, InvocationID: fmt.Sprintf("%s-%s-%d", where.commit, where.lane, len(results)),
		Revision:    th.Revision{Commit: where.commit},
		Lane:        where.lane,
		Runner:      th.RunnerVitest,
		Diagnostics: th.Diagnostics{MachineClass: machine},
		Results:     results,
	}
	if len(results) > 0 {
		r.Runner = th.UnitRunner(results[0].Unit)
	}
	if where.dirty {
		r.Revision.DirtyFingerprint = "sha256:dirty"
	}
	return r
}

func result(unit string, outcome th.Outcome) th.Result {
	return th.Result{Unit: unit, Attempt: 1, Outcome: outcome}
}

func caseResult(unit, name string, outcome th.Outcome) th.Result {
	return th.Result{Unit: unit, Case: name, Attempt: 1, Outcome: outcome}
}

func cached(unit string) th.Result {
	return th.Result{Unit: unit, Attempt: 1, Outcome: th.Pass, Cached: true}
}

const unit = "vitest src/a.test.tsx"

func TestStandingFollowsTheSpecification(t *testing.T) {
	main := th.NewMainline(commits(6))
	admittedAt1 := main.Admission("c01")
	cases := []struct {
		name      string
		unit      string
		receipts  []th.Receipt
		admission *th.Admission
		standing  bool
		kind      th.AnchorKind
		anchor    string
		lane      string
		passes    int
		cases     []string
	}{
		{name: "an admission with no observations stands with no passes",
			admission: &admittedAt1, standing: true, kind: th.AnchorAdmission, anchor: "c01"},
		{name: "fresh passes after the admission clear it",
			admission: &admittedAt1, receipts: []th.Receipt{
				receipt(on("c00", mac), result(unit, th.Pass)),
				receipt(on("c02", mac), result(unit, th.Pass)),
				receipt(on("c04", mac), result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorAdmission, anchor: "c01", lane: "darwin/arm64", passes: 2},
		{name: "passes after a failure in the same lane clear it",
			receipts: []th.Receipt{
				receipt(on("c00", mac), result(unit, th.Pass)),
				receipt(on("c02", mac), result(unit, th.Fail)),
				receipt(on("c03", mac), result(unit, th.Pass)),
				receipt(on("c05", mac), result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorFailure, anchor: "c02", lane: "darwin/arm64", passes: 2},
		{name: "cached passes, skips, expected failures and interruptions clear nothing",
			receipts: []th.Receipt{
				receipt(on("c01", mac), result(unit, th.Fail)),
				receipt(on("c02", mac), cached(unit)),
				receipt(on("c03", mac), result(unit, th.Skip)),
				receipt(on("c04", mac), result(unit, th.ExpectedFailure)),
				receipt(on("c05", mac), result(unit, th.Interrupted)),
			}, standing: true, kind: th.AnchorFailure, anchor: "c01", lane: "darwin/arm64"},
		{name: "repeated passes at one revision are one vote",
			receipts: []th.Receipt{
				receipt(on("c01", mac), result(unit, th.Fail)),
				receipt(on("c02", mac), result(unit, th.Pass)),
				receipt(on("c02", mac), result(unit, th.Pass), result(unit, th.Pass)),
				receipt(on("c02", mac), result(unit, th.Pass), result(unit, th.Pass), result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorFailure, anchor: "c01", lane: "darwin/arm64", passes: 1},
		{name: "a failure wins over a pass at the same revision",
			receipts: []th.Receipt{
				receipt(on("c01", mac), result(unit, th.Pass)),
				receipt(on("c03", mac), result(unit, th.Pass)),
				receipt(on("c03", mac), result(unit, th.Pass), result(unit, th.Fail)),
				receipt(on("c04", mac), result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorFailure, anchor: "c03", lane: "darwin/arm64", passes: 1},
		{name: "a retried attempt that failed is a failure",
			receipts: []th.Receipt{
				receipt(on("c02", mac), th.Result{Unit: unit, Case: "renders", Attempt: 1, Outcome: th.Fail, Detail: "retried"},
					th.Result{Unit: unit, Case: "renders", Attempt: 2, Outcome: th.Pass}, result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorFailure, anchor: "c02", lane: "darwin/arm64", cases: []string{"renders"}},
		{name: "a pass must cover the cases that failed",
			receipts: []th.Receipt{
				receipt(on("c01", mac), caseResult(unit, "renders", th.Fail), result(unit, th.Fail)),
				receipt(on("c02", mac), caseResult(unit, "scrolls", th.Pass), result(unit, th.Pass)),
				receipt(on("c03", mac), caseResult(unit, "renders", th.Skip), result(unit, th.Pass)),
				receipt(on("c04", mac), caseResult(unit, "renders", th.Pass), result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorFailure, anchor: "c01", lane: "darwin/arm64", passes: 1, cases: []string{"renders"}},
		{name: "a failure in another lane is cleared only by passes there",
			receipts: []th.Receipt{
				receipt(on("c01", linux), result(unit, th.Fail)),
				receipt(on("c02", mac), result(unit, th.Pass)),
				receipt(on("c03", mac), result(unit, th.Pass)),
				receipt(on("c04", linux), result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorFailure, anchor: "c01", lane: "linux/amd64", passes: 1},
		{name: "the least cleared lane decides",
			receipts: []th.Receipt{
				receipt(on("c01", mac), result(unit, th.Fail)),
				receipt(on("c02", linux), result(unit, th.Fail)),
				receipt(on("c03", mac), result(unit, th.Pass)),
				receipt(on("c04", mac), result(unit, th.Pass)),
				receipt(on("c05", linux), result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorFailure, anchor: "c02", lane: "linux/amd64", passes: 1},
		{name: "a later failure replaces the admission as anchor",
			admission: &admittedAt1, receipts: []th.Receipt{
				receipt(on("c03", mac), result(unit, th.Fail)),
				receipt(on("c04", mac), result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorFailure, anchor: "c03", lane: "darwin/arm64", passes: 1},
		{name: "a later admission replaces an older failure as anchor",
			admission: &[]th.Admission{main.Admission("c03")}[0], receipts: []th.Receipt{
				receipt(on("c01", mac), result(unit, th.Fail)),
				receipt(on("c02", mac), result(unit, th.Pass)),
				receipt(on("c04", mac), result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorAdmission, anchor: "c03", lane: "darwin/arm64", passes: 1},
		{name: "an admission not yet on main follows every main revision",
			admission: &[]th.Admission{main.Admission("unlanded")}[0], receipts: []th.Receipt{
				receipt(on("c04", mac), result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorAdmission, anchor: "unlanded", lane: "darwin/arm64"},
		{name: "dirty and unlanded failures are not health evidence",
			receipts: []th.Receipt{
				receipt(on("c02", mac, true), result(unit, th.Fail)),
				receipt(on("elsewhere", mac), result(unit, th.Fail)),
			}},
		{name: "silence is no standing without a failure or an admission",
			receipts: []th.Receipt{receipt(on("c02", mac), result(unit, th.Pass))}},
		{name: "a failure at the admission revision is the anchor",
			admission: &admittedAt1, receipts: []th.Receipt{
				receipt(on("c01", mac), result(unit, th.Fail)),
				receipt(on("c02", mac), result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorFailure, anchor: "c01", lane: "darwin/arm64", passes: 1},
		{name: "passes without the race detector do not clear a race failure",
			receipts: []th.Receipt{
				receipt(on("c01", raceLinux), result(unit, th.Fail)),
				receipt(on("c02", linux), result(unit, th.Pass)),
				receipt(on("c03", linux), result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorFailure, anchor: "c01", lane: "linux/amd64/race"},
		{name: "race passes do not clear a failure without the race detector",
			receipts: []th.Receipt{
				receipt(on("c01", linux), result(unit, th.Fail)),
				receipt(on("c02", raceLinux), result(unit, th.Pass)),
			}, standing: true, kind: th.AnchorFailure, anchor: "c01", lane: "linux/amd64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			of := tc.unit
			if of == "" {
				of = unit
			}
			standing, ok := th.BuildHealth(tc.receipts, main).Standing(of, tc.admission)
			if ok != tc.standing {
				t.Fatalf("standing = %v, want %v (%+v)", ok, tc.standing, standing)
			}
			if !ok {
				return
			}
			got := []any{standing.Anchor.Kind, standing.Anchor.Commit, standing.Lane, standing.PassesSince, standing.Anchor.Cases}
			want := []any{tc.kind, tc.anchor, tc.lane, tc.passes, tc.cases}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("standing = %v, want %v", got, want)
			}
		})
	}
}

func TestGoPackageResultsAreNotCandidates(t *testing.T) {
	main := th.NewMainline(commits(3))
	health := th.BuildHealth([]th.Receipt{
		receipt(on("c01", mac), result("go ./p", th.BuildFailure)),
		receipt(on("c02", mac), result("go ./q", th.Fail), result("go ./q TestA", th.Pass)),
	}, main)
	if units := health.FailureUnits(); len(units) != 0 {
		t.Fatalf("failure units = %v; package results carry no health", units)
	}
}

func wall(r th.Receipt, ms int64) th.Receipt {
	r.Command.WallMS = &ms
	r.Command.Exit = &th.Exit{}
	return r
}

func timed(unit string, outcome th.Outcome, ms int64) th.Result {
	r := result(unit, outcome)
	r.ElapsedMS = &ms
	return r
}

func priced(commit, unit string, ms int64) th.Receipt {
	r := wall(receipt(on(commit, mac, true), result(unit, th.Pass)), ms)
	r.InvocationID = "price-" + unit
	return r
}

func recipes(t *testing.T, text string) th.RecipeList {
	t.Helper()
	list, err := th.ParseRecipeList(text)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

type selected map[string]th.Status

func statuses(selection th.Selection) selected {
	got := selected{}
	for _, choice := range selection.Choices {
		got[choice.Unit] = choice.Status
	}
	return got
}

func ranked(selection th.Selection) []string {
	var order []string
	for _, choice := range selection.Choices {
		if choice.Status == th.Selected && !choice.Changed {
			order = append(order, choice.Unit)
		}
	}
	return order
}

func TestSelectionFollowsTheSpecification(t *testing.T) {
	main := th.NewMainline(commits(8))
	const (
		a, b, c   = "vitest src/a.test.tsx", "vitest src/b.test.tsx", "vitest src/c.test.tsx"
		flaky     = "vitest src/flaky.test.tsx"
		healed    = "vitest src/healed.test.tsx"
		changed   = "vitest src/changed.test.tsx"
		gone      = "vitest src/gone.test.tsx"
		scenario  = "native agent-reload"
		playwrite = "playwright e2e/x.spec.ts"
	)
	list := recipes(t, "group app ^app/\nvitest src/a.test.tsx\nvitest src/b.test.tsx\ngroup e2e ^app/e2e/\nplaywright e2e/x.spec.ts\n")
	inventory := th.Inventory{a: true, b: true, c: true, flaky: true, healed: true, changed: true, playwrite: true}
	admissions := map[string]th.Admission{a: main.Admission("c01"), b: main.Admission("c01"), playwrite: main.Admission("c01")}
	history := []th.Receipt{
		receipt(on("c02", linux), result(a, th.Pass), result(b, th.Pass)),
		receipt(on("c03", linux), result(a, th.Pass), result(healed, th.Fail)),
		receipt(on("c04", linux), result(healed, th.Pass), result(b, th.Pass)),
		receipt(on("c05", linux), result(healed, th.Pass), result(flaky, th.Fail), result(scenario, th.Fail), result(gone, th.Fail)),
		receipt(on("c06", linux), result(healed, th.Pass), result(a, th.Pass)),
		receipt(on("c07", linux), result(healed, th.Pass)),
		priced("c07", a, 4000), priced("c07", b, 3000), priced("c07", flaky, 2000), priced("c07", healed, 1000), priced("c07", changed, 9000),
	}
	request := th.SelectRequest{
		Policy: th.SuccessCount, Mode: th.ModeReplace, Recipes: list,
		Changed: []string{"app/src/changed.test.tsx"}, ChangedTests: []string{changed},
		Admissions: admissions, Inventory: inventory,
		Health: th.BuildHealth(history, main),
		Costs:  th.BuildCosts(history, th.CostKey{Lane: mac, Machine: machine}),
	}

	t.Run("replace ranks fewest passes first and fits the baseline's cost", func(t *testing.T) {
		selection := th.Select(request)
		if !reflect.DeepEqual(selection.Groups, []string{"app"}) {
			t.Fatalf("groups = %v", selection.Groups)
		}
		if got, want := ranked(selection), []string{flaky, b, healed}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ranked selection = %v, want %v", got, want)
		}
		want := selected{changed: th.Selected, flaky: th.Selected, b: th.Selected, healed: th.Selected, a: th.OverBudget, gone: th.Unavailable}
		if got := statuses(selection); !reflect.DeepEqual(got, want) {
			t.Fatalf("statuses = %v, want %v", got, want)
		}
		budget := selection.Budgets[0]
		if *budget.BudgetMS != 7000 || budget.EstimateMS != 6000 {
			t.Fatalf("budget = %d, estimate = %d; want 7000 and 6000", *budget.BudgetMS, budget.EstimateMS)
		}
		if selection.Policy != th.SuccessCount {
			t.Fatalf("policy = %s", selection.Policy)
		}
	})

	t.Run("recent-failure ranks by the newest anchor alone", func(t *testing.T) {
		recent := request
		recent.Policy = th.RecentFailure
		if got, want := ranked(th.Select(recent)), []string{flaky, healed, b}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ranked selection = %v, want %v", got, want)
		}
	})

	t.Run("extend keeps the baseline and adds within the allowance", func(t *testing.T) {
		extend := request
		extend.Mode = th.ModeExtend
		allowance := int64(2500)
		extend.AllowanceMS = &allowance
		want := selected{changed: th.Selected, a: th.Selected, b: th.Selected, flaky: th.Selected, healed: th.OverBudget, gone: th.Unavailable}
		if got := statuses(th.Select(extend)); !reflect.DeepEqual(got, want) {
			t.Fatalf("statuses = %v, want %v", got, want)
		}
	})

	t.Run("baseline mode is the fixed list", func(t *testing.T) {
		baseline := request
		baseline.Mode = th.ModeBaseline
		want := selected{a: th.Selected, b: th.Selected}
		if got := statuses(th.Select(baseline)); !reflect.DeepEqual(got, want) {
			t.Fatalf("statuses = %v, want %v", got, want)
		}
	})

	t.Run("an unpriced baseline unit leaves the runner without a budget", func(t *testing.T) {
		unpriced := request
		unpriced.Changed = []string{"app/e2e/x.spec.ts"}
		unpriced.ChangedTests = nil
		selection := th.Select(unpriced)
		if got, want := statuses(selection)[playwrite], th.Selected; got != want {
			t.Fatalf("%s = %s, want %s", playwrite, got, want)
		}
		for _, budget := range selection.Budgets {
			if budget.Runner == th.RunnerPlaywright && (budget.BudgetMS != nil || budget.Note == "") {
				t.Fatalf("playwright budget = %+v, want none with a reason", budget)
			}
		}
	})

	t.Run("a candidate without a cost is unpriced, never free", func(t *testing.T) {
		unpricedFailure := request
		unpricedFailure.Health = th.BuildHealth(append(history, receipt(on("c07", linux), result(c, th.Fail))), main)
		if got := statuses(th.Select(unpricedFailure))[c]; got != th.Unpriced {
			t.Fatalf("%s = %s, want unpriced", c, got)
		}
	})

	t.Run("a changed test is selected outside the budget even when unpriced or over it", func(t *testing.T) {
		tight := request
		tight.ChangedTests = []string{changed, c}
		got := statuses(th.Select(tight))
		if got[changed] != th.Selected || got[c] != th.Selected {
			t.Fatalf("changed tests = %s, %s; want selected", got[changed], got[c])
		}
	})

	t.Run("no change selects nothing", func(t *testing.T) {
		quiet := request
		quiet.Changed, quiet.ChangedTests = nil, nil
		if got := th.Select(quiet); len(got.Choices) != 0 || len(got.Groups) != 0 {
			t.Fatalf("selection = %+v, want empty", got)
		}
	})
}

func TestGoCostIsSetupPlusTheSlowestPackage(t *testing.T) {
	batch := wall(receipt(on("c00", mac),
		timed("go ./p", th.Pass, 10000), timed("go ./p TestA", th.Pass, 6000), timed("go ./p TestB", th.Pass, 3000),
		timed("go ./q", th.Pass, 4000), timed("go ./q TestC", th.Pass, 3500),
	), 12000)
	costs := th.BuildCosts([]th.Receipt{batch}, th.CostKey{Lane: mac, Machine: machine})
	cases := []struct {
		units []string
		want  int64
	}{
		{[]string{"go ./p TestA", "go ./p TestB", "go ./q TestC"}, 2000 + 10000},
		{[]string{"go ./p TestB", "go ./q TestC"}, 2000 + 1000 + 3000},
		{[]string{"go ./q TestC"}, 2000 + 500 + 3500},
	}
	for _, tc := range cases {
		if got, unpriced := costs.Estimate(tc.units); got != tc.want || len(unpriced) != 0 {
			t.Errorf("Estimate(%v) = %d %v, want %d", tc.units, got, unpriced, tc.want)
		}
	}
	unfit := []th.Receipt{
		withArgv(batch, "-run", "^TestA$/case"),
		withArgv(batch, "-skip", "TestB"),
		withResults(batch, append([]th.Result{cached("go ./r")}, batch.Results...)),
		withResults(batch, append([]th.Result{{Unit: "go ./p TestA", Attempt: 2, Outcome: th.Pass}}, batch.Results...)),
		otherMachine(batch),
		failedCommand(batch),
		withoutExit(batch),
		otherLane(batch),
	}
	for i, receipt := range unfit {
		costs := th.BuildCosts([]th.Receipt{receipt}, th.CostKey{Lane: mac, Machine: machine})
		if costs.Priced("go ./p TestA") {
			t.Errorf("unfit receipt %d priced go ./p TestA", i)
		}
	}
}

func withArgv(r th.Receipt, argv ...string) th.Receipt {
	r.Command.Argv = append([]string{"go", "test", "-json"}, append(argv, "./p", "./q")...)
	return r
}

func withResults(r th.Receipt, results []th.Result) th.Receipt {
	r.Results = results
	return r
}

func withoutExit(r th.Receipt) th.Receipt {
	r.Command.Exit = nil
	return r
}

func otherLane(r th.Receipt) th.Receipt {
	r.Lane = linux
	return r
}

func TestCostIsTheMedianOfWholeUnitSamples(t *testing.T) {
	file, other := "vitest src/a.test.tsx", "vitest src/b.test.tsx"
	key := th.CostKey{Lane: mac, Machine: machine}
	samples := []th.Receipt{
		wall(receipt(on("c00", mac), result(file, th.Pass)), 1000),
		wall(receipt(on("c01", mac), result(file, th.Pass)), 9000),
		wall(receipt(on("c02", mac), result(file, th.Pass)), 3000),
		wall(receipt(on("c03", mac), result(file, th.Pass), result(other, th.Pass)), 100),
		wall(receipt(on("c04", mac), result(file, th.Fail)), 100),
	}
	for i := range samples {
		samples[i].InvocationID = fmt.Sprintf("sample%d", i)
	}
	if got, _ := th.BuildCosts(samples, key).UnitMS(file); got != 3000 {
		t.Fatalf("cost = %d, want the median 3000 of the single-file passing runs", got)
	}
	if th.BuildCosts(samples[3:4], key).Priced(other) {
		t.Fatalf("a run of two files priced one of them")
	}

	withCases := wall(receipt(on("c00", mac),
		timed("go ./p", th.Pass, 5000), timed("go ./p TestA", th.Pass, 4000),
		th.Result{Unit: "go ./p TestA", Case: "sub", Attempt: 1, Outcome: th.Pass, ElapsedMS: &[]int64{4000}[0]},
	), 6000)
	if got, _ := th.BuildCosts([]th.Receipt{withCases}, key).Estimate([]string{"go ./p TestA"}); got != 6000 {
		t.Fatalf("estimate = %d, want 6000: subtest time is part of its test's time, not added to it", got)
	}
}

func failedCommand(r th.Receipt) th.Receipt {
	r.Command.Exit = &th.Exit{Code: 1}
	return r
}

func TestFilteredAppRunsDoNotPriceTheirFile(t *testing.T) {
	file := "vitest src/a.test.tsx"
	whole := wall(receipt(on("c00", mac), result(file, th.Pass)), 3000)
	for _, argv := range [][]string{{"vitest", "run", "src/a.test.tsx", "-t", "renders"}, {"vitest", "run", "--testNamePattern=renders"}} {
		filtered := whole
		filtered.Command.Argv = argv
		if th.BuildCosts([]th.Receipt{filtered}, th.CostKey{Lane: mac, Machine: machine}).Priced(file) {
			t.Errorf("%v priced the whole file", argv)
		}
	}
	if !th.BuildCosts([]th.Receipt{whole}, th.CostKey{Lane: mac, Machine: machine}).Priced(file) {
		t.Errorf("an unfiltered run did not price the file")
	}
}

func TestAppTestFilesFollowTheRunnersIncludes(t *testing.T) {
	for path, want := range map[string]string{
		"app/src/App.test.tsx":                          "vitest src/App.test.tsx",
		"app/src/utils/x.test.mjs":                      "vitest src/utils/x.test.mjs",
		"app/scripts/real-app-harness/common.test.mjs":  "vitest scripts/real-app-harness/common.test.mjs",
		"app/lint/rule.test.ts":                         "vitest lint/rule.test.ts",
		"app/e2e/queue.spec.ts":                         "playwright e2e/queue.spec.ts",
		"app/scripts/demo-harness/fixtures/a/b.test.js": "",
		"app/src/helpers.ts":                            "",
		"internal/x_test.go":                            "",
	} {
		if got, _ := th.AppTestUnit(path); got != want {
			t.Errorf("AppTestUnit(%s) = %q, want %q", path, got, want)
		}
	}
}

func otherMachine(r th.Receipt) th.Receipt {
	r.Diagnostics.MachineClass = "github-hosted 4cpu/16GB"
	return r
}

var units = []string{"vitest src/a.test.tsx", "vitest src/b.test.tsx", "vitest src/c.test.tsx", "vitest src/d.test.tsx"}

func generateHistory(t *rapid.T, main []string) []th.Receipt {
	lanes := []th.Lane{mac, linux}
	outcomes := []th.Outcome{th.Pass, th.Pass, th.Pass, th.Fail, th.Skip, th.Interrupted, th.ExpectedFailure}
	count := rapid.IntRange(0, 24).Draw(t, "receipts")
	history := make([]th.Receipt, 0, count)
	for i := 0; i < count; i++ {
		commit := rapid.SampledFrom(append(main, "unlanded")).Draw(t, "commit")
		where := on(commit, rapid.SampledFrom(lanes).Draw(t, "lane"), rapid.Bool().Draw(t, "dirty"))
		var results []th.Result
		for _, u := range units {
			if rapid.Bool().Draw(t, "ran") {
				r := result(u, rapid.SampledFrom(outcomes).Draw(t, "outcome"))
				r.Cached = r.Outcome == th.Pass && rapid.IntRange(0, 4).Draw(t, "cached") == 0
				results = append(results, r)
			}
		}
		r := receipt(where, results...)
		r.InvocationID = fmt.Sprintf("r%d", i)
		if len(results) == 1 && results[0].Outcome == th.Pass {
			r = wall(r, rapid.Int64Range(100, 5000).Draw(t, "wall"))
		}
		history = append(history, r)
	}
	return history
}

func propertyRequest(t *rapid.T, main th.Mainline, history []th.Receipt) th.SelectRequest {
	list, err := th.ParseRecipeList("group app ^app/\nvitest src/a.test.tsx\nvitest src/b.test.tsx\n")
	if err != nil {
		t.Fatal(err)
	}
	inventory := th.Inventory{}
	for _, u := range units {
		inventory[u] = true
	}
	admission := main.Admission(rapid.SampledFrom(commits(main.Len())).Draw(t, "admitted"))
	return th.SelectRequest{
		Policy:  rapid.SampledFrom([]th.Policy{th.SuccessCount, th.RecentFailure}).Draw(t, "policy"),
		Mode:    rapid.SampledFrom([]th.Mode{th.ModeReplace, th.ModeExtend, th.ModeBaseline}).Draw(t, "mode"),
		Recipes: list, Changed: []string{"app/src/x.ts"},
		Admissions: map[string]th.Admission{units[0]: admission, units[1]: admission},
		Inventory:  inventory,
		Health:     th.BuildHealth(history, main),
		Costs:      th.BuildCosts(history, th.CostKey{Lane: mac, Machine: machine}),
	}
}

func decisions(selection th.Selection) []string {
	var list []string
	for _, budget := range selection.Budgets {
		list = append(list, fmt.Sprintf("%s estimate %d", budget.Runner, budget.EstimateMS))
	}
	for _, choice := range selection.Choices {
		line := fmt.Sprintf("%s %s", choice.Status, choice.Unit)
		if s := choice.Standing; s != nil {
			line += fmt.Sprintf(" %s@%s %s passes %d", s.Anchor.Kind, s.Anchor.Commit, s.Lane, s.PassesSince)
		}
		list = append(list, line)
	}
	return list
}

func TestSelectionIgnoresArrivalOrderAndRepeatedRuns(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		list := commits(rapid.IntRange(1, 6).Draw(t, "commits"))
		main := th.NewMainline(list)
		history := generateHistory(t, list)
		request := propertyRequest(t, main, history)
		want := th.Select(request)

		shuffled := rapid.Permutation(history).Draw(t, "arrival")
		repeated := append([]th.Receipt(nil), shuffled...)
		for i, r := range history {
			if r.Command.WallMS == nil && rapid.Bool().Draw(t, "repeat") {
				r.InvocationID = fmt.Sprintf("repeat%d", i)
				repeated = append(repeated, r)
			}
		}
		request.Health = th.BuildHealth(repeated, main)
		request.Costs = th.BuildCosts(repeated, th.CostKey{Lane: mac, Machine: machine})
		if got := decisions(th.Select(request)); !reflect.DeepEqual(got, decisions(want)) {
			t.Fatalf("selection changed with arrival order or repeated runs:\n got %v\nwant %v", got, decisions(want))
		}
	})
}

func TestNoEvidenceButAFreshPassClearsAFailure(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		list := commits(rapid.IntRange(2, 6).Draw(t, "commits"))
		main := th.NewMainline(list)
		history := generateHistory(t, list)
		before := th.BuildHealth(history, main)
		noise := rapid.SampledFrom([]th.Result{cached(units[0]), result(units[0], th.Skip), result(units[0], th.Interrupted), result(units[0], th.ExpectedFailure)}).Draw(t, "noise")
		where := at{rapid.SampledFrom(list).Draw(t, "commit"), rapid.SampledFrom([]th.Lane{mac, linux}).Draw(t, "lane"), false}
		after := th.BuildHealth(append(history, receipt(where, noise)), main)
		was, hadStanding := before.Standing(units[0], nil)
		now, hasStanding := after.Standing(units[0], nil)
		if hadStanding != hasStanding || hadStanding && now.PassesSince != was.PassesSince {
			t.Fatalf("%v at %v changed the standing from %+v to %+v", noise.Outcome, where, was, now)
		}
	})
}

func TestReplaceNeverExceedsTheBaselineCost(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		list := commits(rapid.IntRange(1, 6).Draw(t, "commits"))
		main := th.NewMainline(list)
		request := propertyRequest(t, main, generateHistory(t, list))
		request.Mode = th.ModeReplace
		selection := th.Select(request)
		for _, budget := range selection.Budgets {
			if budget.BudgetMS != nil && budget.EstimateMS > *budget.BudgetMS {
				t.Fatalf("%s selection costs %d, over its budget %d", budget.Runner, budget.EstimateMS, *budget.BudgetMS)
			}
		}
		seen := map[string]bool{}
		for _, choice := range selection.Choices {
			if seen[choice.Unit] {
				t.Fatalf("%s chosen twice", choice.Unit)
			}
			seen[choice.Unit] = true
			if choice.Status == th.Selected && !choice.Changed && !choice.Baseline && !request.Costs.Priced(choice.Unit) {
				t.Fatalf("%s selected without a cost", choice.Unit)
			}
		}
	})
}
