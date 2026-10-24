package daemon_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASessionsPullRequestsComeBackNewestFirstAndOncePerSession(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	s1 := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	s2 := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	recordPullRequest(t, cli, s1, shopPull(1))
	recordPullRequest(t, cli, s1, shopPull(2))
	recordPullRequest(t, cli, s2, shopPull(1))
	recordPullRequest(t, cli, s1, shopPull(1))
	if got := pullNumbers(queriedSession(t, cli, s1)); !slices.Equal(got, []int{2, 1}) {
		t.Errorf("first session's pull requests after recording #1 again = %v, want [2 1]", got)
	}
	if got := pullNumbers(queriedSession(t, cli, s2)); !slices.Equal(got, []int{1}) {
		t.Errorf("second session's pull requests = %v, want [1]", got)
	}

	if err := cli.ForgetSessionPullRequest(protocol.SessionID(s1), shopPull(1)); err != nil {
		t.Fatalf("forget: %v", err)
	}
	testworld.AwaitSession(app, s1, func(s protocol.Session) bool { return slices.Equal(pullNumbers(s), []int{2}) })
	if got := pullNumbers(queriedSession(t, cli, s2)); !slices.Equal(got, []int{1}) {
		t.Errorf("second session's pull requests after the first forgot #1 = %v, want its own kept", got)
	}
	if err := cli.UpdateState(protocol.TerminalID(w.Terminal(s1)), protocol.StateWorking); err != nil {
		t.Fatal(err)
	}
	if got := pullNumbers(queriedSession(t, cli, s1)); !slices.Equal(got, []int{2}) {
		t.Fatalf("a later state report restored the forgotten pull request: %v", got)
	}
	recordPullRequest(t, cli, s1, shopPull(1))
	if got := pullNumbers(queriedSession(t, cli, s1)); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("reporting the forgotten pull request again = %v, want [1 2]", got)
	}
}

func TestAPullRequestReportedByTerminalBelongsToTheSessionItShows(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	if err := cli.RecordPullRequestCreatedFromTerminal(protocol.TerminalID(w.Terminal(session)), shopPull(3)); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return slices.Equal(pullNumbers(s), []int{3})
	})
	if got := pullNumbers(queriedSession(t, cli, session)); !slices.Equal(got, []int{3}) {
		t.Fatalf("reported pull requests = %v, want [3]", got)
	}
}

func TestADriverReportsPullRequestsOnlyForItsOwnRun(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
		session, run := spawnDriven(w, app, driver, w.Path("shop"))
		for _, refusal := range []struct{ session, run, url string }{
			{run.SessionID, "another-run", shopPull(1)},
			{"unknown-terminal", run.RunID, shopPull(1)},
			{run.SessionID, run.RunID, "https://github.com/acme/shop"},
		} {
			if err := driver.report("session.report_pull_request", map[string]any{
				"session_id": refusal.session, "run_id": refusal.run, "url": refusal.url,
			}); err == nil {
				t.Fatalf("accepted invalid report %+v", refusal)
			}
		}
		if got := pullNumbers(queriedSession(t, cli, session)); len(got) != 0 {
			t.Fatalf("refused reports recorded pull requests %v", got)
		}
		driver.mustReport("session.report_pull_request", map[string]any{
			"session_id": run.SessionID, "run_id": run.RunID, "url": shopPull(2),
		})
		testworld.AwaitSession(app, session, func(s protocol.Session) bool {
			return slices.Equal(pullNumbers(s), []int{2})
		})
	})
}

func TestAMergeHarvestsTheSeedArmedOnItEvenAfterItsSessionsClosed(t *testing.T) {
	gh := newPullRequestStates(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "author", "reviewer")
		armed := plantSeedAs(t, cli, "author", "Ship checkout")
		cleared := plantSeedAs(t, cli, "author", "Document checkout")
		recordPullRequest(t, cli, "reviewer", shopPull(7))
		armWhenMerged(t, cli, "author", armed, shopPull(7))
		armWhenMerged(t, cli, "author", cleared, shopPull(9))
		if _, err := cli.SeedTransition("author", cleared, "harvest", "", "", false, client.SeedTransitionOptions{ClearHarvestWhen: true}); err != nil {
			t.Fatalf("clear the condition: %v", err)
		}
		if err := cli.ForgetSessionPullRequest("reviewer", shopPull(7)); err != nil {
			t.Fatalf("forget: %v", err)
		}
		closeSession(t, cli, "author", "handed off to review")

		gh.merge(7, 9)
		w.advance(protocol.HeatHotInterval)

		if status := seedStatus(t, cli, armed); status != "harvested" {
			t.Errorf("the seed armed on the merged #7 is %q, want harvested", status)
		}
		if status := seedStatus(t, cli, cleared); status == "harvested" {
			t.Error("the seed whose condition was cleared was harvested when #9 merged")
		}
	})
}

func shopPull(number int) string {
	return fmt.Sprintf("https://github.com/acme/shop/pull/%d", number)
}

func recordPullRequest(t *testing.T, cli *client.Client, session, url string) {
	t.Helper()
	if err := cli.RecordPullRequestCreated(protocol.SessionID(session), url); err != nil {
		t.Fatalf("%s records %s: %v", session, url, err)
	}
}

func pullNumbers(s protocol.Session) []int {
	numbers := make([]int, 0, len(s.PullRequests))
	for _, pr := range s.PullRequests {
		numbers = append(numbers, pr.Number)
	}
	return numbers
}

func armWhenMerged(t *testing.T, cli *client.Client, session, seed, url string) {
	t.Helper()
	opts := client.SeedTransitionOptions{WhenMerged: true, PullRequestURL: url}
	if _, err := cli.SeedTransition(protocol.SessionID(session), seed, "harvest", "", "", false, opts); err != nil {
		t.Fatalf("arm %s on %s: %v", seed, url, err)
	}
}

func seedStatus(t *testing.T, cli *client.Client, seed string) string {
	t.Helper()
	shown, err := cli.SeedShow("", seed)
	if err != nil {
		t.Fatalf("show %s: %v", seed, err)
	}
	return shown.Seed.Status
}

type pullRequestStates struct {
	mu     sync.Mutex
	merged map[int]bool
}

func newPullRequestStates(t *testing.T) *pullRequestStates {
	t.Helper()
	states := &pullRequestStates{merged: map[int]bool{}}
	pull := regexp.MustCompile(`^/repos/acme/shop/pulls/(\d+)$`)
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Connection", "close")
		match := pull.FindStringSubmatch(r.URL.Path)
		if match == nil {
			_ = json.NewEncoder(rw).Encode(map[string]any{"total_count": 0, "items": []any{}})
			return
		}
		number, _ := strconv.Atoi(match[1])
		states.mu.Lock()
		merged := states.merged[number]
		states.mu.Unlock()
		state := "open"
		if merged {
			state = "closed"
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"number": number, "html_url": shopPull(number), "title": fmt.Sprintf("Change %d", number),
			"state": state, "merged": merged,
		})
	}))
	t.Cleanup(server.Close)
	t.Setenv("ATTN_MOCK_GH_URL", server.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	t.Setenv("ATTN_MOCK_GH_HOST", "github.com")
	return states
}

func (s *pullRequestStates) merge(numbers ...int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, number := range numbers {
		s.merged[number] = true
	}
}

func TestAPullRequestTheNextChiefForgetsStaysForgottenForThePreviousChiefAfterARestart(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		previous, next := w.bubbleClaude(t, app, "shop"), w.bubbleClaude(t, app, "cart")
		setChiefOfStaff(app, previous.id, true)
		recordPullRequest(t, cli, previous.id, shopPull(5))
		if err := cli.WatchSessionPullRequest(protocol.SessionID(previous.id), shopPull(5), protocol.PullRequestWatchModeCodex, ""); err != nil {
			t.Fatal(err)
		}
		setChiefOfStaff(app, next.id, true)
		if err := cli.ForgetSessionPullRequest(protocol.SessionID(next.id), shopPull(5)); err != nil {
			t.Fatal(err)
		}
		if got := pullNumbers(queriedSession(t, cli, previous.id)); len(got) != 0 {
			t.Fatalf("after the chief's forget the previous chief links %v, want none", got)
		}

		w.stop()
		w.start()
		previous.cli = w.Client()
		previous.reply("Back. <!-- attn:state=waiting_input -->")
		w.advance(time.Second)
		if got := pullNumbers(queriedSession(t, w.Client(), previous.id)); len(got) != 0 {
			t.Fatalf("after the restart the previous chief links %v again, want the forgotten pull request gone", got)
		}
	})
}
