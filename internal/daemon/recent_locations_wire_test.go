package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestRecentLocationsRankByFrecencyBeforeApplyingTheLimit(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		frequentOld, recentOnce, staleOnce := w.Path("frequent-old"), w.Path("recent-once"), w.Path("stale-once")

		workIn(t, app, staleOnce)
		for range 3 {
			workIn(t, app, frequentOld)
		}
		w.advance(time.Second)
		workIn(t, app, recentOnce)

		locations := recentLocations(app, 0)
		if got := locationPaths(locations); !slices.Equal(got, []string{frequentOld, recentOnce, staleOnce}) {
			t.Fatalf("recent locations = %v, want three earlier uses above one use now above one earlier use", got)
		}
		if locations[0].UseCount != 3 {
			t.Errorf("%s = %+v, want every use counted", frequentOld, locations[0])
		}

		for i := range 5 {
			workIn(t, app, w.Path(fmt.Sprintf("fresh-%d", i)))
		}
		if got := locationPaths(recentLocations(app, 2)); len(got) != 2 || got[0] != frequentOld {
			t.Errorf("the top two = %v, want the best-ranked location kept above the many newer ones", got)
		}
	})
}

func TestRecentLocationsCollapseWorktreesIntoTheirMainRepo(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	repo := newRepo(t, "shop")
	worktree := filepath.Join(filepath.Dir(repo), "shop--feat")
	runGit(t, repo, "worktree", "add", "-b", "feat", worktree)
	if err := os.MkdirAll(filepath.Join(worktree, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	later := filepath.Join(filepath.Dir(repo), "shop--later")
	subdirectory := filepath.Join(repo, "app")

	workIn(t, app, worktree)
	workIn(t, app, filepath.Join(worktree, "web"))
	workIn(t, app, later)
	if got := locationPaths(recentLocations(app, 0)); !slices.Contains(got, later) {
		t.Fatalf("recent locations = %v, want %s recorded while it is a plain directory", got, later)
	}
	runGit(t, repo, "worktree", "add", "-b", "later", later)
	workIn(t, app, subdirectory)

	locations := recentLocations(app, 0)
	if got := locationPaths(locations); !slices.Equal(got, []string{repo, subdirectory}) {
		t.Fatalf("recent locations = %v, want the worktrees merged into %s and its subdirectory kept apart", got, repo)
	}
	if locations[0].UseCount != 3 {
		t.Errorf("%s = %+v, want the three worktree uses merged into it", repo, locations[0])
	}
}

func workIn(t *testing.T, app *testworld.Peer, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testworld.Request(app, protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: uuid.NewString(), Title: filepath.Base(dir), Directory: dir,
	}, protocol.EventWorkspaceRegistered, func(protocol.WebSocketEvent) bool { return true })
}

func recentLocations(app *testworld.Peer, limit int) []protocol.RecentLocation {
	app.T.Helper()
	requestID := uuid.NewString()
	msg := protocol.GetRecentLocationsMessage{Cmd: protocol.CmdGetRecentLocations, RequestID: protocol.Ptr(requestID)}
	if limit > 0 {
		msg.Limit = protocol.Ptr(limit)
	}
	result := testworld.Request(app, msg, protocol.EventRecentLocationsResult, func(r protocol.RecentLocationsResultMessage) bool {
		return protocol.Deref(r.RequestID) == requestID
	})
	if !result.Success {
		app.T.Fatalf("recent locations refused: %s", protocol.Deref(result.Error))
	}
	return result.RecentLocations
}

func locationPaths(locations []protocol.RecentLocation) []string {
	out := make([]string, 0, len(locations))
	for _, location := range locations {
		out = append(out, location.Path)
	}
	return out
}
