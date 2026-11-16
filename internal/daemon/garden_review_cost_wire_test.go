package daemon_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	gardenReviewHistorySeeds        = 50
	gardenReviewNotesPerClosedSeed  = 21
	gardenReviewHistoryAllocBudget  = 3 << 20
	gardenReviewHistoryAllocReceipt = "receipt: on oma, 2026-11-16, this history grew one overview by 0.0-0.5 MB over 6 runs, and by 9.6 MB when the capture read every note"
)

func TestAGardenReviewOverviewCostsNoMoreWithClosedHistory(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	launches := countGitLaunches(t, w)
	gardenReviewAbandonedSeed(t, w, app, cli, "gardener", "old work")

	healthyGit, healthyBytes := gardenReviewOverviewCost(t, cli, launches)

	repo := newRepo(t, "history")
	worktree := filepath.Join(filepath.Dir(repo), "history--finished")
	runGit(t, repo, "worktree", "add", "-b", "finished", worktree)
	pane := spawnPanes(w, app, worktree)[0]
	for i := range gardenReviewHistorySeeds {
		seed := gardenReviewPlantTended(t, cli, pane.session, fmt.Sprintf("finished part %02d", i))
		for n := range gardenReviewNotesPerClosedSeed - 1 {
			if _, err := cli.SeedNote(protocol.SessionID(pane.session), seed, fmt.Sprintf("step %d of part %02d", n, i), "", false, nil); err != nil {
				t.Fatalf("note on %s: %v", seed, err)
			}
		}
		if _, err := cli.SeedTransition(protocol.SessionID(pane.session), seed, "harvest", "done", false, client.SeedTransitionOptions{}); err != nil {
			t.Fatalf("harvest %s: %v", seed, err)
		}
	}
	closePane(app, pane)
	runGit(t, repo, "worktree", "remove", "--force", worktree)

	historyGit, historyBytes := gardenReviewOverviewCost(t, cli, launches)
	if len(historyGit) > len(healthyGit) {
		t.Errorf("an overview launched git %d times with %d closed seeds behind it, %d without; the limit is no growth.\nwith history: %s\nwithout: %s",
			len(historyGit), gardenReviewHistorySeeds, len(healthyGit), strings.Join(historyGit, "; "), strings.Join(healthyGit, "; "))
	}
	if grown := int64(historyBytes) - int64(healthyBytes); grown > gardenReviewHistoryAllocBudget {
		t.Errorf("an overview allocated %d bytes more with %d closed seeds and %d notes behind it (%d without); the limit is %d bytes of growth (%s)",
			grown, gardenReviewHistorySeeds, gardenReviewHistorySeeds*gardenReviewNotesPerClosedSeed, healthyBytes, gardenReviewHistoryAllocBudget, gardenReviewHistoryAllocReceipt)
	}
}

func gardenReviewOverviewCost(t *testing.T, cli *client.Client, launches func() []string) ([]string, uint64) {
	t.Helper()
	gardenReviewShow(t, cli, "")
	before := len(launches())
	var start, end runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&start)
	if shown := gardenReviewShow(t, cli, ""); shown.CandidateCount != 1 {
		t.Fatalf("the overview counts %d candidates, want only the abandoned seed", shown.CandidateCount)
	}
	runtime.ReadMemStats(&end)
	return launches()[before:], end.TotalAlloc - start.TotalAlloc
}

func countGitLaunches(t *testing.T, w *world) func() []string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(w.Dir, "git-shim")
	log := filepath.Join(w.Dir, "git-launches.log")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	shim := fmt.Sprintf("#!/bin/sh\nprintf '%%s: git %%s\\n' \"$PWD\" \"$*\" >> %q\nexec %q \"$@\"\n", log, git)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		raw, err := os.ReadFile(log)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	}
}
