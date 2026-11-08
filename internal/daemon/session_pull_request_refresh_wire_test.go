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
	"github.com/victorarias/attn/internal/fakeagent"
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
	codexThumbsUp, checkFails  bool
	author, viewer             string
	comments, reviewBodies     []map[string]any
	threads                    []map[string]any
	limitedUntil               time.Time
	inboxTitle                 string
	snapshots, readinessReads  int
}

const dashboardPollInterval = 90 * time.Second

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
	pullPath, isPull := strings.CutPrefix(r.URL.Path, "/repos/acme/shop/pulls/")
	number, reviewsPath := strings.CutSuffix(pullPath, "/reviews")
	switch {
	case isPull && !reviewsPath:
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
		pull, _ := strconv.Atoi(number)
		answer = map[string]any{
			"number": pull, "html_url": shopPull(pull), "title": gh.title, "state": state, "merged": gh.merged,
			"mergeable_state": gh.mergeable, "head": map[string]any{"sha": "sha-1", "ref": "status-refresh"},
		}
	case isPull:
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
		commits := []any{}
		if gh.checkFails {
			commits = append(commits, map[string]any{"commit": map[string]any{"statusCheckRollup": map[string]any{"contexts": map[string]any{"nodes": []any{
				map[string]any{"__typename": "CheckRun", "id": "check-1", "name": "test", "status": "COMPLETED", "conclusion": "FAILURE"},
			}}}}})
		}
		answer = map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"number": 71, "url": shopPull(71), "title": gh.title, "state": "OPEN", "merged": false,
			"viewerDidAuthor": gh.viewer != "" && gh.viewer == gh.author,
			"headRefOid":      "head-a", "headRefName": "status-refresh", "mergeStateStatus": "CLEAN",
			"reactions": map[string]any{"nodes": reactions}, "latestOpinionatedReviews": map[string]any{"nodes": []any{}},
			"reviews": map[string]any{"nodes": []any{}}, "reviewRequests": map[string]any{"nodes": []any{}},
			"commits": map[string]any{"nodes": commits},
		}}}}
	case strings.Contains(string(body), "PullRequestFeedback"):
		answer = map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"comments":      map[string]any{"nodes": append([]map[string]any{}, gh.comments...)},
			"reviews":       map[string]any{"nodes": append([]map[string]any{}, gh.reviewBodies...)},
			"reviewThreads": map[string]any{"nodes": append([]map[string]any{}, gh.threads...)},
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

func TestAnUnwatchedPullRequestPausesWhileItsAgentIsRecoverableAndResumesWithIt(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "Check the pull request.")
		testworld.AwaitSession(app, agent.id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		recordPullRequest(t, cli, agent.id, shopPull(71))
		w.advance(protocol.HeatHotInterval)
		w.stop()
		agent.term.Exit(0)
		w.startHoldingRevives()
		app, cli = w.App(), w.Client()
		if session := queriedSession(t, cli, agent.id); session.State != protocol.SessionStateRecoverable {
			t.Fatalf("restarted session = %+v, want recoverable", session)
		}
		if fetched, _ := gh.fetchesDuring(w, protocol.HeatHotInterval); fetched != 0 {
			t.Fatalf("GitHub was asked %d times while the agent was recoverable, want none", fetched)
		}
		gh.set(func(gh *refreshedPullRequest) { gh.title = "Updated while the agent was away" })
		w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(msg *protocol.SpawnSessionMessage) {
			msg.ID = protocol.SessionID(agent.id)
			msg.ResumeSessionID = protocol.Ptr(agent.id)
		})
		w.bootBubbleClaude(t, agent.id)
		w.advance(protocol.HeatHotInterval)
		if pr := onlyPullRequest(t, cli, agent.id); protocol.Deref(pr.Title) != "Updated while the agent was away" {
			t.Fatalf("reloaded session's pull request = %+v, want its updated title", pr)
		}
	})
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

func TestUnwatchedPullRequestPollingPausesUntilItsSessionIsRevived(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "Check the change")
		agent.reply("Checked. <!-- attn:state=idle -->")
		recordPullRequest(t, w.Client(), agent.id, shopPull(71))
		if fetched, _ := gh.fetchesDuring(w, protocol.HeatHotInterval); fetched != 1 {
			t.Fatalf("live session fetched %d times, want once", fetched)
		}
		w.stop()
		agent.term.Exit(1)
		w.startHoldingRevives()
		app = w.App()
		if session := queriedSession(t, w.Client(), agent.id); session.State != protocol.SessionStateRecoverable {
			t.Fatalf("session after restart is %s, want recoverable", session.State)
		}
		if fetched, _ := gh.fetchesDuring(w, protocol.HeatHotInterval); fetched != 0 {
			t.Fatalf("recoverable session fetched %d times, want none", fetched)
		}
		reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(agent.id), Cols: 80, Rows: 24}, protocol.EventReloadSessionResult,
			func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == agent.id })
		if !reloaded.Success {
			t.Fatalf("reload: %+v", reloaded)
		}
		w.bootBubbleClaude(t, agent.id)
		if fetched, _ := gh.fetchesDuring(w, protocol.HeatHotInterval); fetched != 1 {
			t.Fatalf("revived session fetched %d times, want once", fetched)
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
		w.advance(dashboardPollInterval)
		if reheated, _ := gh.fetchesDuring(w, protocol.HeatHotDuration-dashboardPollInterval); reheated < 3 {
			t.Errorf("after the inbox saw it change GitHub was asked %d times in %s, want every %s", reheated, protocol.HeatHotDuration-dashboardPollInterval, protocol.HeatHotInterval)
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

func TestStoppingWhileAWatchWaitsForGitHubBudgetIsNotAnOutage(t *testing.T) {
	serveRefreshedPullRequest(t)
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		member := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		member.reply("Ready. <!-- attn:state=idle -->")
		w.finishStartupWork()
		for range 35 {
			app.Send(protocol.FetchPRDetailsMessage{Cmd: protocol.CmdFetchPRDetails, ID: protocol.FormatPRID("github.com", "acme/shop", 71)})
		}
		w.advance(0)
		watchPullRequestOn(t, cli, member.id, protocol.PullRequestWatchModeGreen)
		w.advance(0)

		finish := w.beginStop(t)
		finish()
		w.start()
		cli = w.Client()
		if inbox := inboxContents(readInbox(t, cli, member.id, 0).Items); strings.Contains(inbox, "monitoring is delayed") {
			t.Errorf("after a stop the member's inbox = %q, want no note: GitHub had no outage", inbox)
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

		first := onlyPullRequest(t, cli, "s1")
		shown := len(sessionUpdatesOf(app, "s1"))
		w.advance(3 * protocol.HeatHotInterval)
		latest := onlyPullRequest(t, cli, "s1")
		if protocol.Deref(latest.WatchHealth) != "current" || protocol.Deref(latest.StatusFetchedAt) == protocol.Deref(first.StatusFetchedAt) {
			t.Fatalf("unchanged watch checks lost liveness = %+v", latest)
		}
		if again := len(sessionUpdatesOf(app, "s1")); again != shown {
			t.Errorf("three unchanged polls of a watched pull request sent the app %d session updates", again-shown)
		}
	})
}

func TestARepeatedWatchOutageTellsTheAppOnce(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	gh.set(func(gh *refreshedPullRequest) { gh.readinessFails = true })
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		session := w.bubbleClaude(t, app, "shop").id
		watchPullRequestOn(t, cli, session, protocol.PullRequestWatchModeGreen)
		w.advance(protocol.HeatHotInterval)
		if pr := onlyPullRequest(t, cli, session); protocol.Deref(pr.WatchHealth) != "delayed" {
			t.Fatalf("after a failed poll the watch = %+v, want it delayed", pr)
		}

		facts := producedBy(busStatus(t, app), "session.pull_request.changed")
		w.advance(3 * protocol.HeatHotInterval)
		if again := producedBy(busStatus(t, app), "session.pull_request.changed"); again != facts {
			t.Errorf("three more failed polls of the same outage published %d session.pull_request.changed facts, want none", again-facts)
		}
	})
}

func TestTwoPullRequestsChangingInOnePassTellTheAppOnce(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		session := w.bubbleClaude(t, app, "shop").id
		recordPullRequest(t, cli, session, shopPull(71))
		recordPullRequest(t, cli, session, shopPull(72))
		w.advance(protocol.HeatHotInterval)

		facts := producedBy(busStatus(t, app), "session.pull_request.changed")
		gh.set(func(gh *refreshedPullRequest) { gh.mergeable = "dirty" })
		w.advance(protocol.HeatHotInterval)
		for _, pr := range queriedSession(t, cli, session).PullRequests {
			if protocol.Deref(pr.CIStatus) != "failure" {
				t.Fatalf("pull request #%d shows ci %q, want failure", pr.Number, protocol.Deref(pr.CIStatus))
			}
		}
		if again := producedBy(busStatus(t, app), "session.pull_request.changed"); again != facts+1 {
			t.Errorf("two pull requests turning red in one pass published %d session.pull_request.changed facts, want one", again-facts)
		}
	})
}

func watchPullRequestOn(t *testing.T, cli *client.Client, session string, mode protocol.PullRequestWatchMode) {
	t.Helper()
	recordPullRequest(t, cli, session, shopPull(71))
	if err := cli.WatchSessionPullRequest(protocol.SessionID(session), "", shopPull(71), mode, ""); err != nil {
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
		first := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		first.reply("Ready. <!-- attn:state=idle -->")
		watchPullRequestOn(t, cli, first.id, protocol.PullRequestWatchModeGreen)
		w.advance(time.Second)
		readInbox(t, cli, first.id, 0)
		if _, err := cli.CrewHandoff(protocol.SessionID(first.id), "next day please", false); err != nil {
			t.Fatal(err)
		}
		if err := cli.Unregister(protocol.SessionID(first.id)); err != nil {
			t.Fatal(err)
		}
		gh.set(func(gh *refreshedPullRequest) { gh.readinessFails = true })
		w.advance(protocol.HeatHotInterval)
		nextID := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		if nextID == "" || string(nextID) == first.id {
			t.Fatalf("watch did not wake next day: %q", nextID)
		}
		next := w.bootBubbleClaude(t, string(nextID))
		next.reply("Ready. <!-- attn:state=idle -->")
		items := readInbox(t, cli, next.id, 0).Items
		if len(items) != 1 || items[0].Address != "member:trellis" || !strings.Contains(inboxContents(items), "monitoring is delayed") {
			t.Fatalf("next day inbox=%+v", items)
		}

		snapshot := []protocol.Session{queriedSession(t, cli, next.id)}
		found := false
		for _, session := range snapshot {
			if string(session.ID) == next.id {
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
		if err := cli.UnwatchSessionPullRequest(protocol.SessionID(next.id), "", shopPull(71)); err != nil {
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
		first := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		first.reply("Ready. <!-- attn:state=idle -->")
		watchPullRequestOn(t, cli, first.id, protocol.PullRequestWatchModeGreen)
		w.advance(time.Second)
		readInbox(t, cli, first.id, 0)
		if _, err := cli.CrewHandoff(protocol.SessionID(first.id), "next day", false); err != nil {
			t.Fatal(err)
		}
		if err := cli.Unregister(protocol.SessionID(first.id)); err != nil {
			t.Fatal(err)
		}
		next := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		next.reply("Ready. <!-- attn:state=idle -->")
		if err := cli.ForgetSessionPullRequest(protocol.SessionID(next.id), shopPull(71)); err != nil {
			t.Fatal(err)
		}
		if err := cli.UnwatchSessionPullRequest(protocol.SessionID(next.id), "", shopPull(71)); err == nil {
			t.Fatal("forgotten watch remains")
		}
		gh.set(func(gh *refreshedPullRequest) { gh.readinessFails = true })
		w.advance(protocol.HeatHotInterval)
		if items := readInbox(t, cli, next.id, 0).Items; len(items) != 0 {
			t.Fatalf("forgotten watch sent mail=%+v", items)
		}
	})
}

func TestRewatchingAPullRequestKeepsOneWatchThatUnwatchAndForgetStop(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "watcher")
		watchPullRequestOn(t, cli, "watcher", protocol.PullRequestWatchModeGreen)
		w.advance(time.Second)
		readInbox(t, cli, "watcher", 0)
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
		if err := cli.UnwatchSessionPullRequest("watcher", "", shopPull(71)); err != nil {
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
		first := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		first.reply("Ready. <!-- attn:state=idle -->")
		watchPullRequestOn(t, cli, first.id, protocol.PullRequestWatchModeGreen)
		w.advance(time.Second)
		if pr := onlyPullRequest(t, cli, first.id); protocol.Deref(pr.ReadinessState) != "ready" {
			t.Fatalf("initial readiness=%+v", pr)
		}
		readInbox(t, cli, first.id, 0)
		if _, err := cli.CrewHandoff(protocol.SessionID(first.id), "next day", false); err != nil {
			t.Fatal(err)
		}
		next := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		gh.set(func(gh *refreshedPullRequest) { gh.readinessGate = gate })
		defer close(gate)
		if err := cli.WatchSessionPullRequest(protocol.SessionID(next.id), "", shopPull(71), protocol.PullRequestWatchModeCodex, ""); err != nil {
			t.Fatal(err)
		}
		pr := onlyPullRequest(t, cli, next.id)
		if protocol.Deref(pr.WatchMode) != protocol.PullRequestWatchModeCodex || pr.ReadinessState != nil || pr.WatchHealth != nil {
			t.Fatalf("mode change retained old readiness: %+v", pr)
		}
	})
}

func (gh *refreshedPullRequest) comment(id, login, typeName, body string) {
	gh.set(func(gh *refreshedPullRequest) {
		gh.comments = append(gh.comments, map[string]any{
			"id": id, "bodyText": body, "createdAt": "2026-11-04T10:00:00Z",
			"author": map[string]any{"__typename": typeName, "login": login}, "viewerDidAuthor": login == gh.viewer,
		})
	})
}

func (gh *refreshedPullRequest) reviewBody(id, login, body string) {
	gh.set(func(gh *refreshedPullRequest) {
		gh.reviewBodies = append(gh.reviewBodies, map[string]any{
			"id": id, "state": "COMMENTED", "bodyText": body, "submittedAt": "2026-11-04T10:00:00Z",
			"author": map[string]any{"__typename": "User", "login": login}, "viewerDidAuthor": login == gh.viewer,
		})
	})
}

func (gh *refreshedPullRequest) thread(id, login, body string) {
	gh.set(func(gh *refreshedPullRequest) {
		gh.threads = append(gh.threads, map[string]any{"id": id, "isResolved": false, "comments": map[string]any{"nodes": []any{
			reviewThreadComment(id+"-comment", login, body, gh.viewer),
		}}})
	})
}

func reviewThreadComment(id, login, body, viewer string) map[string]any {
	return map[string]any{"id": id, "bodyText": body, "createdAt": "2026-11-04T10:00:00Z", "path": "loop.go", "line": 12,
		"author": map[string]any{"__typename": "User", "login": login}, "viewerDidAuthor": login == viewer}
}

func (gh *refreshedPullRequest) reply(threadID, id, login, body string) {
	gh.set(func(gh *refreshedPullRequest) {
		for _, thread := range gh.threads {
			if thread["id"] == threadID {
				comments := thread["comments"].(map[string]any)
				comments["nodes"] = append(comments["nodes"].([]any), reviewThreadComment(id, login, body, gh.viewer))
			}
		}
	})
}

func (gh *refreshedPullRequest) resolveThread(id string, resolved bool) {
	gh.set(func(gh *refreshedPullRequest) {
		for _, thread := range gh.threads {
			if thread["id"] == id {
				thread["isResolved"] = resolved
			}
		}
	})
}

func TestAPullRequestAuthorsOwnCommentsRingItsWatchOnlyWhenTheProfileAsks(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	gh.set(func(gh *refreshedPullRequest) { gh.author, gh.viewer = "victor", "victor" })
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		if got := app.Initial.Settings["pull_requests.own_comments"]; got != "quiet" {
			t.Fatalf("the app starts with pull_requests.own_comments = %v, want quiet", got)
		}
		home, work := createProfile(app, "Home"), createProfile(app, "Work")
		homeApp, workApp := w.AppOn(home.ID), w.AppOn(work.ID)
		for session, profile := range map[string]string{"s1": home.ID, "s2": work.ID} {
			if err := w.InjectSession(session, session, w.Path(session), protocol.SessionAgentClaude, profile); err != nil {
				t.Fatalf("register %s: %v", session, err)
			}
		}
		watchPullRequestOn(t, cli, "s1", protocol.PullRequestWatchModeGreen)
		watchPullRequestOn(t, cli, "s2", protocol.PullRequestWatchModeGreen)
		w.advance(protocol.HeatHotInterval)
		for _, session := range []string{"s1", "s2"} {
			if inbox := inboxContents(readInbox(t, cli, session, 0).Items); !strings.Contains(inbox, "ready") {
				t.Fatalf("%s's inbox = %q, want a note that the pull request is ready", session, inbox)
			}
		}

		gh.comment("comment-author", "victor", "User", "I pushed the fix")
		gh.comment("comment-reviewer", "alice", "User", "Please rename this")
		gh.comment("comment-codex", "chatgpt-codex-connector", "Bot", "Possible nil dereference")
		gh.reviewBody("review-author", "victor", "Self-review done")
		gh.thread("thread-author", "victor", "Revisit this loop")
		gh.set(func(gh *refreshedPullRequest) { gh.checkFails = true })
		w.advance(protocol.HeatHotInterval)
		gh.resolveThread("thread-author", true)
		w.advance(protocol.HeatHotInterval)
		gh.resolveThread("thread-author", false)
		w.advance(protocol.HeatHotInterval)
		for _, session := range []string{"s1", "s2"} {
			inbox := inboxContents(readInbox(t, cli, session, 0).Items)
			if strings.Contains(inbox, "I pushed the fix") || strings.Contains(inbox, "Self-review done") || strings.Contains(inbox, "Revisit this loop") ||
				!strings.Contains(inbox, "alice: Please rename this") || !strings.Contains(inbox, "chatgpt-codex-connector[bot]: Possible nil dereference") ||
				!strings.Contains(inbox, "checks failed") {
				t.Errorf("under quiet %s's inbox = %q, want alice's and Codex's comments and the failed checks, without the author's comment, review or reopened thread", session, inbox)
			}
		}

		gh.resolveThread("thread-author", true)
		w.advance(protocol.HeatHotInterval)
		if _, err := cli.SetSetting(client.SettingsCaller{SessionID: "s2"}, "pull_requests.own_comments", "ring"); err != nil {
			t.Fatalf("set pull_requests.own_comments to ring for s2's profile: %v", err)
		}
		testworld.Await(workApp, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
			return m.Settings["pull_requests.own_comments"] == "ring"
		})
		testworld.Await(homeApp, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
			return protocol.Deref(m.ChangedKey) == "pull_requests.own_comments" && m.Settings["pull_requests.own_comments"] == "quiet"
		})
		gh.comment("comment-author-2", "victor", "User", "Address the review next")
		gh.resolveThread("thread-author", false)
		gh.set(func(gh *refreshedPullRequest) { gh.checkFails = false })
		w.advance(protocol.HeatHotInterval)
		ringing := inboxContents(readInbox(t, cli, "s2", 0).Items)
		if !strings.Contains(ringing, "victor: Address the review next") || strings.Count(ringing, "review thread reopened") != 1 ||
			strings.Contains(ringing, "I pushed the fix") || strings.Contains(ringing, "Self-review done") || !strings.Contains(ringing, "ready") {
			t.Errorf("under ring s2's inbox = %q, want the author's new comment, one reopened thread and the ready note, without replaying the author's earlier comment or review", ringing)
		}
		if quiet := inboxContents(readInbox(t, cli, "s1", 0).Items); strings.Contains(quiet, "Address the review next") || strings.Contains(quiet, "review thread reopened") {
			t.Errorf("s1's profile stays quiet, but its inbox = %q", quiet)
		}
	})
}

func TestAReviewersWatchOnAnotherAuthorsPullRequestHearsThatAuthorAndStaysQuietOnItsOwnComments(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	gh.set(func(gh *refreshedPullRequest) { gh.author, gh.viewer = "alice", "bob" })
	gh.thread("thread-bob", "bob", "This loop never ends")
	gh.thread("thread-alice", "alice", "Is this the right name?")
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "reviewer")
		watchPullRequestOn(t, cli, "reviewer", protocol.PullRequestWatchModeGreen)
		w.advance(protocol.HeatHotInterval)

		gh.reply("thread-bob", "reply-alice", "alice", "Fixed in the next push")
		gh.reply("thread-bob", "reply-bob", "bob", "Thanks, checking now")
		gh.comment("comment-alice", "alice", "User", "Ready for another look")
		gh.comment("comment-bob", "bob", "User", "Looking again")
		gh.reviewBody("review-alice", "alice", "Self-review after your finding")
		gh.reviewBody("review-bob", "bob", "One finding left")
		gh.reply("thread-alice", "reply-carol", "carol", "I prefer the old name")
		w.advance(protocol.HeatHotInterval)
		quiet := inboxContents(readInbox(t, cli, "reviewer", 0).Items)
		for _, want := range []string{"alice: loop.go:12: Fixed in the next push", "alice: Ready for another look", "alice: Self-review after your finding", "carol: loop.go:12: I prefer the old name"} {
			if !strings.Contains(quiet, want) {
				t.Errorf("under quiet the reviewer's inbox = %q, want %q", quiet, want)
			}
		}
		for _, own := range []string{"Thanks, checking now", "Looking again", "One finding left"} {
			if strings.Contains(quiet, own) {
				t.Errorf("under quiet the reviewer's inbox = %q, want none of its own %q", quiet, own)
			}
		}

		gh.resolveThread("thread-bob", true)
		gh.resolveThread("thread-alice", true)
		w.advance(protocol.HeatHotInterval)
		gh.resolveThread("thread-bob", false)
		gh.resolveThread("thread-alice", false)
		w.advance(protocol.HeatHotInterval)
		if reopened := inboxContents(readInbox(t, cli, "reviewer", 0).Items); strings.Count(reopened, "review thread reopened") != 2 {
			t.Errorf("the reviewer's inbox = %q, want both reopened threads, because GitHub does not say who reopened one", reopened)
		}

		if _, err := cli.SetSetting(client.SettingsCaller{SessionID: "reviewer"}, "pull_requests.own_comments", "ring"); err != nil {
			t.Fatalf("set pull_requests.own_comments to ring: %v", err)
		}
		gh.reply("thread-bob", "reply-bob-2", "bob", "Second look done")
		w.advance(protocol.HeatHotInterval)
		ringing := inboxContents(readInbox(t, cli, "reviewer", 0).Items)
		if !strings.Contains(ringing, "bob: loop.go:12: Second look done") || strings.Contains(ringing, "Thanks, checking now") {
			t.Errorf("under ring the reviewer's inbox = %q, want its new reply without replaying the one skipped earlier", ringing)
		}
	})
}
