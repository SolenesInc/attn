package daemon_test

import (
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/github/mockserver"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const launchFetchRepo = "acme/shop"

var prDetailPath = regexp.MustCompile(`^/repos/[^/]+/[^/]+/pulls/\d+$`)

func githubWithPRs(t *testing.T, count int) *mockserver.Server {
	t.Helper()
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	gh.Config.SetKeepAlivesEnabled(false)
	addPRsAtHead(gh, count, "abc123")
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	return gh
}

func addPRsAtHead(gh *mockserver.Server, count int, head string) {
	gh.Reset()
	for number := 1; number <= count; number++ {
		gh.AddPR(mockserver.MockPR{Repo: launchFetchRepo, Number: number, Title: fmt.Sprintf("Change %d", number), Role: "reviewer", MergeableState: "clean", HeadSHA: head})
	}
}

func prDetailFetches(gh *mockserver.Server) int {
	fetches := 0
	for _, request := range gh.Requests() {
		if request.Method == "GET" && prDetailPath.MatchString(request.Path) {
			fetches++
		}
	}
	return fetches
}

func coreRateLimitEvents(peer *testworld.Peer) int {
	events := 0
	for _, event := range peer.Received() {
		if event.Event == protocol.EventRateLimited && protocol.Deref(event.RateLimitResource) == "core" {
			events++
		}
	}
	return events
}

func anyRateLimitEvents(peer *testworld.Peer) int {
	events := 0
	for _, event := range peer.Received() {
		if event.Event == protocol.EventRateLimited {
			events++
		}
	}
	return events
}

func TestAClientConnectingJustAfterALaunchFetchReusesIt(t *testing.T) {
	const prs = 13
	gh := githubWithPRs(t, prs)
	inBubble(t, func(t *testing.T, w *world) {
		w.App()
		w.finishStartupWork()
		w.restart()

		before := prDetailFetches(gh)
		app := w.ConnectApp()
		w.finishStartupWork()
		if fetched := prDetailFetches(gh) - before; fetched != prs {
			t.Fatalf("opening the app fetched details %d times, want once for each of %d pull requests", fetched, prs)
		}

		before = prDetailFetches(gh)
		observers := []*testworld.Peer{w.ConnectApp(), w.ConnectApp()}
		w.finishStartupWork()
		if fetched := prDetailFetches(gh) - before; fetched != 0 {
			t.Errorf("clients connecting right after the launch fetch fetched details %d times, want none", fetched)
		}
		for _, peer := range append(observers, app) {
			if events := anyRateLimitEvents(peer); events != 0 {
				t.Errorf("a client was told GitHub rate limited attn %d times, want never: GitHub did not", events)
			}
		}

		w.advance(protocol.HeatHotInterval + time.Second)
		before = prDetailFetches(gh)
		w.ConnectApp()
		w.finishStartupWork()
		if fetched := prDetailFetches(gh) - before; fetched != prs {
			t.Errorf("opening the app once details were no longer fresh fetched them %d times, want %d", fetched, prs)
		}
	})
}

func TestALaunchFetchLargerThanTheRequestBudgetPacesItselfToTheEnd(t *testing.T) {
	const prs = 40
	gh := githubWithPRs(t, prs)
	inBubble(t, func(t *testing.T, w *world) {
		w.App()
		w.advance(3 * time.Minute)
		w.restart()
		addPRsAtHead(gh, prs, "def456")

		before := prDetailFetches(gh)
		app := w.ConnectApp()
		w.advance(5 * time.Second)
		midway := prDetailFetches(gh) - before
		if midway >= prs {
			t.Fatalf("the launch fetch finished %d fetches in 5s, want it still pacing itself to reach the second client", midway)
		}
		second := w.ConnectApp()
		w.advance(45 * time.Second)

		if fetched := prDetailFetches(gh) - before; fetched != prs {
			t.Errorf("two clients, the second during the launch fetch, fetched details %d times, want %d", fetched, prs)
		}
		for _, pr := range queryPRs(t, w.Client(), "") {
			if head := protocol.Deref(pr.HeadSHA); head != "def456" {
				t.Errorf("after opening the app pull request %d shows head %q, want GitHub's current def456", pr.Number, head)
			}
		}
		for _, peer := range []*testworld.Peer{app, second} {
			if events := anyRateLimitEvents(peer); events != 0 {
				t.Errorf("a client was told GitHub rate limited attn %d times, want never: only attn paced itself", events)
			}
		}
	})
}

func TestARealGitHubLimitReachesEveryClientThatConnects(t *testing.T) {
	gh := githubWithPRs(t, 3)
	inBubble(t, func(t *testing.T, w *world) {
		w.App()
		w.finishStartupWork()
		w.restart()
		gh.RateLimitUntil(time.Now().Add(30 * time.Minute))

		first := w.ConnectApp()
		w.finishStartupWork()
		if coreRateLimitEvents(first) == 0 {
			t.Errorf("the app was not told GitHub's limit stands")
		}
		second := w.ConnectApp()
		w.finishStartupWork()
		if coreRateLimitEvents(second) == 0 {
			t.Errorf("a client connecting while GitHub's limit stands was not told")
		}
	})
}

func TestStoppingWhileAttnPacesGitHubRequestsNeitherWaitsNorErasesReviews(t *testing.T) {
	const prs = 13
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	gh.Config.SetKeepAlivesEnabled(false)
	for number := 1; number <= prs; number++ {
		gh.AddPR(mockserver.MockPR{Repo: launchFetchRepo, Number: number, Title: fmt.Sprintf("Change %d", number), Role: "reviewer", MergeableState: "clean", HeadSHA: "abc123", ReviewState: "APPROVED"})
	}
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	inBubble(t, func(t *testing.T, w *world) {
		w.App()
		w.finishStartupWork()
		w.advance(3 * time.Minute)
		w.restart()
		app := w.ConnectApp()
		w.finishStartupWork()
		listed := queryPRs(t, w.Client(), "")
		if len(listed) != prs {
			t.Fatalf("GitHub returned %d pull requests, want %d", len(listed), prs)
		}
		for _, pr := range listed[:3] {
			app.Send(protocol.PRVisitedMessage{Cmd: protocol.CmdPRVisited, ID: pr.ID})
		}
		w.finishStartupWork()

		sentBeforeStop := len(gh.Requests())
		stopping := time.Now()
		finish := w.beginStop(t)
		finish()
		if drained := time.Since(stopping); drained > 5*time.Second {
			t.Errorf("the daemon drained in %s, past the five seconds the CLI gives it before killing it", drained)
		}
		if sent := len(gh.Requests()) - sentBeforeStop; sent != 0 {
			t.Errorf("stopping sent %d more GitHub requests, want none once attn had run out of budget", sent)
		}

		gh.RateLimitUntil(time.Now().Add(30 * time.Minute))
		w.start()
		w.finishStartupWork()
		for _, pr := range queryPRs(t, w.Client(), "") {
			if review := protocol.Deref(pr.ReviewStatus); review != "approved" {
				t.Errorf("after the stop pull request %d shows review %q, want the approved it had", pr.Number, review)
			}
		}
	})
}
