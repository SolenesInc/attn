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
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASessionsPullRequestsComeBackNewestFirstAndOncePerSession(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client()
		registerSessions(t, w, cli, "s1", "s2")
		recordPullRequest(t, cli, "s1", shopPull(1))
		w.advance(time.Second)
		recordPullRequest(t, cli, "s1", shopPull(2))
		recordPullRequest(t, cli, "s2", shopPull(1))
		w.advance(time.Second)
		recordPullRequest(t, cli, "s1", shopPull(1))
		if got := pullNumbers(queriedSession(t, cli, "s1")); !slices.Equal(got, []int{2, 1}) {
			t.Errorf("s1 pull requests after recording #1 again = %v, want [2 1]", got)
		}
		if got := pullNumbers(queriedSession(t, cli, "s2")); !slices.Equal(got, []int{1}) {
			t.Errorf("s2 pull requests = %v, want [1]", got)
		}

		if err := cli.ForgetSessionPullRequest("s1", shopPull(1)); err != nil {
			t.Fatalf("forget: %v", err)
		}
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return slices.Equal(pullNumbers(s), []int{2}) })
		if got := pullNumbers(queriedSession(t, cli, "s2")); !slices.Equal(got, []int{1}) {
			t.Errorf("s2 pull requests after s1 forgot #1 = %v, want its own kept", got)
		}
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
	if err := cli.RecordPullRequestCreated(session, url); err != nil {
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
	if _, err := cli.SeedTransition(session, seed, "harvest", "", "", false, opts); err != nil {
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
