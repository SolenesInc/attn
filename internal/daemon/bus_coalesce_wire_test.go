package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/github/mockserver"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestABulkChangePushesEachAffectedSnapshotToTheAppOnce(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	for number, title := range []string{"Add checkout", "Fix the cart", "Speed up search"} {
		gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: number + 1, Title: title, Role: "reviewer"})
	}
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")

	w := newWorld(t)
	app := w.App()
	refreshPRs(t, app)
	busCoalesceToggleRepoMute(app, "acme/shop", true)
	t.Setenv("ATTN_MOCK_GH_URL", "")
	t.Setenv("PATH", ghWarningPath(t, ""))
	w.restart()

	app = w.App()
	if len(app.Initial.Prs) != 3 {
		t.Fatalf("after a restart without GitHub the app sees %d pull requests, want the three it knew", len(app.Initial.Prs))
	}
	from := len(app.Received())
	busCoalesceToggleRepoMute(app, "acme/shop", false)
	busStatus(t, app)

	var prPushes, repoPushes []protocol.WebSocketEvent
	for _, e := range app.Received()[from:] {
		switch e.Event {
		case protocol.EventPRsUpdated:
			prPushes = append(prPushes, e)
		case protocol.EventReposUpdated:
			repoPushes = append(repoPushes, e)
		}
	}
	if len(prPushes) != 1 || len(prPushes[0].Prs) != 3 {
		t.Errorf("unmuting a repository with three pull requests pushed prs_updated %d times, want once carrying all three", len(prPushes))
	}
	if len(repoPushes) != 1 || busCoalesceRepoMuted(repoPushes[0].Repos, "acme/shop") {
		t.Errorf("unmuting a repository pushed repos_updated %d times, want once showing it unmuted", len(repoPushes))
	}
}

func busCoalesceToggleRepoMute(app *testworld.Peer, repo string, muted bool) {
	app.Send(protocol.MuteRepoMessage{Cmd: protocol.CmdMuteRepo, Repo: repo})
	testworld.Await(app, protocol.EventReposUpdated, func(e protocol.WebSocketEvent) bool {
		return busCoalesceRepoMuted(e.Repos, repo) == muted
	})
}

func busCoalesceRepoMuted(repos []protocol.RepoState, repo string) bool {
	i := slices.IndexFunc(repos, func(r protocol.RepoState) bool { return r.Repo == repo })
	return i >= 0 && repos[i].Muted
}
