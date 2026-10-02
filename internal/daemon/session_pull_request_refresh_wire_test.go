package daemon_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/prreadiness"
	"github.com/victorarias/attn/internal/testworld"
)

type refreshedPullRequest struct {
	mu                         sync.Mutex
	title, mergeable           string
	merged                     bool
	reviews                    []string
	reviewsFail, snapshotFails bool
	readinessFails             bool
	readinessGate              <-chan struct{}
	keepAlive                  bool
	codexThumbsUp              bool
	limitedUntil               time.Time
	inboxTitle                 string
	snapshots, readinessReads  int
}

func serveRefreshedPullRequest(t *testing.T) *refreshedPullRequest {
	t.Helper()
	gh := &refreshedPullRequest{title: "Keep session PR status fresh", mergeable: "clean"}
	server := httptest.NewServer(http.HandlerFunc(gh.serve))
	t.Cleanup(server.Close)
	t.Setenv("ATTN_MOCK_GH_URL", server.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	t.Setenv("ATTN_MOCK_GH_HOST", "github.com")
	return gh
}

func (gh *refreshedPullRequest) serve(rw http.ResponseWriter, r *http.Request) {
	gh.mu.Lock()
	defer gh.mu.Unlock()
	rw.Header().Set("Content-Type", "application/json")
	if !gh.keepAlive {
		rw.Header().Set("Connection", "close")
	}
	body, _ := io.ReadAll(r.Body)
	var answer any
	switch {
	case r.URL.Path == "/repos/acme/shop/pulls/71":
		gh.snapshots++
		if !gh.limitedUntil.IsZero() {
			rw.Header().Set("X-RateLimit-Remaining", "0")
			rw.Header().Set("X-RateLimit-Reset", strconv.FormatInt(gh.limitedUntil.Unix(), 10))
			rw.WriteHeader(http.StatusForbidden)
			gh.limitedUntil = time.Time{}
			return
		}
		if gh.snapshotFails {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		state := "open"
		if gh.merged {
			state = "closed"
		}
		answer = map[string]any{
			"number": 71, "html_url": shopPull(71), "title": gh.title, "state": state, "merged": gh.merged,
			"mergeable_state": gh.mergeable, "head": map[string]any{"sha": "sha-1", "ref": "status-refresh"},
		}
	case r.URL.Path == "/repos/acme/shop/pulls/71/reviews":
		if gh.reviewsFail {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		reviews := []map[string]string{}
		for _, state := range gh.reviews {
			reviews = append(reviews, map[string]string{"state": state})
		}
		answer = reviews
	case strings.Contains(string(body), "PullRequestReadiness"):
		gh.readinessReads++
		if gh.readinessGate != nil {
			<-gh.readinessGate
		}
		if gh.readinessFails {
			rw.WriteHeader(http.StatusBadGateway)
			return
		}
		reactions := []any{}
		if gh.codexThumbsUp {
			reactions = append(reactions, map[string]any{"content": "THUMBS_UP",
				"user": map[string]any{"__typename": "Bot", "login": "chatgpt-codex-connector"}})
		}
		answer = map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"number": 71, "url": shopPull(71), "title": gh.title, "state": "OPEN", "merged": false,
			"headRefOid": "head-a", "headRefName": "status-refresh", "mergeStateStatus": "CLEAN",
			"reactions": map[string]any{"nodes": reactions}, "latestOpinionatedReviews": map[string]any{"nodes": []any{}},
			"reviews": map[string]any{"nodes": []any{}}, "reviewRequests": map[string]any{"nodes": []any{}},
			"commits": map[string]any{"nodes": []any{}},
		}}}}
	case strings.Contains(string(body), "PullRequestFeedback"):
		answer = map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"comments": map[string]any{"nodes": []any{}}, "reviews": map[string]any{"nodes": []any{}}, "reviewThreads": map[string]any{"nodes": []any{}},
		}}}}
	case strings.Contains(r.URL.Query().Get("q"), "author:@me"):
		items := []any{}
		if gh.inboxTitle != "" {
			items = append(items, map[string]any{
				"number": 71, "html_url": shopPull(71), "title": gh.inboxTitle,
				"repository_url": "https://api.github.com/repos/acme/shop", "pull_request": map[string]any{"url": shopPull(71)},
			})
		}
		answer = map[string]any{"total_count": len(items), "items": items}
	default:
		answer = map[string]any{"total_count": 0, "items": []any{}}
	}
	_ = json.NewEncoder(rw).Encode(answer)
}

func (gh *refreshedPullRequest) set(change func(gh *refreshedPullRequest)) {
	gh.mu.Lock()
	defer gh.mu.Unlock()
	change(gh)
}

func (gh *refreshedPullRequest) fetchesDuring(w *world, span time.Duration) (snapshots, readinessReads int) {
	w.T.Helper()
	gh.mu.Lock()
	snapshotsBefore, readinessBefore := gh.snapshots, gh.readinessReads
	gh.mu.Unlock()
	w.advance(span)
	gh.mu.Lock()
	defer gh.mu.Unlock()
	return gh.snapshots - snapshotsBefore, gh.readinessReads - readinessBefore
}

func onlyPullRequest(t *testing.T, cli *client.Client, session string) protocol.SessionPullRequest {
	t.Helper()
	prs := queriedSession(t, cli, session).PullRequests
	if len(prs) != 1 {
		t.Fatalf("%s's pull requests = %+v, want exactly one", session, prs)
	}
	return prs[0]
}

func TestAPullRequestsStatusFollowsGitHubAndOnlyRealChangesReachTheApp(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	gh.set(func(gh *refreshedPullRequest) { gh.mergeable, gh.reviews = "blocked", []string{"APPROVED"} })
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "s1")
		recordPullRequest(t, cli, "s1", shopPull(71))
		w.advance(protocol.HeatHotInterval)

		pr := onlyPullRequest(t, cli, "s1")
		if protocol.Deref(pr.Title) != "Keep session PR status fresh" || pr.State != "open" || pr.StatusFetchedAt == nil {
			t.Errorf("the pull request = %+v, want GitHub's title, open, and when it was fetched", pr)
		}
		if protocol.Deref(pr.CIStatus) != "pending" || protocol.Deref(pr.MergeableState) != "blocked" || protocol.Deref(pr.ReviewStatus) != "approved" {
			t.Errorf("the pull request shows ci %q, mergeable %q, review %q; want pending, blocked, approved",
				protocol.Deref(pr.CIStatus), protocol.Deref(pr.MergeableState), protocol.Deref(pr.ReviewStatus))
		}

		shown := len(sessionUpdatesOf(app, "s1"))
		if snapshots, _ := gh.fetchesDuring(w, protocol.HeatHotInterval); snapshots != 1 {
			t.Fatalf("GitHub was asked %d times in one hot interval, want once", snapshots)
		}
		if again := len(sessionUpdatesOf(app, "s1")); again != shown {
			t.Errorf("an unchanged pull request sent the app %d session updates", again-shown)
		}

		gh.set(func(gh *refreshedPullRequest) { gh.mergeable, gh.reviewsFail = "dirty", true })
		w.advance(protocol.HeatHotInterval)
		pr = onlyPullRequest(t, cli, "s1")
		if protocol.Deref(pr.CIStatus) != "failure" || protocol.Deref(pr.ReviewStatus) != "approved" {
			t.Errorf("with CI red and reviews unreadable the pull request shows ci %q, review %q; want failure and the approval it had",
				protocol.Deref(pr.CIStatus), protocol.Deref(pr.ReviewStatus))
		}
		if again := len(sessionUpdatesOf(app, "s1")); again != shown+1 {
			t.Errorf("CI turning red sent the app %d session updates, want one", again-shown)
		}
	})
}

func TestAPullRequestIsAskedAboutLessAsItCoolsAndNotAtAllOnceMerged(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	gh.set(func(gh *refreshedPullRequest) { gh.reviews = []string{"APPROVED"} })
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		recordPullRequest(t, cli, "s1", shopPull(71))

		if hot, _ := gh.fetchesDuring(w, protocol.HeatHotDuration); hot < 5 {
			t.Errorf("GitHub was asked %d times in the first %s, want every %s", hot, protocol.HeatHotDuration, protocol.HeatHotInterval)
		}
		if warm, _ := gh.fetchesDuring(w, protocol.HeatWarmDuration-protocol.HeatHotDuration); warm < 3 || warm > 4 {
			t.Errorf("GitHub was asked %d times while warm, want every %s", warm, protocol.HeatWarmInterval)
		}
		if cold, _ := gh.fetchesDuring(w, 3*protocol.HeatColdInterval); cold != 3 {
			t.Errorf("GitHub was asked %d times over %s cold, want every %s", cold, 3*protocol.HeatColdInterval, protocol.HeatColdInterval)
		}

		gh.set(func(gh *refreshedPullRequest) { gh.merged = true })
		w.advance(protocol.HeatColdInterval)
		pr := onlyPullRequest(t, cli, "s1")
		if pr.State != "merged" || protocol.Deref(pr.CIStatus) != "success" || protocol.Deref(pr.ReviewStatus) != "approved" {
			t.Errorf("the merged pull request = %+v, want merged with the CI and review it merged with", pr)
		}
		if after, _ := gh.fetchesDuring(w, 2*protocol.HeatColdInterval); after != 0 {
			t.Errorf("GitHub was asked %d times about a merged pull request, want none", after)
		}
	})
}

func TestAFailingPullRequestClaimsNoStatusAndIsRetriedAtItsOwnPace(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	gh.set(func(gh *refreshedPullRequest) { gh.snapshotFails = true })
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		recordPullRequest(t, cli, "s1", shopPull(71))
		w.advance(protocol.HeatWarmDuration + protocol.HeatHotInterval)

		if pr := onlyPullRequest(t, cli, "s1"); pr.State != "open" || pr.CIStatus != nil || pr.StatusFetchedAt != nil {
			t.Errorf("a pull request GitHub cannot find = %+v, want it as recorded", pr)
		}
		if cold, _ := gh.fetchesDuring(w, 3*protocol.HeatColdInterval); cold != 3 {
			t.Errorf("a failing cold pull request was retried %d times over %s, want every %s", cold, 3*protocol.HeatColdInterval, protocol.HeatColdInterval)
		}
	})
}

func TestAPullRequestOnAHostAttnHasNoClientForStaysAsRecorded(t *testing.T) {
	serveRefreshedPullRequest(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		recordPullRequest(t, cli, "s1", "https://ghe.example.test/acme/shop/pull/71")
		w.advance(2 * protocol.HeatHotInterval)

		if pr := onlyPullRequest(t, cli, "s1"); pr.State != "open" || pr.Title != nil || pr.CIStatus != nil || pr.StatusFetchedAt != nil {
			t.Errorf("a pull request on a host without a client = %+v, want it as recorded", pr)
		}
	})
}

func TestARateLimitedHostIsLeftAloneUntilItsLimitResets(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		limit := 4 * time.Minute
		gh.set(func(gh *refreshedPullRequest) { gh.limitedUntil = time.Now().Add(limit) })
		recordPullRequest(t, cli, "s1", shopPull(71))
		if first, _ := gh.fetchesDuring(w, protocol.HeatHotInterval); first != 1 {
			t.Fatalf("GitHub was asked %d times before it answered with its limit, want once", first)
		}

		if limited, _ := gh.fetchesDuring(w, limit-protocol.HeatHotInterval-time.Second); limited != 0 {
			t.Errorf("GitHub was asked %d times while its limit stood, want none", limited)
		}
		gh.fetchesDuring(w, protocol.HeatHotInterval)
		if pr := onlyPullRequest(t, cli, "s1"); pr.StatusFetchedAt == nil {
			t.Errorf("once the limit reset the pull request = %+v, want its status fetched", pr)
		}
	})
}

func TestTwoSessionsOnOnePullRequestShareOneFetch(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1", "s2")
		recordPullRequest(t, cli, "s1", shopPull(71))
		recordPullRequest(t, cli, "s2", shopPull(71))

		if snapshots, _ := gh.fetchesDuring(w, protocol.HeatHotInterval); snapshots != 1 {
			t.Errorf("GitHub was asked %d times for one pull request in two sessions, want once", snapshots)
		}
		for _, session := range []string{"s1", "s2"} {
			if pr := onlyPullRequest(t, cli, session); protocol.Deref(pr.CIStatus) != "success" {
				t.Errorf("%s's pull request shows ci %q, want the shared success", session, protocol.Deref(pr.CIStatus))
			}
		}
	})
}

func TestAColdPullRequestTheInboxSeesChangeIsAskedAboutAtTheHotPaceAgain(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		recordPullRequest(t, cli, "s1", shopPull(71))
		w.advance(protocol.HeatWarmDuration + protocol.HeatColdInterval)

		gh.set(func(gh *refreshedPullRequest) { gh.inboxTitle = "Keep session PR status fresh" })
		w.advance(2 * time.Minute)
		gh.set(func(gh *refreshedPullRequest) { gh.inboxTitle = "Keep session PR status fresher" })
		w.advance(2 * time.Minute)
		if reheated, _ := gh.fetchesDuring(w, 2*time.Minute); reheated < 3 {
			t.Errorf("after the inbox saw it change GitHub was asked %d times in 2m, want every %s", reheated, protocol.HeatHotInterval)
		}
	})
}

func TestACodexWatchIsReadyAtItsSettlingDeadlineNotTheNextPoll(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	gh.set(func(gh *refreshedPullRequest) { gh.codexThumbsUp = true })
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		w.advance(protocol.HeatHotInterval / 3)
		watchPullRequestOn(t, cli, "s1", protocol.PullRequestWatchModeCodex)
		w.advance(time.Second)

		if pr := onlyPullRequest(t, cli, "s1"); protocol.Deref(pr.ReadinessState) != prreadiness.StateWaiting || protocol.Deref(pr.ReadinessReason) != "codex_settling" {
			t.Errorf("a pull request Codex just thumbed up = %+v, want it waiting for Codex to settle", pr)
		}
		w.advance(prreadiness.CodexSettle)
		if pr := onlyPullRequest(t, cli, "s1"); protocol.Deref(pr.ReadinessState) != prreadiness.StateReady {
			t.Errorf("at its settling deadline the pull request = %+v, want it ready", pr)
		}
	})
}

func TestAWatchOutageTellsTheAgentOnceAndRecoveryTellsItTheNews(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	gh.set(func(gh *refreshedPullRequest) { gh.readinessFails = true })
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		watchPullRequestOn(t, cli, "s1", protocol.PullRequestWatchModeGreen)
		w.advance(3 * protocol.HeatHotInterval)
		if inbox := inboxContents(readInbox(t, cli, "s1", 0).Items); strings.Count(inbox, "monitoring is delayed") != 1 {
			t.Errorf("after three failed polls the agent's inbox = %q, want one note that monitoring is delayed", inbox)
		}

		gh.set(func(gh *refreshedPullRequest) { gh.readinessFails = false })
		w.advance(protocol.HeatHotInterval)
		items := readInbox(t, cli, "s1", 0).Items
		if len(items) != 1 || !strings.Contains(items[0].Content, "ready") {
			t.Errorf("after recovering the agent's inbox = %q, want one note that the pull request is ready", inboxContents(items))
		}
		if pr := onlyPullRequest(t, cli, "s1"); protocol.Deref(pr.WatchHealth) != "current" || pr.WatchError != nil {
			t.Errorf("the recovered watch = %+v, want it current", pr)
		}

		gh.set(func(gh *refreshedPullRequest) { gh.readinessFails = true })
		w.advance(protocol.HeatHotInterval)
		if inbox := inboxContents(readInbox(t, cli, "s1", 0).Items); !strings.Contains(inbox, "monitoring is delayed") {
			t.Errorf("after a second outage the agent's inbox = %q, want a new note that monitoring is delayed", inbox)
		}
	})
}

func TestAnUnchangedWatchedPullRequestSendsTheAppNothing(t *testing.T) {
	serveRefreshedPullRequest(t)
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "s1")
		watchPullRequestOn(t, cli, "s1", protocol.PullRequestWatchModeGreen)
		w.advance(protocol.HeatHotInterval)

		shown := len(sessionUpdatesOf(app, "s1"))
		w.advance(3 * protocol.HeatHotInterval)
		if again := len(sessionUpdatesOf(app, "s1")); again != shown {
			t.Errorf("three unchanged polls of a watched pull request sent the app %d session updates", again-shown)
		}
	})
}

func watchPullRequestOn(t *testing.T, cli *client.Client, session string, mode protocol.PullRequestWatchMode) {
	t.Helper()
	recordPullRequest(t, cli, session, shopPull(71))
	if err := cli.WatchSessionPullRequest(session, shopPull(71), mode, ""); err != nil {
		t.Fatalf("%s watches the pull request: %v", session, err)
	}
}

// Reset the client connection after a query is written, including an idle reader already blocked in Read.
type resetQueryConn struct {
	net.Conn
	reset   *atomic.Int32
	queries *atomic.Int32
	failed  atomic.Bool
}

func (c *resetQueryConn) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "PullRequestReadiness") {
		c.queries.Add(1)
		if c.reset.Load() != 0 {
			if c.reset.Load() > 0 {
				c.reset.Add(-1)
			}
			c.failed.Store(true)
			c.Conn.Close()
			return len(p), nil
		}
	}
	return c.Conn.Write(p)
}

func (c *resetQueryConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil && c.failed.Load() {
		return n, &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	}
	return n, err
}

func TestAWatchRetriesAReusedConnectionResetBeforeReportingDelay(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		for _, tc := range []struct {
			name              string
			persistent, fresh bool
			attempts          int32
		}{
			{name: "one reset", attempts: 2},
			{name: "persistent resets", persistent: true, attempts: 2},
			{name: "fresh connection reset", fresh: true, attempts: 1},
		} {
			version := "HTTP1"
			if http2 {
				version = "HTTP2"
			}
			t.Run(version+"/"+tc.name, func(t *testing.T) {
				t.Setenv("ATTN_MOCK_GH_URL", "http://127.0.0.1:80")
				t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
				t.Setenv("ATTN_MOCK_GH_HOST", "github.com")
				prepared := prepareWorld(t)
				synctest.Test(t, func(t *testing.T) {
					gh := &refreshedPullRequest{title: "Recover from reset", mergeable: "clean", keepAlive: true}
					listener := newPipeListener()
					protocols := new(http.Protocols)
					if http2 {
						protocols.SetUnencryptedHTTP2(true)
					} else {
						protocols.SetHTTP1(true)
					}
					server := &http.Server{Handler: http.HandlerFunc(gh.serve), Protocols: protocols}
					go server.Serve(listener)
					defer server.Close()
					var resets, queries atomic.Int32

					original := http.DefaultTransport
					transport := original.(*http.Transport).Clone()
					transport.Proxy = nil
					transport.Protocols = protocols
					transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
						conn, err := listener.dial(ctx)
						if err != nil {
							return nil, err
						}
						return &resetQueryConn{Conn: conn, reset: &resets, queries: &queries}, nil
					}
					http.DefaultTransport = transport
					defer func() { transport.CloseIdleConnections(); http.DefaultTransport = original }()
					bubbled := *prepared
					bubbled.T = t
					w := &world{World: &bubbled, bubbled: true}
					w.start()
					defer w.stop()
					cli := w.Client()
					registerSessions(t, w, cli, "s1")
					watchPullRequestOn(t, cli, "s1", protocol.PullRequestWatchModeGreen)
					synctest.Wait()
					if pr := onlyPullRequest(t, cli, "s1"); protocol.Deref(pr.WatchHealth) != "current" {
						t.Fatalf("watch did not warm up: %+v", pr)
					}
					before := queries.Load()
					if tc.fresh {
						transport.CloseIdleConnections()
					}
					resets.Store(1)
					if tc.persistent {
						resets.Store(-1)
					}
					w.advance(protocol.HeatHotInterval)
					pr := onlyPullRequest(t, cli, "s1")
					inbox := inboxContents(readInbox(t, cli, "s1", 0).Items)
					if attempts := queries.Load() - before; attempts != tc.attempts {
						t.Errorf("readiness attempts = %d, want %d", attempts, tc.attempts)
					}
					if tc.persistent || tc.fresh {
						if protocol.Deref(pr.WatchHealth) != "delayed" || !strings.Contains(inbox, "monitoring is delayed") {
							t.Fatalf("reset: watch=%+v inbox=%q, want visible delay", pr, inbox)
						}
					} else if protocol.Deref(pr.WatchHealth) != "current" || strings.Contains(inbox, "monitoring is delayed") {
						t.Fatalf("one reset: watch=%+v inbox=%q, want current without delay", pr, inbox)
					}
				})
			})
		}
	}
}

func TestAMembersPullRequestWatchSurvivesItsCreatingDayAndCanBeStoppedFromTheNext(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli := w.Client()
		first := w.bootBubbleClaude(t, wakeCrew(t, cli, "trellis", "").SessionID)
		first.reply("Ready. <!-- attn:state=idle -->")
		watchPullRequestOn(t, cli, first.id, protocol.PullRequestWatchModeGreen)
		w.advance(time.Second)
		readInbox(t, cli, first.id, 0)
		if _, err := cli.CrewHandoff(first.id, "next day please", false, protocol.CrewDayCloseSleep); err != nil {
			t.Fatal(err)
		}
		if err := cli.Unregister(first.id); err != nil {
			t.Fatal(err)
		}
		gh.set(func(gh *refreshedPullRequest) { gh.readinessFails = true })
		w.advance(protocol.HeatHotInterval)
		nextID := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		if nextID == "" || nextID == first.id {
			t.Fatalf("watch did not wake next day: %q", nextID)
		}
		next := w.bootBubbleClaude(t, nextID)
		next.reply("Ready. <!-- attn:state=idle -->")
		items := readInbox(t, cli, next.id, 0).Items
		if len(items) != 1 || items[0].Address != "member:trellis" || !strings.Contains(inboxContents(items), "monitoring is delayed") {
			t.Fatalf("next day inbox=%+v", items)
		}

		snapshot := []protocol.Session{queriedSession(t, cli, next.id)}
		found := false
		for _, session := range snapshot {
			if session.ID == next.id {
				for _, pr := range session.PullRequests {
					if pr.Number == 71 && protocol.Deref(pr.Watching) && protocol.Deref(pr.WatchHealth) == "delayed" {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatalf("successor cannot inspect inherited watch: %+v", snapshot)
		}
		observer := w.App()
		gh.set(func(gh *refreshedPullRequest) { gh.readinessFails = false })
		w.advance(protocol.HeatHotInterval)
		testworld.AwaitSession(observer, next.id, func(session protocol.Session) bool {
			for _, pr := range session.PullRequests {
				if pr.Number == 71 && protocol.Deref(pr.WatchHealth) == "current" {
					return true
				}
			}
			return false
		})
		if err := cli.UnwatchSessionPullRequest(next.id, shopPull(71)); err != nil {
			t.Fatal(err)
		}
		gh.set(func(gh *refreshedPullRequest) { gh.readinessFails = true })
		w.advance(protocol.HeatHotInterval)
		if items := readInbox(t, cli, next.id, 0).Items; len(items) != 0 {
			t.Fatalf("mail after unwatch=%+v", items)
		}
	})
}

func TestAMemberCanForgetItsWatchFromTheNextDay(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli := w.Client()
		first := w.bootBubbleClaude(t, wakeCrew(t, cli, "trellis", "").SessionID)
		first.reply("Ready. <!-- attn:state=idle -->")
		watchPullRequestOn(t, cli, first.id, protocol.PullRequestWatchModeGreen)
		w.advance(time.Second)
		readInbox(t, cli, first.id, 0)
		if _, err := cli.CrewHandoff(first.id, "next day", false, protocol.CrewDayCloseSleep); err != nil {
			t.Fatal(err)
		}
		if err := cli.Unregister(first.id); err != nil {
			t.Fatal(err)
		}
		next := w.bootBubbleClaude(t, wakeCrew(t, cli, "trellis", "").SessionID)
		next.reply("Ready. <!-- attn:state=idle -->")
		if err := cli.ForgetSessionPullRequest(next.id, shopPull(71)); err != nil {
			t.Fatal(err)
		}
		if err := cli.UnwatchSessionPullRequest(next.id, shopPull(71)); err == nil {
			t.Fatal("forgotten watch remains")
		}
		gh.set(func(gh *refreshedPullRequest) { gh.readinessFails = true })
		w.advance(protocol.HeatHotInterval)
		if items := readInbox(t, cli, next.id, 0).Items; len(items) != 0 {
			t.Fatalf("forgotten watch sent mail=%+v", items)
		}
	})
}

func TestAPullRequestWatchCreatedBeforeTheChiefRoleStaysVisibleAndStoppable(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "watcher")
		watchPullRequestOn(t, cli, "watcher", protocol.PullRequestWatchModeGreen)
		w.advance(time.Second)
		readInbox(t, cli, "watcher", 0)
		if result := setChiefOfStaff(app, "watcher", true); !result.Success {
			t.Fatalf("Chief=%+v", result)
		}
		synctest.Wait()
		session := queriedSession(t, cli, "watcher")
		if len(session.PullRequests) != 1 || !protocol.Deref(session.PullRequests[0].Watching) {
			t.Fatalf("hidden watch=%+v", session.PullRequests)
		}
		watchPullRequestOn(t, cli, "watcher", protocol.PullRequestWatchModeGreen)
		gh.set(func(gh *refreshedPullRequest) { gh.readinessFails = true })
		w.advance(protocol.HeatHotInterval)
		items := readInbox(t, cli, "watcher", 0).Items
		if len(items) != 1 || items[0].Address != "session:watcher" {
			t.Fatalf("rewatch duplicated or rewrote the watch: %+v", items)
		}
		if err := cli.UnwatchSessionPullRequest("watcher", shopPull(71)); err != nil {
			t.Fatal(err)
		}
		session = queriedSession(t, cli, "watcher")
		if len(session.PullRequests) != 1 || protocol.Deref(session.PullRequests[0].Watching) {
			t.Fatalf("watch after unwatch=%+v", session.PullRequests)
		}
		w.advance(protocol.HeatHotInterval)
		if items := readInbox(t, cli, "watcher", 0).Items; len(items) != 0 {
			t.Fatalf("mail after unwatch=%+v", items)
		}
		watchPullRequestOn(t, cli, "watcher", protocol.PullRequestWatchModeGreen)
		if err := cli.ForgetSessionPullRequest("watcher", shopPull(71)); err != nil {
			t.Fatal(err)
		}
		if session := queriedSession(t, cli, "watcher"); len(session.PullRequests) != 0 {
			t.Fatalf("forgotten watch=%+v", session.PullRequests)
		}
	})
}

func TestANextDayWatchModeChangeClearsThePreviousReadiness(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	gate := make(chan struct{})
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli := w.Client()
		first := w.bootBubbleClaude(t, wakeCrew(t, cli, "trellis", "").SessionID)
		first.reply("Ready. <!-- attn:state=idle -->")
		watchPullRequestOn(t, cli, first.id, protocol.PullRequestWatchModeGreen)
		w.advance(time.Second)
		if pr := onlyPullRequest(t, cli, first.id); protocol.Deref(pr.ReadinessState) != "ready" {
			t.Fatalf("initial readiness=%+v", pr)
		}
		readInbox(t, cli, first.id, 0)
		if _, err := cli.CrewHandoff(first.id, "next day", false, protocol.CrewDayCloseSleep); err != nil {
			t.Fatal(err)
		}
		next := w.bootBubbleClaude(t, wakeCrew(t, cli, "trellis", "").SessionID)
		gh.set(func(gh *refreshedPullRequest) { gh.readinessGate = gate })
		defer close(gate)
		if err := cli.WatchSessionPullRequest(next.id, shopPull(71), protocol.PullRequestWatchModeCodex, ""); err != nil {
			t.Fatal(err)
		}
		pr := onlyPullRequest(t, cli, next.id)
		if protocol.Deref(pr.WatchMode) != protocol.PullRequestWatchModeCodex || pr.ReadinessState != nil || pr.WatchHealth != nil {
			t.Fatalf("mode change retained old readiness: %+v", pr)
		}
	})
}
