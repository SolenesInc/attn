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
			fixture: "vitest/mixed.jsonl", runner: RunnerVitest, finished: true,
			expect: []expect{
				{unit: "vitest src/mixed.test.mjs", attempt: 1, outcome: Fail},
				{unit: "vitest src/mixed.test.mjs", test: "outer > passes", attempt: 1, outcome: Pass},
				{unit: "vitest src/mixed.test.mjs", test: "outer > fails", attempt: 1, outcome: Fail},
				{unit: "vitest src/mixed.test.mjs", test: "outer > is skipped", attempt: 1, outcome: Skip},
				{unit: "vitest src/mixed.test.mjs", test: "outer > is todo", attempt: 1, outcome: Skip, detail: "todo"},
				{unit: "vitest src/mixed.test.mjs", test: "outer > fails as expected", attempt: 1, outcome: ExpectedFailure},
				{unit: "vitest src/mixed.test.mjs", test: "outer > passes on retry", attempt: 1, outcome: Fail, detail: "retried"},
				{unit: "vitest src/mixed.test.mjs", test: "outer > passes on retry", attempt: 2, outcome: Pass},
				{unit: "vitest src/allskipped.test.mjs", attempt: 1, outcome: Skip},
				{unit: "vitest src/broken.test.mjs", attempt: 1, outcome: Fail},
				{unit: "vitest src/good.test.mjs", attempt: 1, outcome: Pass},
			},
		},
		{
			fixture: "vitest/interrupted.jsonl", runner: RunnerVitest, finished: false,
			expect: []expect{
				{unit: "vitest src/slow.test.mjs", test: "finishes first", attempt: 1, outcome: Pass},
			},
			absent: []absent{
				{unit: "vitest src/slow.test.mjs"},
				{unit: "vitest src/slow.test.mjs", test: "is interrupted"},
				{unit: "vitest src/slow.test.mjs", test: "never starts"},
			},
		},
		{
			fixture: "playwright/mixed.json", runner: RunnerPlaywright, finished: true,
			expect: []expect{
				{unit: "playwright e2e/good.spec.mjs", attempt: 1, outcome: Pass},
				{unit: "playwright e2e/mixed.spec.mjs", attempt: 1, outcome: Fail},
				{unit: "playwright e2e/mixed.spec.mjs", test: "group › passes", attempt: 1, outcome: Pass},
				{unit: "playwright e2e/mixed.spec.mjs", test: "group › fails", attempt: 1, outcome: Fail},
				{unit: "playwright e2e/mixed.spec.mjs", test: "group › fails", attempt: 2, outcome: Fail},
				{unit: "playwright e2e/mixed.spec.mjs", test: "group › skips", attempt: 1, outcome: Skip, detail: "skip: needs a display"},
				{unit: "playwright e2e/mixed.spec.mjs", test: "group › is fixme", attempt: 1, outcome: Skip, detail: "fixme:"},
				{unit: "playwright e2e/mixed.spec.mjs", test: "group › fails as expected", attempt: 1, outcome: ExpectedFailure},
				{unit: "playwright e2e/mixed.spec.mjs", test: "group › expected to fail but passes", attempt: 1, outcome: Fail, detail: "expected to fail but passed"},
				{unit: "playwright e2e/mixed.spec.mjs", test: "group › flaky", attempt: 1, outcome: Fail},
				{unit: "playwright e2e/mixed.spec.mjs", test: "group › flaky", attempt: 2, outcome: Pass},
				{unit: "playwright e2e/mixed.spec.mjs", test: "group › times out", attempt: 2, outcome: Fail, detail: "timed out"},
				{unit: "playwright e2e/mixed.spec.mjs", test: "group › expected to fail but times out", attempt: 1, outcome: Fail, detail: "timed out"},
				{unit: "playwright e2e/mixed.spec.mjs", test: "group › expected to fail but times out", attempt: 2, outcome: Fail, detail: "timed out"},
			},
		},
		{
			fixture: "playwright/load-error.json", runner: RunnerPlaywright, finished: true,
			expect: []expect{
				{unit: "playwright e2e/broken.spec.mjs", attempt: 1, outcome: Fail, detail: "failed to load"},
			},
			absent: []absent{{unit: "playwright e2e/good.spec.mjs"}},
		},
		{
			fixture: "playwright/interrupted.json", runner: RunnerPlaywright, finished: true,
			expect: []expect{
				{unit: "playwright e2e/slow.spec.mjs", attempt: 1, outcome: Interrupted},
				{unit: "playwright e2e/slow.spec.mjs", test: "finishes first", attempt: 1, outcome: Pass},
				{unit: "playwright e2e/slow.spec.mjs", test: "is interrupted", attempt: 1, outcome: Interrupted},
			},
			absent: []absent{{unit: "playwright e2e/slow.spec.mjs", test: "never starts"}},
		},
		{fixture: "playwright/truncated.json", runner: RunnerPlaywright, finished: false},
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
