package daemon_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

var graphQLOperation = regexp.MustCompile(`(?:query|mutation)\s+(\w+)`)

func servePagedEnterpriseGitHub(t *testing.T, pages map[string]string) (set func(operation, answer string)) {
	t.Helper()
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Connection", "close")
		if r.URL.Path != "/api/graphql" {
			_, _ = rw.Write([]byte(`{"total_count":0,"items":[]}`))
			return
		}
		var request struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		mu.Lock()
		defer mu.Unlock()
		operation := graphQLOperation.FindStringSubmatch(request.Query)
		if operation == nil || pages[operation[1]] == "" {
			http.Error(rw, "unexpected query", http.StatusBadRequest)
			return
		}
		_, _ = rw.Write([]byte(pages[operation[1]]))
	}))
	t.Cleanup(server.Close)
	t.Setenv("ATTN_MOCK_GH_URL", server.URL+"/api/v3")
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	t.Setenv("ATTN_MOCK_GH_HOST", "github.com")
	return func(operation, answer string) {
		mu.Lock()
		defer mu.Unlock()
		pages[operation] = answer
	}
}

const pagedReadinessFirstPage = `{"data":{"repository":{"pullRequest":{
	"number":71,"url":"https://github.com/victorarias/attn/pull/71","title":"Ship","state":"OPEN","isDraft":false,"merged":false,
	"headRefOid":"head-a","headRefName":"topic","mergeStateStatus":"CLEAN","reviewDecision":"REVIEW_REQUIRED",
	"reactions":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}},
	"latestOpinionatedReviews":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"o"}},
	"reviews":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"h"}},
	"reviewRequests":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}},
	"commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"c"}}}}}]}
}}}}`

func pagedReadiness(head string) map[string]string {
	approval := `{"id":"v1","state":"APPROVED","bodyText":"looks good","submittedAt":"2026-09-20T12:00:00Z","author":{"__typename":"User","id":"u1","login":"victor"}}`
	return map[string]string{
		"PullRequestReadiness": pagedReadinessFirstPage,
		"PullRequestOpinions":  `{"data":{"repository":{"pullRequest":{"headRefOid":"` + head + `","latestOpinionatedReviews":{"nodes":[` + approval + `],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`,
		"PullRequestReviews":   `{"data":{"repository":{"pullRequest":{"headRefOid":"` + head + `","reviews":{"nodes":[` + approval + `],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`,
		"PullRequestChecks": `{"data":{"repository":{"pullRequest":{"headRefOid":"` + head + `","commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[
			{"__typename":"CheckRun","id":"check-1","name":"test","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://ci"}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}]}}}}}`,
		"PullRequestFeedback": `{"data":{"repository":{"pullRequest":{"comments":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}},
			"reviews":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}},"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`,
	}
}

func TestAWatchedPullRequestIsJudgedOnEveryPageOfItsReviewsAndChecks(t *testing.T) {
	servePagedEnterpriseGitHub(t, pagedReadiness("head-a"))
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	s1 := spawnPanes(w, app, w.Path("s1"))[0].session

	watchPullRequestAs(t, cli, s1, protocol.PullRequestWatchModeFormalReview, "victor")
	watched := awaitWatchedPullRequest(app, s1, func(pr protocol.SessionPullRequest) bool { return protocol.Deref(pr.ReadinessState) != "" })
	if protocol.Deref(watched.ReadinessState) != "ready" || protocol.Deref(watched.ReadinessReason) != "selected_reviewer_approved" {
		t.Errorf("the pull request reads %s (%s), want ready on victor's approval from the second page", protocol.Deref(watched.ReadinessState), protocol.Deref(watched.ReadinessReason))
	}
	if protocol.Deref(watched.CIStatus) != "failure" {
		t.Errorf("the pull request's checks read %q, want the failure on the second page", protocol.Deref(watched.CIStatus))
	}
}

func TestAWatchedPullRequestWhoseHeadMovesWhileBeingReadIsNotJudged(t *testing.T) {
	servePagedEnterpriseGitHub(t, pagedReadiness("head-b"))
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	s1 := spawnPanes(w, app, w.Path("s1"))[0].session

	watchPullRequestAs(t, cli, s1, protocol.PullRequestWatchModeFormalReview, "victor")
	watched := awaitWatchedPullRequest(app, s1, func(pr protocol.SessionPullRequest) bool { return protocol.Deref(pr.WatchError) != "" })
	if protocol.Deref(watched.ReadinessState) == "ready" || !strings.Contains(protocol.Deref(watched.WatchError), "head changed") {
		t.Errorf("the pull request = %+v, want it unjudged with a watch error naming the moved head", watched)
	}
	if inbox := inboxContents(readInbox(t, cli, s1, 0).Items); strings.Contains(inbox, "ready") {
		t.Errorf("the agent was told %q about a pull request whose head moved mid-read", inbox)
	}
}

func TestADismissedApprovalOnALaterPageKeepsThePullRequestWaiting(t *testing.T) {
	pages := pagedReadiness("head-a")
	dismissed := `{"id":"v2","state":"DISMISSED","bodyText":"","submittedAt":"2026-09-20T12:01:00Z","author":{"__typename":"User","id":"u1","login":"victor"}}`
	pages["PullRequestReviews"] = `{"data":{"repository":{"pullRequest":{"headRefOid":"head-a","reviews":{"nodes":[` + dismissed + `],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`
	servePagedEnterpriseGitHub(t, pages)
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	s1 := spawnPanes(w, app, w.Path("s1"))[0].session

	watchPullRequestAs(t, cli, s1, protocol.PullRequestWatchModeFormalReview, "victor")
	watched := awaitWatchedPullRequest(app, s1, func(pr protocol.SessionPullRequest) bool { return protocol.Deref(pr.ReadinessState) != "" })
	if protocol.Deref(watched.ReadinessState) != "waiting" || protocol.Deref(watched.ReadinessReason) != "review_dismissed" {
		t.Errorf("the pull request reads %s (%s), want waiting because victor's approval was dismissed", protocol.Deref(watched.ReadinessState), protocol.Deref(watched.ReadinessReason))
	}
}

func TestNewFeedbackOnEveryPageReachesTheWatchingAgent(t *testing.T) {
	pages := pagedReadiness("head-a")
	pages["PullRequestReviews"] = `{"data":{"repository":{"pullRequest":{"headRefOid":"head-a","reviews":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`
	set := servePagedEnterpriseGitHub(t, pages)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		watchPullRequestOn(t, cli, "s1", protocol.PullRequestWatchModeGreen)
		w.advance(protocol.HeatHotInterval)

		set("PullRequestFeedback", `{"data":{"repository":{"pullRequest":{
			"comments":{"nodes":[{"id":"c1","bodyText":"first-page comment","createdAt":"2026-09-20T12:00:00Z","author":{"__typename":"User","login":"a"}}],"pageInfo":{"hasNextPage":true,"endCursor":"c"}},
			"reviews":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"v"}},
			"reviewThreads":{"nodes":[{"id":"t1","isResolved":false,"comments":{"nodes":[{"id":"i1","bodyText":"first inline","createdAt":"2026-09-20T12:01:00Z","path":"x.go","line":4,"author":{"__typename":"User","login":"b"}}],"pageInfo":{"hasNextPage":true,"endCursor":"i"}}}],"pageInfo":{"hasNextPage":true,"endCursor":"t"}}
		}}}}`)
		set("PullRequestComments", `{"data":{"repository":{"pullRequest":{"comments":{"nodes":[{"id":"c2","bodyText":"second-page comment","createdAt":"2026-09-20T12:02:00Z","author":{"__typename":"User","login":"c"}}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`)
		set("PullRequestReviewBodies", `{"data":{"repository":{"pullRequest":{"reviews":{"nodes":[{"id":"v2","state":"COMMENTED","bodyText":"second-page review","submittedAt":"2026-09-20T12:02:30Z","author":{"__typename":"User","login":"d"}}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`)
		set("PullRequestThreads", `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`)
		set("PullRequestThreadComments", `{"data":{"node":{"comments":{"nodes":[{"id":"r1","bodyText":"second-page reply","createdAt":"2026-09-20T12:03:00Z","path":"x.go","line":4,"author":{"__typename":"User","login":"e"}}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}`)
		w.advance(protocol.HeatHotInterval)

		inbox := inboxContents(readInbox(t, cli, "s1", 0).Items)
		for _, want := range []string{"first-page comment", "second-page comment", "first inline", "second-page reply", "second-page review"} {
			if !strings.Contains(inbox, want) {
				t.Errorf("the agent's inbox = %q, want %q", inbox, want)
			}
		}
	})
}
