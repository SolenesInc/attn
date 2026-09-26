package daemon_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const watchedPullRequestURL = "https://github.com/victorarias/attn/pull/71"

type watchedPullRequestGitHub struct {
	state, reviewDecision string
	merged, feedbackFails bool
	opinions              []map[string]any
}

func serveWatchedPullRequest(t *testing.T, gh watchedPullRequestGitHub) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		var answer any = map[string]any{"total_count": 0, "items": []any{}}
		switch {
		case strings.Contains(string(body), "PullRequestReadiness"):
			answer = map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
				"number": 71, "url": watchedPullRequestURL, "title": "Ready to ship", "state": gh.state, "merged": gh.merged,
				"headRefOid": "head-a", "headRefName": "pr-readiness", "mergeStateStatus": "CLEAN", "reviewDecision": gh.reviewDecision,
				"reactions": map[string]any{"nodes": []any{}}, "latestOpinionatedReviews": map[string]any{"nodes": gh.opinions},
				"reviews": map[string]any{"nodes": gh.opinions}, "reviewRequests": map[string]any{"nodes": []any{}},
				"commits": map[string]any{"nodes": []any{}},
			}}}}
		case strings.Contains(string(body), "PullRequestFeedback") && gh.feedbackFails:
			answer = map[string]any{"errors": []any{map[string]any{"message": "review threads unavailable"}}}
		case strings.Contains(string(body), "PullRequestFeedback"):
			answer = map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
				"comments": map[string]any{"nodes": []any{}}, "reviews": map[string]any{"nodes": []any{}}, "reviewThreads": map[string]any{"nodes": []any{}},
			}}}}
		}
		_ = json.NewEncoder(rw).Encode(answer)
	}))
	t.Cleanup(server.Close)
	t.Setenv("ATTN_MOCK_GH_URL", server.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	t.Setenv("ATTN_MOCK_GH_HOST", "github.com")
}

func watchPullRequestAs(t *testing.T, cli *client.Client, session string, mode protocol.PullRequestWatchMode, reviewer string) {
	t.Helper()
	recordPullRequest(t, cli, session, watchedPullRequestURL)
	if err := cli.WatchSessionPullRequest(session, watchedPullRequestURL, mode, reviewer); err != nil {
		t.Fatalf("%s watches the pull request: %v", session, err)
	}
}

func awaitWatchedPullRequest(app *testworld.Peer, session string, match func(protocol.SessionPullRequest) bool) protocol.SessionPullRequest {
	app.T.Helper()
	return testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return len(s.PullRequests) == 1 && match(s.PullRequests[0])
	}).PullRequests[0]
}

func TestAWatchedPullRequestIsReadyForEachWatcherInItsOwnMode(t *testing.T) {
	serveWatchedPullRequest(t, watchedPullRequestGitHub{
		state: "OPEN", reviewDecision: "REVIEW_REQUIRED",
		opinions: []map[string]any{{"id": "review-1", "state": "APPROVED", "bodyText": "Ship it", "submittedAt": "2026-09-21T10:00:00Z",
			"author": map[string]any{"__typename": "User", "id": "u-1", "login": "victor"}}},
	})
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	panes := spawnPanes(w, app, w.Path("s1"), w.Path("s2"))
	s1, s2 := panes[0].session, panes[1].session

	watchPullRequestAs(t, cli, s1, protocol.PullRequestWatchModeGreen, "")
	watchPullRequestAs(t, cli, s2, protocol.PullRequestWatchModeFormalReview, "victor")
	for session, mode := range map[string]protocol.PullRequestWatchMode{s1: protocol.PullRequestWatchModeGreen, s2: protocol.PullRequestWatchModeFormalReview} {
		watched := awaitWatchedPullRequest(app, session, func(pr protocol.SessionPullRequest) bool { return protocol.Deref(pr.ReadinessState) != "" })
		if !protocol.Deref(watched.Watching) || protocol.Deref(watched.WatchMode) != mode || protocol.Deref(watched.ReadinessState) != "ready" {
			t.Errorf("%s's pull request = %+v, want it watched in %s mode and ready", session, watched, mode)
		}
		if protocol.Deref(watched.MergeableState) != "clean" || protocol.Deref(watched.ReviewStatus) != "pending" {
			t.Errorf("%s's pull request shows mergeable %q and review %q, want clean and pending", session, protocol.Deref(watched.MergeableState), protocol.Deref(watched.ReviewStatus))
		}
		if inbox := inboxContents(readInbox(t, cli, session, 0).Items); !strings.Contains(inbox, "ready") {
			t.Errorf("%s's inbox = %q, want a note that the pull request is ready", session, inbox)
		}
	}
}

func TestAWatchedPullRequestStaysReadyWhenItsFeedbackCannotBeRead(t *testing.T) {
	serveWatchedPullRequest(t, watchedPullRequestGitHub{state: "OPEN", feedbackFails: true})
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	s1 := spawnPanes(w, app, w.Path("s1"))[0].session

	watchPullRequestAs(t, cli, s1, protocol.PullRequestWatchModeGreen, "")
	watched := awaitWatchedPullRequest(app, s1, func(pr protocol.SessionPullRequest) bool { return protocol.Deref(pr.ReadinessState) != "" })
	if protocol.Deref(watched.ReadinessState) != "ready" || protocol.Deref(watched.WatchHealth) != "delayed" || !strings.Contains(protocol.Deref(watched.WatchError), "feedback") {
		t.Errorf("the pull request = %+v, want it ready with its watch delayed on feedback", watched)
	}
}

func TestAMergedWatchedPullRequestTellsItsAgentOnceAndEndsTheWatch(t *testing.T) {
	serveWatchedPullRequest(t, watchedPullRequestGitHub{state: "MERGED", merged: true})
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	s1 := spawnPanes(w, app, w.Path("s1"))[0].session

	watchPullRequestAs(t, cli, s1, protocol.PullRequestWatchModeGreen, "")
	merged := awaitWatchedPullRequest(app, s1, func(pr protocol.SessionPullRequest) bool { return protocol.Deref(pr.ReadinessState) != "" })
	if merged.State != "merged" || protocol.Deref(merged.ReadinessState) != "merged" || protocol.Deref(merged.Watching) {
		t.Errorf("the merged pull request = %+v, want it merged and no longer watched", merged)
	}
	items := readInbox(t, cli, s1, 0).Items
	if len(items) != 1 || !strings.Contains(items[0].Content, "merged") {
		t.Errorf("the agent's inbox = %q, want one note that it merged", inboxContents(items))
	}
}
