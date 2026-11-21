package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/testhistory"
)

func TestTheLedgerListsATestWhoseLatestCIPassesSitOutTheSameWait(t *testing.T) {
	repo := newCheckout(t)
	repo.write("p/p_test.go", "package p\n")
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	commit := repo.commit("tests", start)
	timings := map[string][]int64{
		"go ./p TestWaitsOutATimer":     append(repeat(5, 3_000), steady(20, 90_100)...),
		"go ./p TestWasDeleted":         steady(20, 90_100),
		"go ./q TestDoesSlowWork":       varying(20, 20_000, 1_000),
		"go ./r TestHadItsWaitFixed":    append(steady(19, 90_100), 2_000),
		"go ./s TestWaitsUnderTheFloor": steady(20, 10_000),
	}
	for unit, elapsed := range timings {
		job := strings.Fields(unit)[1]
		for i, ms := range elapsed {
			repo.receive(pass(fmt.Sprintf("%s-%d", strings.Fields(unit)[2], i), "ci", job, commit, unit, ms, start.Add(time.Duration(i)*time.Hour)))
		}
	}
	cancelled := pass("cancelled", "ci", "./p", commit, "go ./p TestOther", 1_000, start.Add(30*time.Hour))
	cancelled.Complete = false
	repo.receive(cancelled)
	repo.receive(pass("pull-request", "ci", "./p", strings.Repeat("0", 40), "go ./p TestOther", 1_000, start.Add(31*time.Hour)))
	for i := range 20 {
		repo.receive(pass(fmt.Sprintf("local-%d", i), "local", "./t", commit, "go ./t TestWaitsOnlyOnThisMachine", 30_000, start.Add(time.Duration(i)*time.Hour)))
	}

	out, code := repo.history("report", "--main", "main", "--format", "json")
	if code != 0 {
		t.Fatalf("report exited %d:\n%s", code, out)
	}
	report := decode[failureReport](t, out)
	want := []testhistory.FixedWait{{Lane: "linux/amd64", Unit: "go ./p TestWaitsOutATimer", Passes: 20, MedianMS: 90_110, FastestMS: 90_100, SlowestMS: 90_119}}
	if fmt.Sprint(report.FixedWaits) != fmt.Sprint(want) {
		t.Fatalf("fixed waits = %+v, want %+v", report.FixedWaits, want)
	}
	markdown, code := repo.history("report", "--main", "main", "--format", "markdown")
	row := "| 90.1s | 90.1s to 90.1s | 20 | linux/amd64 | go ./p TestWaitsOutATimer |\n"
	if code != 0 || !strings.Contains(markdown, "## Fixed waits\n") || !strings.Contains(markdown, row) {
		t.Fatalf("the ledger's markdown (exit %d) lacks the fixed-wait row %q:\n%s", code, row, markdown)
	}
}

func repeat(n int, ms int64) []int64 { return varying(n, ms, 0) }
func steady(n int, ms int64) []int64 { return varying(n, ms, 1) }

func varying(n int, ms, step int64) []int64 {
	elapsed := make([]int64, n)
	for i := range elapsed {
		elapsed[i] = ms + int64(i)*step
	}
	return elapsed
}

func pass(id, kind, job, commit, unit string, ms int64, started time.Time) testhistory.Receipt {
	finished := started.Add(time.Duration(ms) * time.Millisecond)
	return testhistory.Receipt{
		Schema: testhistory.Schema, InvocationID: id, Runner: testhistory.RunnerGo,
		Source:   testhistory.Source{Kind: kind, RunID: id, Attempt: "1", Job: job},
		Revision: testhistory.Revision{Commit: commit},
		Lane:     testhistory.Lane{OS: "linux", Arch: "amd64"},
		Command:  testhistory.Command{StartedAt: started, FinishedAt: &finished, Exit: &testhistory.Exit{}},
		Results:  []testhistory.Result{{Unit: unit, Attempt: 1, Outcome: testhistory.Pass, ElapsedMS: &ms}},
		Complete: true,
	}
}
