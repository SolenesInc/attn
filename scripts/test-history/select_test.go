package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/testhistory"
)

type checkoutRepo struct {
	t     *testing.T
	dir   string
	spool string
}

func newCheckout(t *testing.T) *checkoutRepo {
	repo := &checkoutRepo{t: t, dir: t.TempDir(), spool: t.TempDir()}
	repo.git("init", "-q", "-b", "main")
	repo.write("go.mod", "module example.invalid/selection\n\ngo 1.22\n")
	return repo
}

func (r *checkoutRepo) write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *checkoutRepo) git(args ...string) string {
	r.t.Helper()
	return r.gitAt(time.Now(), args...)
}

func (r *checkoutRepo) gitAt(when time.Time, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
	cmd.Dir = r.dir
	stamp := when.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+stamp, "GIT_AUTHOR_DATE="+stamp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *checkoutRepo) commit(message string, when time.Time) string {
	r.t.Helper()
	r.gitAt(when, "add", "-A")
	r.gitAt(when, "commit", "-q", "-m", message)
	return r.git("rev-parse", "HEAD")
}

func (r *checkoutRepo) history(args ...string) (string, int) {
	r.t.Helper()
	cmd := historyCommand(r.t, r.spool, r.dir, args...)
	cmd.Env = append(cmd.Env, "GOWORK=off", "GOFLAGS=")
	out, err := cmd.Output()
	if exitErr, ok := err.(*exec.ExitError); ok {
		r.t.Logf("%s stderr:\n%s", strings.Join(args, " "), exitErr.Stderr)
	}
	return string(out), exitCode(err)
}

func (r *checkoutRepo) runTests(packages ...string) {
	r.t.Helper()
	if out, code := r.history(append([]string{"capture", "go", "--", "-count=1"}, packages...)...); code != 0 {
		r.t.Fatalf("capture exited %d:\n%s", code, out)
	}
}

func (r *checkoutRepo) receive(receipt testhistory.Receipt) {
	r.t.Helper()
	data, err := json.Marshal(receipt)
	if err != nil {
		r.t.Fatal(err)
	}
	dir := filepath.Join(r.spool, "shared")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, receipt.InvocationID+".json"), data, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func linuxFailure(id, commit, unit string, finished time.Time) testhistory.Receipt {
	exit := testhistory.Exit{Code: 1}
	return testhistory.Receipt{
		Schema: testhistory.Schema, InvocationID: id, Runner: testhistory.RunnerGo,
		Source:      testhistory.Source{Kind: "ci", RunID: "1", Attempt: "1", Job: "daemon"},
		Revision:    testhistory.Revision{Commit: commit},
		Lane:        testhistory.Lane{OS: "linux", Arch: "amd64"},
		Diagnostics: testhistory.Diagnostics{MachineClass: "github-hosted 4cpu/16GB"},
		Command:     testhistory.Command{StartedAt: finished.Add(-time.Minute), FinishedAt: &finished, Exit: &exit},
		Results:     []testhistory.Result{{Unit: unit, Attempt: 1, Outcome: testhistory.Fail}},
		Complete:    true,
	}
}

const testSource = `package %s

import "testing"

func Test%s(t *testing.T) {
	t.Log("%s")
}

func Test%s(t *testing.T) {}
`

func decode[T any](t *testing.T, out string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(out), &value); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	return value
}

func choiceOf(t *testing.T, selection testhistory.Selection, unit string) testhistory.Choice {
	t.Helper()
	for _, choice := range selection.Choices {
		if choice.Unit == unit {
			return choice
		}
	}
	t.Fatalf("%s is not a candidate in %+v", unit, selection.Choices)
	return testhistory.Choice{}
}

func TestSelectRanksTheCheckoutsChangeFromItsHistory(t *testing.T) {
	repo := newCheckout(t)
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	repo.write("scripts/landing-tests", "group go \\.go$\ngo ./p TestA\ngo ./p TestGone\n")
	repo.write("p/p_test.go", fmt.Sprintf(testSource, "p", "A", "first", "B"))
	repo.write("q/q_test.go", fmt.Sprintf(testSource, "q", "C", "first", "D"))
	admitted := repo.commit("admit the baseline", start)
	repo.runTests("./p", "./q")
	repo.receive(linuxFailure("ci-c-fails", admitted, "go ./q TestC", start.Add(time.Minute)))
	repo.receive(linuxFailure("ci-native-fails", admitted, "native agent-reload", start.Add(time.Minute)))

	repo.git("checkout", "-q", "-b", "side")
	repo.write("side.txt", "merged, not first-parent\n")
	sideCommit := repo.commit("work merged into main", start.Add(2*time.Minute))
	repo.receive(linuxFailure("ci-d-fails-off-main", sideCommit, "go ./q TestD", start.Add(3*time.Minute)))
	repo.git("checkout", "-q", "main")
	repo.gitAt(start.Add(4*time.Minute), "merge", "-q", "--no-ff", "-m", "merge side", "side")

	repo.write("q/q_test.go", fmt.Sprintf(testSource, "q", "C", "second", "D"))
	landed := repo.commit("land on main", time.Now().Add(time.Hour))
	repo.runTests("./p", "./q")
	repo.receive(linuxFailure("ci-a-fails", landed, "go ./p TestA", time.Now().Add(2*time.Hour)))

	repo.git("checkout", "-q", "-b", "change")
	repo.write("p/p_test.go", fmt.Sprintf(testSource, "p", "A", "first", "B")+"\nfunc TestNew(t *testing.T) {}\n")
	repo.write("p/testdata/fixture/f_test.go", fmt.Sprintf(testSource, "fixture", "Fixture", "x", "Other"))

	t.Run("select", func(t *testing.T) {
		out, code := repo.history("select", "--main", "main", "--mode", "extend", "--json")
		if code != 0 {
			t.Fatalf("select exited %d", code)
		}
		selection := decode[testhistory.Selection](t, out)
		if selection.Policy != testhistory.SuccessCount || strings.Join(selection.Groups, " ") != "go" {
			t.Fatalf("policy %s, groups %v", selection.Policy, selection.Groups)
		}
		changed := choiceOf(t, selection, "go ./p TestNew")
		if !changed.Changed || changed.Status != testhistory.Selected {
			t.Fatalf("changed test: %+v", changed)
		}
		for _, choice := range selection.Choices {
			if strings.Contains(choice.Unit, "testdata") {
				t.Fatalf("a test under testdata, which go test skips, became a candidate: %+v", choice)
			}
			if choice.Unit == "go ./p TestB" {
				t.Fatalf("TestB did not change, never failed and is not in the baseline, but is a candidate: %+v", choice)
			}
		}
		baseline := choiceOf(t, selection, "go ./p TestA")
		if baseline.Status != testhistory.Selected || baseline.Standing.Anchor.Kind != testhistory.AnchorFailure || baseline.Standing.Lane != "linux/amd64" {
			t.Fatalf("baseline TestA: %+v %+v", baseline, baseline.Standing)
		}
		nominated := choiceOf(t, selection, "go ./q TestC")
		standing := nominated.Standing
		if nominated.Status != testhistory.Selected || standing.Lane != "linux/amd64" || standing.PassesSince != 0 || standing.Anchor.Commit != admitted {
			t.Fatalf("a Linux failure cleared only by Mac passes must stand uncleared: %+v %+v", nominated, standing)
		}
		if gone := choiceOf(t, selection, "go ./p TestGone"); gone.Status != testhistory.Unavailable || gone.Note == "" {
			t.Fatalf("missing baseline test: %+v", gone)
		}
		if nominated.Command != "./scripts/test-go.sh --packages -count=1 -run '^TestC$' ./q" {
			t.Fatalf("rerun command %q", nominated.Command)
		}
	})

	t.Run("the same from a subdirectory", func(t *testing.T) {
		cmd := historyCommand(t, repo.spool, filepath.Join(repo.dir, "q"), "select", "--main", "main", "--mode", "extend", "--json")
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		root, _ := repo.history("select", "--main", "main", "--mode", "extend", "--json")
		if string(out) != root {
			t.Fatalf("select from q/ printed:\n%s\nfrom the root:\n%s", out, root)
		}
	})

	t.Run("replace stays within the baseline's cost", func(t *testing.T) {
		out, _ := repo.history("select", "--main", "main", "--json")
		selection := decode[testhistory.Selection](t, out)
		budget := selection.Budgets[0]
		if budget.BudgetMS == nil || budget.EstimateMS > *budget.BudgetMS {
			t.Fatalf("budget %+v", budget)
		}
		if choiceOf(t, selection, "go ./p TestNew").Status != testhistory.Selected {
			t.Fatalf("a changed test must be selected outside the budget")
		}
	})

	t.Run("baseline", func(t *testing.T) {
		out, _ := repo.history("select", "--main", "main", "--mode", "baseline", "--json")
		selection := decode[testhistory.Selection](t, out)
		var units []string
		for _, choice := range selection.Choices {
			units = append(units, string(choice.Status)+" "+choice.Unit)
		}
		if got := strings.Join(units, ", "); got != "selected go ./p TestA, unavailable go ./p TestGone" {
			t.Fatalf("baseline = %s", got)
		}
	})

	t.Run("explain", func(t *testing.T) {
		out, code := repo.history("explain", "--main", "main", "--test", "go ./q TestC", "--json")
		explained := decode[explanation](t, out)
		if code != 0 || explained.Standing == nil || len(explained.Standing.Lanes) != 1 || !explained.Priced {
			t.Fatalf("explain exited %d: %+v", code, explained)
		}
		if evidence := explained.Standing.Anchor.Evidence; len(evidence) != 1 || evidence[0] != "ci-c-fails" {
			t.Fatalf("evidence = %v", evidence)
		}
		out, _ = repo.history("explain", "--main", "main", "--test", "go ./q TestD", "--json")
		if offMain := decode[explanation](t, out); offMain.Standing != nil {
			t.Fatalf("a failure on a merged branch's own commit counted as main evidence: %+v", offMain.Standing)
		}
	})

	t.Run("pending", func(t *testing.T) {
		out, code := repo.history("pending", "--main", "main", "--json")
		pending := decode[[]pendingUnit](t, out)
		var units []string
		for _, unit := range pending {
			if !strings.HasPrefix(unit.Reason, "unavailable") {
				t.Fatalf("%s pending as %q", unit.Unit, unit.Reason)
			}
			if strings.HasPrefix(unit.Unit, "native ") && !strings.Contains(unit.Reason, "no landing recipe") {
				t.Fatalf("%s pending as %q; a native scenario is unavailable because no landing recipe runs it", unit.Unit, unit.Reason)
			}
			units = append(units, unit.Unit)
		}
		if got := strings.Join(units, ", "); code != 0 || got != "go ./p TestGone, native agent-reload" {
			t.Fatalf("pending exited %d: %s", code, got)
		}
	})

	t.Run("replay", func(t *testing.T) {
		repo.write("q/q_test.go", "package q\n")
		out, code := repo.history("replay", "--main", "main", "--from", admitted)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		point := decode[replayPoint](t, lines[len(lines)-1])
		if code != 0 || len(lines) != 2 || point.Commit != landed {
			t.Fatalf("replay exited %d at %s", code, point.Commit)
		}
		if point.Freshness.NewestEvidence != admitted {
			t.Fatalf("replay at %s used evidence from %s; only earlier main commits count", landed, point.Freshness.NewestEvidence)
		}
		if strings.Join(point.Changed, " ") != "go ./q TestC" {
			t.Fatalf("changed tests at the landed commit = %v, want its own change, not the checkout's", point.Changed)
		}
		if failures := point.Failures["go ./p TestA"]; len(failures) != 1 || failures[0] != "linux/amd64" {
			t.Fatalf("failures at the landed commit = %v", point.Failures)
		}
		shared, _ := filepath.Glob(filepath.Join(repo.spool, "shared", "*.json"))
		if want := len(sealedReceipts(t, repo.spool)) + len(shared) - 1; point.Available != want {
			t.Fatalf("replay used %d receipts, want %d: all but ci-a-fails, which finished after the commit", point.Available, want)
		}
		for name, selection := range point.Selections {
			if strings.Join(selection.Included, " ") != "go ./p TestA" {
				t.Fatalf("%s included %v", name, selection.Included)
			}
		}
		if selected := strings.Join(point.Selections["baseline"].Selected, " "); selected != "go ./p TestA" {
			t.Fatalf("baseline selected %s", selected)
		}
		if extended := point.Selections["extend-all"].Selected; len(extended) != 2 {
			t.Fatalf("extend-all selected %v, want TestA and the nominated TestC", extended)
		}
	})
}
