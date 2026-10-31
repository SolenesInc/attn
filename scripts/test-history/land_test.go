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
)

const sleepingTest = `package %s

import (
	"testing"
	"time"
)

func Test%s(t *testing.T) { time.Sleep(%d * time.Millisecond) }
`

func TestLandAddsFailedTestsOnlyWithinTheBaselinesCostAndChoosesTheBaselineBeforeAnySync(t *testing.T) {
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
	repo.write("scripts/landing-tests", "group go \\.go$\ngo ./p TestA\n")
	repo.write("p/p_test.go", fmt.Sprintf(sleepingTest, "p", "A", 800))
	repo.write("q/q_test.go", fmt.Sprintf(sleepingTest, "q", "Slow", 1600))
	repo.write("r/r_test.go", fmt.Sprintf(sleepingTest, "r", "Quick", 0))
	admitted := repo.commit("admit the baseline", start)
	repo.runTests("./p", "./q", "./r")
	for _, unit := range []string{"go ./q TestSlow", "go ./r TestQuick"} {
		id := "ci-" + strings.Fields(unit)[2]
		body, err := json.Marshal(otherLaneFailure(id, admitted, unit, start.Add(time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		service.add("github.com/"+checkoutRepository, id, body)
	}
	repo.git("checkout", "-q", "-b", "change")
	repo.write("p/p.go", "package p\n")
	head := repo.commit("change p", start.Add(2*time.Minute))

	land := func() (string, []string) {
		t.Helper()
		unitsFile := filepath.Join(t.TempDir(), "units")
		out, code := repo.history("land", "--main", "main", "--units-out", unitsFile)
		if code != 0 {
			t.Fatalf("land exited %d:\n%s", code, out)
		}
		units, err := os.ReadFile(unitsFile)
		if err != nil {
			t.Fatal(err)
		}
		chosen := strings.Split(strings.TrimSpace(string(units)), "\n")
		sort.Strings(chosen)
		return out, chosen
	}

	out, units := land()
	if strings.Join(units, ", ") != "go ./p TestA" || !strings.Contains(out, "the baseline, no shared history") || !strings.Contains(out, "run go tool test-history sync") {
		t.Fatalf("before any sync, land chose %v:\n%s", units, out)
	}

	sync := historyCommand(t, repo.spool, repo.dir, "sync")
	sync.Env = append(sync.Env, "ATTN_TEST_HISTORY_URL="+service.server.URL, "XDG_CONFIG_HOME="+config)
	if synced, err := sync.CombinedOutput(); err != nil {
		t.Fatalf("sync: %v\n%s", err, synced)
	}

	out, units = land()
	if strings.Join(units, ", ") != "go ./p TestA, go ./r TestQuick" {
		t.Fatalf("land chose %v, want the baseline plus the failed test that fits its cost:\n%s", units, out)
	}
	for _, want := range []string{
		"landing tests for " + short(head) + ": success-count/1",
		"ADDED       go ./r TestQuick",
		"failed on " + otherLane().String() + " at " + short(admitted) + ", 0 passing main revisions since",
		"OVER-BUDGET go ./q TestSlow",
		"past the limit of",
		"baseline: 1 units",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("land output lacks %q:\n%s", want, out)
		}
	}
}
