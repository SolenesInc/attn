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

	"github.com/victorarias/attn/internal/bridge"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/testworld"
)

type accountGitHub struct {
	mu                       sync.Mutex
	merged, checksFail       bool
	comments                 []map[string]any
	searches, pullRequestAsk int
}

func serveAccountGitHub(t *testing.T, s *testworld.Stack) *accountGitHub {
	t.Helper()
	gh := &accountGitHub{}
	server := httptest.NewServer(http.HandlerFunc(gh.serve))
	t.Cleanup(server.Close)
	s.Vars = append(s.Vars, "ATTN_MOCK_GH_URL="+server.URL, "ATTN_MOCK_GH_TOKEN=test-token", "ATTN_MOCK_GH_HOST=github.com")
	return gh
}

func (gh *accountGitHub) serve(rw http.ResponseWriter, r *http.Request) {
	gh.mu.Lock()
	defer gh.mu.Unlock()
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Connection", "close")
	body, _ := io.ReadAll(r.Body)
	var answer any = map[string]any{"total_count": 0, "items": []any{}}
	switch {
	case strings.HasPrefix(r.URL.Path, "/search/"):
		gh.searches++
	case strings.Contains(r.URL.Path, "/pulls/"):
		gh.pullRequestAsk++
	case strings.Contains(string(body), "PullRequestReadiness"):
		gh.pullRequestAsk++
		state := "OPEN"
		if gh.merged {
			state = "MERGED"
		}
		commits := []any{}
		if gh.checksFail {
			commits = append(commits, map[string]any{"commit": map[string]any{"statusCheckRollup": map[string]any{"contexts": map[string]any{"nodes": []any{
				map[string]any{"__typename": "CheckRun", "id": "check-1", "name": "test", "status": "COMPLETED", "conclusion": "FAILURE"},
			}}}}})
		}
		answer = map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"number": 71, "url": shopPull(71), "title": "Stop polling on outposts", "state": state, "merged": gh.merged,
			"author":     map[string]any{"__typename": "User", "login": "victor"},
			"headRefOid": "head-a", "headRefName": "outpost-no-poll", "mergeStateStatus": "CLEAN",
			"reactions": map[string]any{"nodes": []any{}}, "latestOpinionatedReviews": map[string]any{"nodes": []any{}},
			"reviews": map[string]any{"nodes": []any{}}, "reviewRequests": map[string]any{"nodes": []any{}},
			"commits": map[string]any{"nodes": commits},
		}}}}
	case strings.Contains(string(body), "PullRequestFeedback"):
		gh.pullRequestAsk++
		answer = map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"comments": map[string]any{"nodes": append([]map[string]any{}, gh.comments...)},
			"reviews":  map[string]any{"nodes": []any{}}, "reviewThreads": map[string]any{"nodes": []any{}},
		}}}}
	}
	_ = json.NewEncoder(rw).Encode(answer)
}

func (gh *accountGitHub) set(change func(gh *accountGitHub)) {
	gh.mu.Lock()
	defer gh.mu.Unlock()
	change(gh)
}

func (gh *accountGitHub) asked() (searches, pullRequestAsks int) {
	gh.mu.Lock()
	defer gh.mu.Unlock()
	return gh.searches, gh.pullRequestAsk
}

const prPollTimer stepclock.Name = "daemon-pr-poll"

const bridgeSafeStep = bridge.StallAfter - 500*time.Millisecond

func TestOnlyTheHomePollsGitHubPullRequestsWhileAnOutpostAgentsWatchStillReachesIt(t *testing.T) {
	home := testworld.NewStack(t)
	homeGitHub := serveAccountGitHub(t, home)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpostGitHub := serveAccountGitHub(t, outpost)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	app := pair.App
	testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, row := range m.Outposts {
			if row.ID == pair.Host && row.Notebook != nil && row.Notebook.State == protocol.OutpostNotebookStateInSync {
				return true
			}
		}
		return false
	})

	session := home.Spawn(app, fakeagent.Claude, outpost.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	terminal := protocol.TerminalID(app.Terminal(session))
	outpost.LaunchedCarrying(string(terminal))
	agent := outpost.Client()
	if err := agent.RecordPullRequestCreatedFromTerminal(terminal, shopPull(71)); err != nil {
		t.Fatalf("the outpost agent records its pull request: %v", err)
	}
	if err := agent.WatchSessionPullRequest(protocol.SessionID(session), "", shopPull(71), protocol.PullRequestWatchModeGreen, ""); err != nil {
		t.Fatalf("the outpost agent watches its pull request: %v", err)
	}

	outpost.AwaitTimer(prPollTimer)
	home.AwaitTimer(prPollTimer)
	pollTicks := func(s *testworld.Stack) {
		t.Helper()
		for range 3 {
			s.FirePending(prPollTimer)
			s.AwaitTimer(prPollTimer)
		}
	}
	outpostSearches, outpostPullAsks := outpostGitHub.asked()
	homeSearches, _ := homeGitHub.asked()
	pollTicks(outpost)
	pollTicks(home)
	if searches, pullAsks := outpostGitHub.asked(); searches != outpostSearches || pullAsks != outpostPullAsks {
		t.Errorf("over three enrolled poll ticks the outpost asked GitHub %d searches and %d pull request reads, want none", searches-outpostSearches, pullAsks-outpostPullAsks)
	}
	if searches, _ := homeGitHub.asked(); searches == homeSearches {
		t.Errorf("over three poll ticks the home made no pull request search, want it to keep polling")
	}

	pastHotInterval := func() {
		t.Helper()
		for stepped := time.Duration(0); stepped <= protocol.HeatHotInterval; stepped += bridgeSafeStep {
			home.Advance(bridgeSafeStep)
			home.AwaitTimer(bridge.StallTimer)
		}
		home.AwaitJobsSettled()
	}
	var heard []string
	hears := func(news string) {
		t.Helper()
		inbox := inboxContents(readInbox(t, agent, session, 0).Items)
		heard = append(heard, inbox)
		if !strings.Contains(inbox, news) {
			t.Errorf("the outpost agent's inbox = %q, want %q", inbox, news)
		}
	}
	home.AwaitJobsSettled()
	hears("ready")
	homeGitHub.set(func(gh *accountGitHub) { gh.checksFail = true })
	pastHotInterval()
	hears("checks failed")
	homeGitHub.set(func(gh *accountGitHub) {
		gh.comments = append(gh.comments, map[string]any{
			"id": "comment-alice", "bodyText": "Please rename the flag", "createdAt": "2026-11-16T10:00:00Z",
			"author": map[string]any{"__typename": "User", "login": "alice"},
		})
	})
	pastHotInterval()
	hears("alice: Please rename the flag")
	homeGitHub.set(func(gh *accountGitHub) { gh.merged = true })
	pastHotInterval()
	hears("merged")
	all := strings.Join(heard, "\n") + inboxContents(readInbox(t, agent, session, 0).Items)
	for _, news := range []string{"ready", "checks failed", "alice: Please rename the flag", "merged"} {
		if got := strings.Count(all, news); got != 1 {
			t.Errorf("the outpost agent heard %q %d times, want once; everything it heard = %q", news, got, all)
		}
	}
}
