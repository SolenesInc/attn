package testhistory

import (
	"os"
	"path/filepath"
	"testing"
)

type expect struct {
	unit, test string
	attempt    int
	outcome    Outcome
	cached     bool
	detail     string
}

type absent struct{ unit, test string }

func TestRunnerOutputNormalizesToReceiptResults(t *testing.T) {
	const module = "example.invalid/fixture"
	cases := []struct {
		fixture  string
		runner   Runner
		finished bool
		expect   []expect
		absent   []absent
	}{
		{
			fixture: "go/mixed.jsonl", runner: RunnerGo, finished: true,
			expect: []expect{
				{unit: "go ./good", attempt: 1, outcome: Fail},
				{unit: "go ./good TestPasses", attempt: 1, outcome: Pass},
				{unit: "go ./good TestFails", attempt: 1, outcome: Fail},
				{unit: "go ./good TestSkips", attempt: 1, outcome: Skip, detail: "good_test.go:13: needs ClickHouse"},
				{unit: "go ./good TestCases", attempt: 1, outcome: Fail},
				{unit: "go ./good TestCases", test: "passes", attempt: 1, outcome: Pass},
				{unit: "go ./good TestCases", test: "fails", attempt: 1, outcome: Fail},
				{unit: "go ./good TestCases", test: "skips", attempt: 1, outcome: Skip, detail: "good_test.go:18: not here"},
				{unit: "go ./good TestCases", test: "nested/deep", attempt: 1, outcome: Pass},
				{unit: "go ./broken", attempt: 1, outcome: BuildFailure},
				{unit: "go ./mainfail", attempt: 1, outcome: Fail},
			},
			absent: []absent{{unit: "go ./broken TestNeverBuilds"}, {unit: "go ./mainfail TestNeverRuns"}},
		},
		{
			fixture: "go/cached.jsonl", runner: RunnerGo, finished: true,
			expect: []expect{
				{unit: "go ./good", attempt: 1, outcome: Pass, cached: true},
				{unit: "go ./good TestPasses", attempt: 1, outcome: Pass, cached: true},
				{unit: "go ./good TestSkips", attempt: 1, outcome: Skip, cached: true, detail: "good_test.go:13: needs ClickHouse"},
			},
		},
		{
			fixture: "go/timeout.jsonl", runner: RunnerGo, finished: true,
			expect: []expect{
				{unit: "go ./good", attempt: 1, outcome: Fail},
				{unit: "go ./good TestPasses", attempt: 1, outcome: Pass},
				{unit: "go ./good TestSleeps", attempt: 1, outcome: Fail, detail: "still running when its package ended"},
			},
		},
		{
			fixture: "go/interrupted.jsonl", runner: RunnerGo, finished: true,
			expect: []expect{
				{unit: "go ./good", attempt: 1, outcome: Interrupted},
				{unit: "go ./good TestPasses", attempt: 1, outcome: Pass},
				{unit: "go ./good TestSleeps", attempt: 1, outcome: Interrupted},
			},
		},
		{
			fixture: "go/truncated.jsonl", runner: RunnerGo, finished: false,
			expect: []expect{
				{unit: "go ./broken", attempt: 1, outcome: BuildFailure},
				{unit: "go ./good", attempt: 1, outcome: Interrupted},
				{unit: "go ./mainfail", attempt: 1, outcome: Interrupted},
			},
			absent: []absent{{unit: "go ./mainfail TestNeverRuns"}},
		},
		{
			fixture: "cargo/mixed.log", runner: RunnerCargo, finished: true,
			expect: []expect{
				{unit: "cargo good --lib", attempt: 1, outcome: Fail},
				{unit: "cargo good --lib", test: "tests::passes", attempt: 1, outcome: Pass},
				{unit: "cargo good --lib", test: "tests::fails", attempt: 1, outcome: Fail},
				{unit: "cargo good --lib", test: "tests::skips", attempt: 1, outcome: Skip, detail: "needs a GPU"},
				{unit: "cargo good --test crash", attempt: 1, outcome: Fail, detail: "the test binary exited before reporting its result"},
				{unit: "cargo good --test slow", attempt: 1, outcome: Pass},
				{unit: "cargo good --test wire", attempt: 1, outcome: Pass},
				{unit: "cargo good --test wire", test: "wire::passes", attempt: 1, outcome: Pass},
				{unit: "cargo good --doc", attempt: 1, outcome: Pass},
				{unit: "cargo good --doc", test: "good/src/lib.rs - two (line 1)", attempt: 1, outcome: Pass},
			},
			absent: []absent{{unit: "cargo good --test crash", test: "passes_first"}},
		},
		{
			fixture: "cargo/build-failure.log", runner: RunnerCargo, finished: true,
			expect: []expect{
				{unit: "cargo broken --lib", attempt: 1, outcome: BuildFailure},
			},
			absent: []absent{{unit: "cargo broken --lib", test: "never_builds"}},
		},
		{
			fixture: "cargo/interrupted.log", runner: RunnerCargo, finished: false,
			expect: []expect{
				{unit: "cargo good --test slow", attempt: 1, outcome: Interrupted},
			},
			absent: []absent{{unit: "cargo good --test wire"}},
		},
		{
			fixture: "native/complete.json", runner: RunnerNative, finished: true,
			expect: []expect{
				{unit: "native session-close-ledger", attempt: 1, outcome: Pass},
				{unit: "native session-reopen", attempt: 1, outcome: Pass},
			},
		},
		{
			fixture: "native/timed-out.json", runner: RunnerNative, finished: true,
			expect: []expect{
				{unit: "native session-close-ledger", attempt: 1, outcome: Fail, detail: "timed out"},
			},
		},
		{
			fixture: "native/interrupted.json", runner: RunnerNative, finished: false,
			expect: []expect{
				{unit: "native session-reopen", attempt: 1, outcome: Pass},
				{unit: "native session-close-ledger", attempt: 1, outcome: Interrupted},
			},
		},
		{
			fixture: "native/ci-legacy-passed-with-skips.json", runner: RunnerNative, finished: false,
			expect: []expect{
				{unit: "native agent-reload", attempt: 1, outcome: Pass},
				{unit: "native quick-capture", attempt: 1, outcome: Skip, detail: "Quick Capture uses a macOS nonactivating panel"},
			},
		},
		{
			fixture: "native/ci-legacy-timed-out.json", runner: RunnerNative, finished: false,
			expect: []expect{
				{unit: "native outposts-settings", attempt: 1, outcome: Fail, detail: "exit 124"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			observed := Observe(tc.runner, raw, module)
			if observed.Finished != tc.finished {
				t.Errorf("finished = %v, want %v", observed.Finished, tc.finished)
			}
			for _, want := range tc.expect {
				got, ok := find(observed.Results, want.unit, want.test, want.attempt)
				if !ok {
					t.Errorf("no result for %q case %q attempt %d", want.unit, want.test, want.attempt)
					continue
				}
				if got.Outcome != want.outcome || got.Cached != want.cached || got.Detail != want.detail {
					t.Errorf("%q case %q attempt %d = %s cached=%v detail=%q, want %s cached=%v detail=%q",
						want.unit, want.test, want.attempt, got.Outcome, got.Cached, got.Detail, want.outcome, want.cached, want.detail)
				}
			}
			for _, missing := range tc.absent {
				for _, result := range observed.Results {
					if result.Unit == missing.unit && result.Case == missing.test {
						t.Errorf("%q case %q reported %s, but the runner never reported it", missing.unit, missing.test, result.Outcome)
					}
				}
			}
		})
	}
}

func find(results []Result, unit, test string, attempt int) (Result, bool) {
	for _, result := range results {
		if result.Unit == unit && result.Case == test && result.Attempt == attempt {
			return result, true
		}
	}
	return Result{}, false
}
