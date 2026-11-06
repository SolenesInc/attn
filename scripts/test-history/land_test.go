package main

import (
	"bytes"
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
	repo.write("q/q_test.go", "package q\n\nimport \"testing\"\n\nfunc TestRecent(t *testing.T) {}\n\nfunc TestCleared(t *testing.T) {}\n\nfunc TestStillRed(t *testing.T) {}\n")
	repo.write("q/elsewhere_plan9_test.go", "package q\n\nimport \"testing\"\n\nfunc TestElsewhere(t *testing.T) {}\n")
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
	share("still-red-fails", failedAt, "go ./q TestStillRed", testhistory.Fail)
	share("elsewhere-fails", failedAt, "go ./q TestElsewhere", testhistory.Fail)
	for i := range testhistory.ClearedAfter {
		repo.gitAt(start.Add(time.Duration(i+1)*time.Second), "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("main %d", i))
		commit := repo.git("rev-parse", "HEAD")
		share(fmt.Sprintf("cleared-passes-%d", i), commit, "go ./q TestCleared", testhistory.Pass)
		if i == 0 {
			share("recent-passes", commit, "go ./q TestRecent", testhistory.Pass)
			share("elsewhere-passes", commit, "go ./q TestElsewhere", testhistory.Pass)
		}
	}
	repo.git("checkout", "-q", "-b", "change")
	repo.write("README.md", "docs only\n")
	head := repo.commit("change the docs", start.Add(time.Hour))

	var landErr string
	land := func(groups ...string) (string, string, int) {
		t.Helper()
		unitsFile := filepath.Join(t.TempDir(), "units")
		args := []string{"land", "--main", "main", "--units-out", unitsFile}
		for _, group := range groups {
			args = append(args, "--group", group)
		}
		cmd := historyCommand(t, repo.spool, repo.dir, args...)
		cmd.Env = append(cmd.Env, "GOWORK=off", "GOFLAGS=", "ATTN_TEST_HISTORY_URL="+service.server.URL, "XDG_CONFIG_HOME="+config)
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		code := exitCode(cmd.Run())
		landErr = errOut.String()
		units, _ := os.ReadFile(unitsFile)
		chosen := strings.Split(strings.TrimSpace(string(units)), "\n")
		sort.Strings(chosen)
		return out.String(), strings.Join(chosen, ", "), code
	}

	olderVersion := `{"last_success":"2026-10-31T12:00:00Z","last_attempt":"2026-10-31T12:00:00Z","cursor":20,"cursor_of":"` + service.server.URL + ` github.com/` + checkoutRepository + ` .json.zst"}`
	if err := os.WriteFile(filepath.Join(repo.spool, "sync.json"), []byte(olderVersion), 0o644); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	service.down = true
	service.mu.Unlock()
	out, units, code := land("go")
	if code != 0 || units != "go ./p TestA, go ./p TestRenamed" || !strings.Contains(out, "the baseline, no shared history") || !strings.Contains(landErr, "choosing landing tests from the history already synced") {
		t.Fatalf("offline, with only an older version's sync in the spool, land exited %d and chose %s:\n%s\nstderr: %s", code, units, out, landErr)
	}

	service.mu.Lock()
	service.down = false
	service.mu.Unlock()
	out, units, code = land("go")
	if code != 0 || units != "go ./p TestA, go ./p TestRenamed, go ./q TestRecent" {
		t.Fatalf("land --group go exited %d and chose %s, want the go group's baseline, though the change touches only docs, and the uncleared failure:\n%s", code, units, out)
	}
	for _, want := range []string{
		"landing tests for " + short(head) + ":",
		"ADDED       go ./q TestRecent",
		fmt.Sprintf("failed on %s at %s; passed on 1 main revisions since, runs until %d more pass", otherLane(), short(failedAt), testhistory.ClearedAfter-1),
		"RED         go ./q TestStillRed",
		"still red on main: the sheriff owns it; landings run it once it passes on main",
		"UNAVAILABLE go ./q TestElsewhere",
		"its test file does not build on this platform",
		"MISSING     go ./p TestRenamed",
		"requested anyway, so the run fails until its baseline line names a test",
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

func TestLandLeavesFilesStillRedOnMainOutOfTheVitestSuite(t *testing.T) {
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
	repo.write("scripts/landing-tests", "group app ^app/\nvitest .\n")
	repo.write("app/src/red.test.ts", "test\n")
	failedAt := repo.commit("tests", start)
	body, err := json.Marshal(otherLaneRun("red-fails", failedAt, "vitest src/red.test.ts", testhistory.Fail, start.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	service.add("github.com/"+checkoutRepository, "red-fails", body)
	repo.git("checkout", "-q", "-b", "change")
	repo.write("app/src/example.ts", "export {};\n")
	repo.commit("change the app", start.Add(time.Hour))

	unitsFile := filepath.Join(t.TempDir(), "units")
	cmd := historyCommand(t, repo.spool, repo.dir, "land", "--main", "main", "--units-out", unitsFile, "--group", "app")
	cmd.Env = append(cmd.Env, "GOWORK=off", "GOFLAGS=", "ATTN_TEST_HISTORY_URL="+service.server.URL, "XDG_CONFIG_HOME="+config)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("land: %v\n%s", err, out)
	}
	units, err := os.ReadFile(unitsFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := "vitest .\nred vitest src/red.test.ts\n"; string(units) != want {
		t.Fatalf("units = %q, want %q", units, want)
	}
}
