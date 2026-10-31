package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/testhistory"
)

func otherLaneRun(id, commit, unit string, outcome testhistory.Outcome, finished time.Time) testhistory.Receipt {
	receipt := otherLaneFailure(id, commit, unit, finished)
	receipt.Results[0].Outcome = outcome
	return receipt
}

func TestLandRunsTheNamedGroupsAndEachFailureUntilItClears(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", checkoutRepository)
	repo := newCheckout(t)
	service := newFakeHistoryService(t)
	config := t.TempDir()
	if err := os.MkdirAll(filepath.Join(config, "attn-test-history"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "attn-test-history", "token"), []byte(serviceToken), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	repo.write("scripts/landing-tests", "group go \\.go$\ngo ./p TestA\ngo ./p TestRenamed\n")
	repo.write("p/p_test.go", "package p\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n")
	repo.write("q/q_test.go", "package q\n\nimport \"testing\"\n\nfunc TestRecent(t *testing.T) {}\n\nfunc TestCleared(t *testing.T) {}\n")
	failedAt := repo.commit("tests", start)
	share := func(id, commit, unit string, outcome testhistory.Outcome) {
		body, err := json.Marshal(otherLaneRun(id, commit, unit, outcome, start.Add(time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		service.add("github.com/"+checkoutRepository, id, body)
	}
	share("recent-fails", failedAt, "go ./q TestRecent", testhistory.Fail)
	share("cleared-fails", failedAt, "go ./q TestCleared", testhistory.Fail)
	for i := range testhistory.ClearedAfter {
		repo.gitAt(start.Add(time.Duration(i+1)*time.Second), "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("main %d", i))
		commit := repo.git("rev-parse", "HEAD")
		share(fmt.Sprintf("cleared-passes-%d", i), commit, "go ./q TestCleared", testhistory.Pass)
		if i == 0 {
			share("recent-passes", commit, "go ./q TestRecent", testhistory.Pass)
		}
	}
	repo.git("checkout", "-q", "-b", "change")
	repo.write("README.md", "docs only\n")
	head := repo.commit("change the docs", start.Add(time.Hour))

	land := func(groups ...string) (string, string, int) {
		t.Helper()
		unitsFile := filepath.Join(t.TempDir(), "units")
		args := []string{"land", "--main", "main", "--units-out", unitsFile}
		for _, group := range groups {
			args = append(args, "--group", group)
		}
		out, code := repo.history(args...)
		units, _ := os.ReadFile(unitsFile)
		chosen := strings.Split(strings.TrimSpace(string(units)), "\n")
		sort.Strings(chosen)
		return out, strings.Join(chosen, ", "), code
	}

	out, units, code := land("go")
	if code != 0 || units != "go ./p TestA, go ./p TestRenamed" || !strings.Contains(out, "the baseline, no shared history") {
		t.Fatalf("before any sync, land exited %d and chose %s:\n%s", code, units, out)
	}

	sync := historyCommand(t, repo.spool, repo.dir, "sync")
	sync.Env = append(sync.Env, "ATTN_TEST_HISTORY_URL="+service.server.URL, "XDG_CONFIG_HOME="+config)
	if synced, err := sync.CombinedOutput(); err != nil {
		t.Fatalf("sync: %v\n%s", err, synced)
	}

	out, units, code = land("go")
	if code != 0 || units != "go ./p TestA, go ./p TestRenamed, go ./q TestRecent" {
		t.Fatalf("land --group go exited %d and chose %s, want the go group's baseline, though the change touches only docs, and the uncleared failure:\n%s", code, units, out)
	}
	for _, want := range []string{
		"landing tests for " + short(head) + ":",
		"ADDED       go ./q TestRecent",
		fmt.Sprintf("failed on %s at %s; passed on 1 main revisions since, runs until %d more pass", otherLane(), short(failedAt), testhistory.ClearedAfter-1),
		"UNAVAILABLE go ./p TestRenamed",
		"requested anyway: every baseline line must name a test in the checkout",
		"baseline: 1 units; 1 tests that failed on main have cleared",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("land output lacks %q:\n%s", want, out)
		}
	}

	if _, _, code := land("missing"); code == 0 {
		t.Fatal("land with a group the list lacks must fail, so land.sh runs its groups itself")
	}
}
