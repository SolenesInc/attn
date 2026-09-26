package daemon_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/github/mockserver"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheDashboardKeepsWhatTheUserDidAcrossRefreshesAndRestarts(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 1, Title: "Add checkout", Role: "reviewer"})
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 2, Title: "Fix the cart", Role: "author"})
	gh.AddPR(mockserver.MockPR{Repo: "acme/docs", Number: 3, Title: "Document checkout", Role: "reviewer"})
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")

	w := newWorld(t)
	app := w.App()
	cli := w.Client()
	refreshPRs(t, app)
	all := queryPRs(t, cli, "")
	if len(all) != 3 {
		t.Fatalf("dashboard = %+v, want the three pull requests GitHub reported", all)
	}
	checkout, cart, docs := prNumbered(t, all, 1), prNumbered(t, all, 2), prNumbered(t, all, 3)
	if waiting := queryPRs(t, cli, protocol.PRStateWaiting); len(waiting) != 3 {
		t.Errorf("waiting filter = %+v, want every pull request GitHub reported waiting", waiting)
	}
	if working := queryPRs(t, cli, protocol.StateWorking); len(working) != 0 {
		t.Errorf("working filter = %+v, want none of the waiting pull requests", working)
	}

	if err := cli.ToggleMuteRepo("acme/docs"); err != nil {
		t.Fatalf("mute repo: %v", err)
	}
	fetched, err := cli.FetchPRDetails(docs.ID)
	if err != nil {
		t.Fatalf("fetch details: %v", err)
	}
	if pr := prNumbered(t, fetched, 3); !hasFetchedDetails(pr) {
		t.Fatalf("fetched details = %+v, want the details GitHub reported", pr)
	}
	if err := cli.ToggleMutePR(cart.ID); err != nil {
		t.Fatalf("mute: %v", err)
	}
	approved := testworld.Request(app, protocol.ApprovePRMessage{Cmd: protocol.CmdApprovePR, ID: checkout.ID},
		protocol.EventPRActionResult, func(r protocol.PRActionResultMessage) bool { return r.ID == checkout.ID })
	if !approved.Success {
		t.Fatalf("approve: %s", protocol.Deref(approved.Error))
	}

	refreshPRs(t, app)
	after := queryPRs(t, cli, "")
	if pr := prNumbered(t, after, 2); !pr.Muted {
		t.Error("the muted pull request came back unmuted after a refresh")
	}
	if pr := prNumbered(t, after, 1); !pr.ApprovedByMe {
		t.Error("the approval was forgotten after a refresh")
	}
	if pr := prNumbered(t, after, 3); !hasFetchedDetails(pr) {
		t.Errorf("details after a refresh = ci %q, mergeable %q, head %q; want the fetched details kept",
			protocol.Deref(pr.CIStatus), protocol.Deref(pr.MergeableState), protocol.Deref(pr.HeadSHA))
	}
	if err := cli.ToggleMutePR(cart.ID); err != nil {
		t.Fatalf("unmute: %v", err)
	}
	if pr := prNumbered(t, queryPRs(t, cli, ""), 2); pr.Muted {
		t.Error("muting twice left the pull request muted")
	}

	if err := cli.SetRepoCollapsed("acme/shop", true); err != nil {
		t.Fatalf("collapse repo: %v", err)
	}
	for _, author := range []string{"dependabot", "renovate", "renovate"} {
		app.Send(protocol.MuteAuthorMessage{Cmd: protocol.CmdMuteAuthor, Author: author})
	}
	mutedAuthors := map[string]bool{"dependabot": true, "renovate": false}
	testworld.Await(app, protocol.EventAuthorsUpdated, func(e protocol.WebSocketEvent) bool {
		return maps.Equal(authorMutes(e.Authors), mutedAuthors)
	})

	w.restart()
	cli = w.Client()
	repos, err := cli.QueryRepos()
	if err != nil {
		t.Fatalf("query repos: %v", err)
	}
	if got := repoStates(repos); got["acme/docs"] != (protocol.RepoState{Repo: "acme/docs", Muted: true}) ||
		got["acme/shop"] != (protocol.RepoState{Repo: "acme/shop", Collapsed: true}) {
		t.Errorf("repos after a restart = %+v, want acme/docs muted and acme/shop collapsed", repos)
	}
	authors, err := cli.QueryAuthors()
	if err != nil {
		t.Fatalf("query authors: %v", err)
	}
	if !maps.Equal(authorMutes(authors), mutedAuthors) {
		t.Errorf("authors after a restart = %+v, want dependabot muted and renovate unmuted", authors)
	}

	if err := cli.ToggleMuteRepo("acme/docs"); err != nil {
		t.Fatalf("unmute repo: %v", err)
	}
	repos, err = cli.QueryRepos()
	if err != nil {
		t.Fatalf("query repos: %v", err)
	}
	if repoStates(repos)["acme/docs"].Muted {
		t.Error("muting a repository twice left it muted")
	}
}

func refreshPRs(t *testing.T, app *testworld.Peer) {
	t.Helper()
	refreshed := testworld.Request(app, protocol.RefreshPRsMessage{Cmd: protocol.CmdRefreshPRs},
		protocol.EventRefreshPRsResult, func(protocol.RefreshPRsResultMessage) bool { return true })
	if !refreshed.Success {
		t.Fatalf("refresh pull requests: %s", protocol.Deref(refreshed.Error))
	}
}

func queryPRs(t *testing.T, cli *client.Client, filter string) []protocol.PR {
	t.Helper()
	prs, err := cli.QueryPRs(filter)
	if err != nil {
		t.Fatalf("query pull requests %q: %v", filter, err)
	}
	return prs
}

func prNumbered(t *testing.T, prs []protocol.PR, number int) protocol.PR {
	t.Helper()
	i := slices.IndexFunc(prs, func(pr protocol.PR) bool { return pr.Number == number })
	if i < 0 {
		t.Fatalf("no pull request #%d among %+v", number, prs)
	}
	return prs[i]
}

func repoStates(repos []protocol.RepoState) map[string]protocol.RepoState {
	byRepo := make(map[string]protocol.RepoState, len(repos))
	for _, repo := range repos {
		byRepo[repo.Repo] = repo
	}
	return byRepo
}

func hasFetchedDetails(pr protocol.PR) bool {
	return protocol.Deref(pr.CIStatus) == "success" && protocol.Deref(pr.MergeableState) == "clean" && protocol.Deref(pr.HeadSHA) == "abc123"
}

func authorMutes(authors []protocol.AuthorState) map[string]bool {
	muted := make(map[string]bool, len(authors))
	for _, author := range authors {
		muted[author.Author] = author.Muted
	}
	return muted
}
