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

func filtered(r th.Receipt) th.Receipt {
	r.Command.Argv = []string{"vitest", "run", "-t", "scrolls"}
	return r
}

func cached(unit string) th.Result {
	return th.Result{Unit: unit, Attempt: 1, Outcome: th.Pass, Cached: true}
}

const unit = "vitest src/a.test.tsx"

func TestStandingFollowsTheSpecification(t *testing.T) {
	main := th.NewMainline(commits(6))
	cases := []struct {
		name     string
		unit     string
		receipts []th.Receipt
		standing bool
		anchor   string
		lane     string
		passes   int
		cases    []string
	}{
		{name: "passes after a failure in the same lane clear it",
			receipts: []th.Receipt{
				receipt(on("c00", mac), result(unit, th.Pass)),
				receipt(on("c02", mac), result(unit, th.Fail)),
				receipt(on("c03", mac), result(unit, th.Pass)),
				receipt(on("c05", mac), result(unit, th.Pass)),
			}, standing: true, anchor: "c02", lane: "darwin/arm64", passes: 2},
		{name: "cached passes, skips, expected failures and interruptions clear nothing",
			receipts: []th.Receipt{
				receipt(on("c01", mac), result(unit, th.Fail)),
				receipt(on("c02", mac), cached(unit)),
				receipt(on("c03", mac), result(unit, th.Skip)),
				receipt(on("c04", mac), result(unit, th.ExpectedFailure)),
				receipt(on("c05", mac), result(unit, th.Interrupted)),
			}, standing: true, anchor: "c01", lane: "darwin/arm64"},
		{name: "repeated passes at one revision are one vote",
			receipts: []th.Receipt{
				receipt(on("c01", mac), result(unit, th.Fail)),
				receipt(on("c02", mac), result(unit, th.Pass)),
				receipt(on("c02", mac), result(unit, th.Pass), result(unit, th.Pass)),
				receipt(on("c02", mac), result(unit, th.Pass), result(unit, th.Pass), result(unit, th.Pass)),
			}, standing: true, anchor: "c01", lane: "darwin/arm64", passes: 1},
		{name: "a failure wins over a pass at the same revision",
			receipts: []th.Receipt{
				receipt(on("c01", mac), result(unit, th.Pass)),
				receipt(on("c03", mac), result(unit, th.Pass)),
				receipt(on("c03", mac), result(unit, th.Pass), result(unit, th.Fail)),
				receipt(on("c04", mac), result(unit, th.Pass)),
			}, standing: true, anchor: "c03", lane: "darwin/arm64", passes: 1},
		{name: "a retried attempt that failed is a failure",
			receipts: []th.Receipt{
				receipt(on("c02", mac), th.Result{Unit: unit, Case: "renders", Attempt: 1, Outcome: th.Fail, Detail: "retried"},
					th.Result{Unit: unit, Case: "renders", Attempt: 2, Outcome: th.Pass}, result(unit, th.Pass)),
			}, standing: true, anchor: "c02", lane: "darwin/arm64", cases: []string{"renders"}},
		{name: "a pass must cover the cases that failed",
			receipts: []th.Receipt{
				receipt(on("c01", mac), caseResult(unit, "renders", th.Fail), result(unit, th.Fail)),
				filtered(receipt(on("c02", mac), caseResult(unit, "scrolls", th.Pass), result(unit, th.Pass))),
				receipt(on("c03", mac), caseResult(unit, "renders", th.Skip), result(unit, th.Pass)),
				receipt(on("c04", mac), caseResult(unit, "renders", th.Pass), result(unit, th.Pass)),
			}, standing: true, anchor: "c01", lane: "darwin/arm64", passes: 1, cases: []string{"renders"}},
		{name: "a whole run that passes without a failed case it no longer has clears it",
			receipts: []th.Receipt{
				receipt(on("c01", mac), caseResult(unit, "renders", th.Fail), result(unit, th.Fail)),
				receipt(on("c02", mac), caseResult(unit, "renders its rows", th.Pass), result(unit, th.Pass)),
			}, standing: true, anchor: "c01", lane: "darwin/arm64", passes: 1, cases: []string{"renders"}},
		{name: "a failure in another lane is cleared only by passes there",
			receipts: []th.Receipt{
				receipt(on("c01", linux), result(unit, th.Fail)),
				receipt(on("c02", mac), result(unit, th.Pass)),
				receipt(on("c03", mac), result(unit, th.Pass)),
				receipt(on("c04", linux), result(unit, th.Pass)),
			}, standing: true, anchor: "c01", lane: "linux/amd64", passes: 1},
		{name: "the least cleared lane decides",
			receipts: []th.Receipt{
				receipt(on("c01", mac), result(unit, th.Fail)),
				receipt(on("c02", linux), result(unit, th.Fail)),
				receipt(on("c03", mac), result(unit, th.Pass)),
				receipt(on("c04", mac), result(unit, th.Pass)),
				receipt(on("c05", linux), result(unit, th.Pass)),
			}, standing: true, anchor: "c02", lane: "linux/amd64", passes: 1},
		{name: "dirty and unlanded failures are not health evidence",
			receipts: []th.Receipt{
				receipt(on("c02", mac, true), result(unit, th.Fail)),
				receipt(on("elsewhere", mac), result(unit, th.Fail)),
			}},
		{name: "passes without the race detector do not clear a race failure",
			receipts: []th.Receipt{
				receipt(on("c01", raceLinux), result(unit, th.Fail)),
				receipt(on("c02", linux), result(unit, th.Pass)),
				receipt(on("c03", linux), result(unit, th.Pass)),
			}, standing: true, anchor: "c01", lane: "linux/amd64/race"},
		{name: "race passes do not clear a failure without the race detector",
			receipts: []th.Receipt{
				receipt(on("c01", linux), result(unit, th.Fail)),
				receipt(on("c02", raceLinux), result(unit, th.Pass)),
			}, standing: true, anchor: "c01", lane: "linux/amd64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			of := tc.unit
			if of == "" {
				of = unit
			}
			standing, ok := th.BuildHealth(tc.receipts, main).Standing(of)
			if ok != tc.standing {
				t.Fatalf("standing = %v, want %v (%+v)", ok, tc.standing, standing)
			}
			if !ok {
				return
			}
			got := []any{standing.Anchor.Commit, standing.Lane, standing.PassesSince, standing.Anchor.Cases}
			want := []any{tc.anchor, tc.lane, tc.passes, tc.cases}
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

func recipes(t *testing.T, text string) th.RecipeList {
	t.Helper()
	list, err := th.ParseRecipeList(text)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func statuses(selection th.Selection) map[string]th.Status {
	got := map[string]th.Status{}
	for _, choice := range selection.Choices {
		got[choice.Unit] = choice.Status
	}
	return got
}

func TestSelectionFollowsTheSpecification(t *testing.T) {
	n := th.ClearedAfter
	main := th.NewMainline(commits(n + 4))
	at := func(i int) string { return fmt.Sprintf("c%02d", i) }
	const (
		a, b      = "vitest src/a.test.tsx", "vitest src/b.test.tsx"
		recent    = "vitest src/recent.test.tsx"
		cleared   = "vitest src/cleared.test.tsx"
		changed   = "vitest src/changed.test.tsx"
		gone      = "vitest src/gone.test.tsx"
		scenario  = "native agent-reload"
		elsewhere = "go ./p TestElsewhere"
		playwrite = "playwright e2e/x.spec.ts"
	)
	list := recipes(t, "group app ^app/\nvitest src/a.test.tsx\nvitest src/b.test.tsx\ngroup e2e ^app/e2e/\nplaywright e2e/x.spec.ts\n")
	inventory := th.Inventory{a: true, b: true, recent: true, cleared: true, changed: true, playwrite: true, elsewhere: true}
	history := []th.Receipt{
		receipt(on(at(1), linux), result(cleared, th.Fail), result(scenario, th.Fail), result(gone, th.Fail)),
		receipt(on(at(1), linux), result(elsewhere, th.Fail)),
		receipt(on(at(2), linux), result(recent, th.Fail)),
	}
	for i := 2; i < n+2; i++ {
		history = append(history, receipt(on(at(i), linux), result(cleared, th.Pass)))
	}
	for i := 3; i < n+2; i++ {
		history = append(history, receipt(on(at(i), linux), result(recent, th.Pass)))
	}
	request := th.SelectRequest{
		Groups: list.Groups[:1], ChangedTests: []string{changed}, Inventory: inventory,
		Health: th.BuildHealth(history, main),
	}
	selection := th.Select(request)
	want := map[string]th.Status{
		a: th.Selected, b: th.Selected, changed: th.Selected, recent: th.Selected,
		cleared: th.Cleared, gone: th.Missing, scenario: th.Unavailable,
	}
	if got := statuses(selection); !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %v, want %v: the matched group's baseline, the changed test and a failure with %d of %d passes run; one with %d passes has cleared; a native failure is unavailable and a test gone from the checkout missing; a Go failure waits for a group that runs Go", got, want, n-1, n, n)
	}
	for _, choice := range selection.Choices {
		if choice.Unit == recent && choice.PassesLeft() != 1 {
			t.Fatalf("%s has %d passes left, want 1", recent, choice.PassesLeft())
		}
		if choice.Status == th.Unavailable && choice.Note == "" {
			t.Fatalf("%s is unavailable without a reason", choice.Unit)
		}
	}

	t.Run("a baseline unit missing from the checkout is unavailable", func(t *testing.T) {
		request := request
		request.Inventory = th.Inventory{b: true}
		if got := statuses(th.Select(request))[a]; got != th.Unavailable {
			t.Fatalf("missing baseline unit is %s", got)
		}
	})

	t.Run("no group selects nothing", func(t *testing.T) {
		request := request
		request.Groups = nil
		if choices := th.Select(request).Choices; len(choices) != 0 {
			t.Fatalf("with no group, chose %v", choices)
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
	return th.SelectRequest{Groups: list.Groups, Inventory: inventory, Health: th.BuildHealth(history, main)}
}

func decisions(selection th.Selection) []string {
	var list []string
	for _, choice := range selection.Choices {
		line := fmt.Sprintf("%s %s", choice.Status, choice.Unit)
		if s := choice.Standing; s != nil {
			line += fmt.Sprintf(" %s %s passes %d", s.Anchor.Commit, s.Lane, s.PassesSince)
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
		was, hadStanding := before.Standing(units[0])
		now, hasStanding := after.Standing(units[0])
		if hadStanding != hasStanding || hadStanding && now.PassesSince != was.PassesSince {
			t.Fatalf("%v at %v changed the standing from %+v to %+v", noise.Outcome, where, was, now)
		}
	})
}
