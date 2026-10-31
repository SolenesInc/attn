package daemon_test

import (
	"maps"
	"net/http"
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

	gh.Reset()
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 1, Title: "Checkout is ready", Role: "reviewer"})
	app = w.App()
	refreshPRs(t, app)
	testworld.Await(app, protocol.EventPRsUpdated, func(e protocol.WebSocketEvent) bool {
		return len(e.Prs) == 1 && e.Prs[0].ID == checkout.ID && e.Prs[0].Title == "Checkout is ready"
	})
	gh.Close()
	w.restart()
	remaining := queryPRs(t, w.Client(), "")
	if len(remaining) != 1 || remaining[0].ID != checkout.ID || remaining[0].Title != "Checkout is ready" {
		t.Fatalf("dashboard after removal and restart = %+v, want only the renamed checkout PR", remaining)
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

func TestAPollPublishesAPullRequestUpdateOnlyWhenGitHubReportsAChange(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 1, Title: "Add checkout", Role: "reviewer"})
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")

	w := newWorld(t)
	app := w.App()
	refreshPRs(t, app)
	refreshPRs(t, app)
	status := busStatus(t, app)
	if appeared, updated := producedBy(status, "pr.appeared"), producedBy(status, "pr.updated"); appeared != 1 || updated != 0 {
		t.Errorf("two polls of the same pull request published %d pr.appeared and %d pr.updated, want 1 and 0", appeared, updated)
	}

	gh.Reset()
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 1, Title: "Checkout is ready", Role: "reviewer"})
	refreshPRs(t, app)
	refreshPRs(t, app)
	if updated := producedBy(busStatus(t, app), "pr.updated"); updated != 1 {
		t.Errorf("a renamed pull request polled twice published %d pr.updated, want 1", updated)
	}
}

func TestPullRequestDetailsPublishAChangeOnlyWhenGitHubReportsOne(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	checkout := mockserver.MockPR{Repo: "acme/shop", Number: 1, Title: "Add checkout", Role: "reviewer"}
	cart := mockserver.MockPR{Repo: "acme/shop", Number: 2, Title: "Fix the cart", Role: "author"}
	gh.AddPR(checkout)
	gh.AddPR(cart)
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")

	w := newWorld(t)
	app := w.App()
	for producedBy(busStatus(t, app), "pr.details.changed") < 2 {
		testworld.Await[protocol.WebSocketEvent](app, protocol.EventPRsUpdated, nil)
	}
	checkoutID := prNumbered(t, queryPRs(t, w.Client(), ""), checkout.Number).ID
	before := producedBy(busStatus(t, app), "pr.details.changed")
	if before != 2 {
		t.Fatalf("GitHub's first details for two pull requests published %d pr.details.changed, want 2", before)
	}

	fetchPRDetails(t, app, checkoutID)
	fetchPRDetails(t, app, checkoutID)
	if published := producedBy(busStatus(t, app), "pr.details.changed") - before; published != 0 {
		t.Errorf("two reads of unchanged details published %d pr.details.changed, want 0", published)
	}

	setMergeableState(gh, "unstable", checkout, cart)
	if pr := prNumbered(t, fetchPRDetails(t, app, checkoutID), 1); protocol.Deref(pr.CIStatus) != "pending" {
		t.Fatalf("details after CI started = CI %q, mergeable %q; want CI pending", protocol.Deref(pr.CIStatus), protocol.Deref(pr.MergeableState))
	}
	if published := producedBy(busStatus(t, app), "pr.details.changed") - before; published != 1 {
		t.Errorf("a read after CI started published %d pr.details.changed, want 1", published)
	}

	setMergeableState(gh, "dirty", checkout, cart)
	app.Send(protocol.PRVisitedMessage{Cmd: protocol.CmdPRVisited, ID: checkoutID})
	prFlowAwait(app, checkoutID, func(pr protocol.PR) bool { return protocol.Deref(pr.CIStatus) == "failure" })
}

func TestAnOlderDetailsReadNeverOverwritesANewerOne(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")

	w := newWorld(t)
	cli := w.Client()
	if err := cli.ToggleMuteRepo("acme/docs"); err != nil {
		t.Fatalf("mute repo: %v", err)
	}
	app := w.App()
	docs := mockserver.MockPR{Repo: "acme/docs", Number: 3, Title: "Document checkout", Role: "reviewer"}
	gh.AddPR(docs)
	refreshPRs(t, app)
	id := prNumbered(t, queryPRs(t, cli, ""), docs.Number).ID
	fetchPRDetails(t, app, id)
	facts := func() int { return producedBy(busStatus(t, app), "pr.details.changed") }

	before := facts()
	older := startDetailsReadHeldAtReviews(t, gh, app, docs, id)
	setPRDetails(gh, docs, "unstable", "", 0)
	if pr := prNumbered(t, fetchPRDetails(t, app, id), docs.Number); protocol.Deref(pr.CIStatus) != "pending" {
		t.Fatalf("newer read = CI %q, mergeable %q; want the pending CI GitHub now reports", protocol.Deref(pr.CIStatus), protocol.Deref(pr.MergeableState))
	}
	assertDetailsRead(t, "an older read answering after a newer change", older(), docs.Number, "pending", "abc123")
	assertStoredDetails(t, cli, docs.Number, "pending", "abc123", false)
	if published := facts() - before; published != 1 {
		t.Errorf("an older read answering after a newer change published %d pr.details.changed in all, want only the newer change's 1", published)
	}

	before = facts()
	setPRDetails(gh, docs, "dirty", "def456", 0)
	older = startDetailsReadHeldAtReviews(t, gh, app, docs, id)
	setPRDetails(gh, docs, "unstable", "", 0)
	fetchPRDetails(t, app, id)
	assertDetailsRead(t, "an older read answering after a newer unchanged read", older(), docs.Number, "pending", "abc123")
	assertStoredDetails(t, cli, docs.Number, "pending", "abc123", false)
	if published := facts() - before; published != 0 {
		t.Errorf("an older read answering after a newer unchanged read published %d pr.details.changed, want 0", published)
	}

	before = facts()
	setPRDetails(gh, docs, "dirty", "def456", 0)
	older = startDetailsReadHeldAtReviews(t, gh, app, docs, id)
	setPRDetails(gh, docs, "dirty", "def456", http.StatusBadGateway)
	failed := testworld.Request(app, protocol.FetchPRDetailsMessage{Cmd: protocol.CmdFetchPRDetails, ID: id},
		protocol.EventFetchPRDetailsResult, func(protocol.FetchPRDetailsResultMessage) bool { return true })
	if failed.Success {
		t.Fatalf("a read GitHub answered with %d succeeded: %+v", http.StatusBadGateway, failed.Prs)
	}
	assertDetailsRead(t, "an older read answering after a newer read failed", older(), docs.Number, "failure", "def456")
	assertStoredDetails(t, cli, docs.Number, "failure", "def456", true)
	if published := facts() - before; published != 1 {
		t.Errorf("an older read answering after a newer read failed published %d pr.details.changed, want its own 1", published)
	}
}

func startDetailsReadHeldAtReviews(t *testing.T, gh *mockserver.Server, app *testworld.Peer, pr mockserver.MockPR, id string) (answer func() protocol.FetchPRDetailsResultMessage) {
	t.Helper()
	held, release := gh.HoldReviews(pr.Repo, pr.Number)
	t.Cleanup(release)
	app.Send(protocol.FetchPRDetailsMessage{Cmd: protocol.CmdFetchPRDetails, ID: id})
	awaitHeldReviews(t, held, id)
	return func() protocol.FetchPRDetailsResultMessage {
		t.Helper()
		release()
		return testworld.Await(app, protocol.EventFetchPRDetailsResult, func(protocol.FetchPRDetailsResultMessage) bool { return true })
	}
}

func setPRDetails(gh *mockserver.Server, pr mockserver.MockPR, mergeableState, headSHA string, status int) {
	gh.Reset()
	pr.MergeableState, pr.HeadSHA, pr.DetailsHTTPStatus = mergeableState, headSHA, status
	gh.AddPR(pr)
}

func assertDetailsRead(t *testing.T, what string, read protocol.FetchPRDetailsResultMessage, number int, ci, head string) {
	t.Helper()
	if !read.Success {
		t.Fatalf("%s: %s", what, protocol.Deref(read.Error))
	}
	if pr := prNumbered(t, read.Prs, number); protocol.Deref(pr.CIStatus) != ci || protocol.Deref(pr.HeadSHA) != head {
		t.Errorf("%s returned CI %q, mergeable %q, head %q; want CI %q, head %q", what,
			protocol.Deref(pr.CIStatus), protocol.Deref(pr.MergeableState), protocol.Deref(pr.HeadSHA), ci, head)
	}
}

func assertStoredDetails(t *testing.T, cli *client.Client, number int, ci, head string, hot bool) {
	t.Helper()
	pr := prNumbered(t, queryPRs(t, cli, ""), number)
	if protocol.Deref(pr.CIStatus) != ci || protocol.Deref(pr.HeadSHA) != head {
		t.Errorf("stored details = CI %q, mergeable %q, head %q; want CI %q, head %q",
			protocol.Deref(pr.CIStatus), protocol.Deref(pr.MergeableState), protocol.Deref(pr.HeadSHA), ci, head)
	}
	if isHot := protocol.Deref(pr.HeatState) == protocol.HeatStateHot; isHot != hot {
		t.Errorf("stored heat = %q, want hot %v: only an applied read that moves the head heats a pull request", protocol.Deref(pr.HeatState), hot)
	}
}

func fetchPRDetails(t *testing.T, app *testworld.Peer, id string) []protocol.PR {
	t.Helper()
	fetched := testworld.Request(app, protocol.FetchPRDetailsMessage{Cmd: protocol.CmdFetchPRDetails, ID: id},
		protocol.EventFetchPRDetailsResult, func(protocol.FetchPRDetailsResultMessage) bool { return true })
	if !fetched.Success {
		t.Fatalf("fetch details %s: %s", id, protocol.Deref(fetched.Error))
	}
	return fetched.Prs
}

func setMergeableState(gh *mockserver.Server, state string, checkout, cart mockserver.MockPR) {
	gh.Reset()
	checkout.MergeableState = state
	gh.AddPR(checkout)
	gh.AddPR(cart)
}
