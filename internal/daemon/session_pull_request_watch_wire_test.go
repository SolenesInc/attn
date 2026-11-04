package daemon_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const watchedPullRequestURL = "https://github.com/victorarias/attn/pull/71"

type watchedPullRequestGitHub struct {
	state, reviewDecision, mergeStateStatus string
	merged, feedbackFails                   bool
	opinions, comments                      []map[string]any
}

type watchedPullRequest struct {
	mu sync.Mutex
	gh watchedPullRequestGitHub
}

func (p *watchedPullRequest) set(change func(gh *watchedPullRequestGitHub)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	change(&p.gh)
}

func (p *watchedPullRequest) current() watchedPullRequestGitHub {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gh
}

func serveWatchedPullRequest(t *testing.T, initial watchedPullRequestGitHub) *watchedPullRequest {
	t.Helper()
	served := &watchedPullRequest{gh: initial}
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Connection", "close")
		body, _ := io.ReadAll(r.Body)
		gh := served.current()
		mergeStateStatus := gh.mergeStateStatus
		if mergeStateStatus == "" {
			mergeStateStatus = "CLEAN"
		}
		var answer any = map[string]any{"total_count": 0, "items": []any{}}
		switch {
		case strings.Contains(string(body), "PullRequestReadiness"):
			answer = map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
				"number": 71, "url": watchedPullRequestURL, "title": "Ready to ship", "state": gh.state, "merged": gh.merged,
				"author":     map[string]any{"__typename": "User", "login": "victor"},
				"headRefOid": "head-a", "headRefName": "pr-readiness", "mergeStateStatus": mergeStateStatus, "reviewDecision": gh.reviewDecision,
				"reactions": map[string]any{"nodes": []any{}}, "latestOpinionatedReviews": map[string]any{"nodes": gh.opinions},
				"reviews": map[string]any{"nodes": gh.opinions}, "reviewRequests": map[string]any{"nodes": []any{}},
				"commits": map[string]any{"nodes": []any{}},
			}}}}
		case strings.Contains(string(body), "PullRequestFeedback") && gh.feedbackFails:
			answer = map[string]any{"errors": []any{map[string]any{"message": "review threads unavailable"}}}
		case strings.Contains(string(body), "PullRequestFeedback"):
			answer = map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
				"comments": map[string]any{"nodes": append([]map[string]any{}, gh.comments...)}, "reviews": map[string]any{"nodes": []any{}}, "reviewThreads": map[string]any{"nodes": []any{}},
			}}}}
		}
		_ = json.NewEncoder(rw).Encode(answer)
	}))
	t.Cleanup(server.Close)
	t.Setenv("ATTN_MOCK_GH_URL", server.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	t.Setenv("ATTN_MOCK_GH_HOST", "github.com")
	return served
}

func watchPullRequestAs(t *testing.T, cli *client.Client, session, seed string, mode protocol.PullRequestWatchMode, reviewer string) {
	t.Helper()
	recordPullRequest(t, cli, session, watchedPullRequestURL)
	if err := cli.WatchSessionPullRequest(protocol.SessionID(session), seed, watchedPullRequestURL, mode, reviewer); err != nil {
		t.Fatalf("%s watches the pull request for seed %q: %v", session, seed, err)
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

	watchPullRequestAs(t, cli, s1, "", protocol.PullRequestWatchModeGreen, "")
	watchPullRequestAs(t, cli, s2, "", protocol.PullRequestWatchModeFormalReview, "victor")
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

	watchPullRequestAs(t, cli, s1, "", protocol.PullRequestWatchModeGreen, "")
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

	watchPullRequestAs(t, cli, s1, "", protocol.PullRequestWatchModeGreen, "")
	merged := awaitWatchedPullRequest(app, s1, func(pr protocol.SessionPullRequest) bool { return protocol.Deref(pr.ReadinessState) != "" })
	if merged.State != "merged" || protocol.Deref(merged.ReadinessState) != "merged" || protocol.Deref(merged.Watching) {
		t.Errorf("the merged pull request = %+v, want it merged and no longer watched", merged)
	}
	items := readInbox(t, cli, s1, 0).Items
	if len(items) != 1 || !strings.Contains(items[0].Content, "merged") {
		t.Errorf("the agent's inbox = %q, want one note that it merged", inboxContents(items))
	}
}

func TestAPullRequestWatchedOnASeedReportsToTheSeedsNextTender(t *testing.T) {
	gh := serveWatchedPullRequest(t, watchedPullRequestGitHub{state: "OPEN", mergeStateStatus: "BLOCKED"})
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		first := w.Spawn(app, fakeagent.Codex, w.Path("first"))
		next := w.Spawn(app, fakeagent.Claude, w.Path("next"))
		seed := plantSeedAs(t, cli, first, "Ship the readiness check")
		moveDelegationSeed(t, cli, first, seed, "tend", "")

		watchPullRequestAs(t, cli, first, seed, protocol.PullRequestWatchModeGreen, "")
		w.advance(time.Second)
		if pr := onlyPullRequest(t, cli, first); protocol.Deref(pr.ReadinessState) != "blocked" {
			t.Fatalf("the pull request reads %+v, want it blocked before the handover", pr)
		}
		if _, err := cli.SeedTransition(protocol.SessionID(next), seed, "tend", "", true, client.SeedTransitionOptions{}); err != nil {
			t.Fatalf("%s takes over %s: %v", next, seed, err)
		}
		if watched, err := cli.SeedPullRequests(protocol.SessionID(next), seed); err != nil || len(watched) != 1 || watched[0].URL != watchedPullRequestURL || !protocol.Deref(watched[0].Watching) {
			t.Fatalf("after the handover %s lists %+v (%v), want the watch the seed kept", seed, watched, err)
		}
		if _, err := cli.SetSetting(client.SettingsCaller{SessionID: protocol.SessionID(next)}, "pull_requests.own_comments", "ring"); err != nil {
			t.Fatalf("set pull_requests.own_comments to ring: %v", err)
		}

		gone := w.terminal(first)
		gone.HoldExitDelivery()
		gone.Exit(0)
		w.restart()
		app, cli = w.App(), w.Client()
		for _, s := range app.Initial.Sessions {
			if string(s.ID) == first {
				t.Fatalf("after its terminal ended unseen, %s is still listed as %s, want it gone", first, s.State)
			}
		}

		gh.set(func(gh *watchedPullRequestGitHub) {
			gh.mergeStateStatus = "CLEAN"
			gh.comments = append(gh.comments, map[string]any{
				"id": "comment-author", "bodyText": "Rename the flag next", "createdAt": "2026-11-04T10:00:00Z", "viewerDidAuthor": true,
				"author": map[string]any{"__typename": "User", "login": "victor"},
			})
		})
		w.advance(protocol.HeatHotInterval)
		if inbox := inboxContents(readInbox(t, cli, next, 0).Items); !strings.Contains(inbox, "ready") || !strings.Contains(inbox, "victor: Rename the flag next") {
			t.Errorf("after the session that started the watch is gone, the seed's next tender's inbox = %q, want the ready note and the author's comment its profile rings for", inbox)
		}
	})
}

func TestClosingASeedStopsItsPullRequestWatchesAndWithdrawsTheirUnreadNews(t *testing.T) {
	for _, close := range []struct{ verb, reason string }{{"harvest", "shipped"}, {"wither", ""}} {
		t.Run(close.verb, func(t *testing.T) {
			serveWatchedPullRequest(t, watchedPullRequestGitHub{state: "OPEN"})
			w := newWorld(t, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			tender := spawnPanes(w, app, w.Path("tender"))[0].session
			seed := plantSeedAs(t, cli, tender, "Ship the readiness check")
			moveDelegationSeed(t, cli, tender, seed, "tend", "")
			watchPullRequestAs(t, cli, tender, seed, protocol.PullRequestWatchModeGreen, "")
			awaitWatchedPullRequest(app, tender, func(pr protocol.SessionPullRequest) bool { return protocol.Deref(pr.ReadinessState) == "ready" })

			moveDelegationSeed(t, cli, tender, seed, close.verb, close.reason)
			if watched, err := cli.SeedPullRequests(protocol.SessionID(tender), seed); err != nil || len(watched) != 0 {
				t.Errorf("after %s, %s lists %+v (%v), want no watches", close.verb, seed, watched, err)
			}
			if err := cli.WatchSessionPullRequest(protocol.SessionID(tender), seed, watchedPullRequestURL, protocol.PullRequestWatchModeGreen, ""); err == nil || !strings.Contains(err.Error(), seed) {
				t.Errorf("watching for the closed seed = %v, want a refusal naming %s", err, seed)
			}
			moveDelegationSeed(t, cli, tender, seed, "replant", "")
			moveDelegationSeed(t, cli, tender, seed, "tend", "")
			if inbox := inboxContents(readInbox(t, cli, tender, 0).Items); strings.Contains(inbox, "ready") {
				t.Errorf("tending %s again after %s, the inbox = %q, want the unread pull request news withdrawn", seed, close.verb, inbox)
			}
		})
	}
}
