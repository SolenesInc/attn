package daemon_test

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/github/mockserver"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/testworld"
)

const prPollTimer stepclock.Name = "daemon-pr-poll"

const bridgeSafeStep = bridge.StallAfter - 500*time.Millisecond

const (
	homeGitHubToken    = "home-token"
	outpostGitHubToken = "outpost-token"
)

func serveAccountGitHub(t *testing.T, home, outpost *testworld.Stack) *mockserver.Server {
	t.Helper()
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	gh.Config.SetKeepAlivesEnabled(false)
	for s, token := range map[*testworld.Stack]string{home: homeGitHubToken, outpost: outpostGitHubToken} {
		s.Vars = append(s.Vars, "ATTN_MOCK_GH_URL="+gh.URL, "ATTN_MOCK_GH_TOKEN="+token, "ATTN_MOCK_GH_HOST=github.com")
	}
	return gh
}

func pullRequestReadsBy(requests []mockserver.RequestLog, token string) (searches, reads []string) {
	for _, request := range requests {
		if request.Token != token {
			continue
		}
		switch {
		case strings.HasPrefix(request.Path, "/search/"):
			searches = append(searches, request.Path)
		case strings.Contains(request.Path, "/pulls/"), request.Path == "/graphql":
			reads = append(reads, request.Method+" "+request.Path)
		}
	}
	return searches, reads
}

var pullRequestNews = regexp.MustCompile(`^Pull request monitor: (.+) for \S+#(\d+)\.`)

type pullRequestWatcher struct {
	name    string
	cli     *client.Client
	session string
}

func (w pullRequestWatcher) news(t *testing.T) (news []string, contents string) {
	t.Helper()
	items := readInbox(t, w.cli, w.session, 0).Items
	for _, item := range items {
		m := pullRequestNews.FindStringSubmatch(item.Content)
		if m == nil {
			news = append(news, "unexpected: "+item.Content)
			continue
		}
		news = append(news, m[1]+" #"+m[2])
	}
	slices.Sort(news)
	return news, inboxContents(items)
}

func TestEveryPullRequestUpdateReachesWatchersOnTheHomeAndTheOutpostWhileOnlyTheHomeAsksGitHub(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	gh := serveAccountGitHub(t, home, outpost)
	const repo = "acme/shop"
	for _, number := range []int{71, 72, 73, 74, 75} {
		gh.AddPR(mockserver.MockPR{Repo: repo, Number: number, Title: fmt.Sprintf("Change %d", number), Role: "author", State: "open", MergeableState: "clean"})
	}
	moveAll := func(numbers []int, change func(pr *mockserver.MockPR)) {
		for _, number := range numbers {
			gh.UpdatePR(repo, number, change)
		}
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	app, cli := pair.App, home.Client()
	testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, row := range m.Outposts {
			if row.ID == pair.Host && row.Notebook != nil && row.Notebook.State == protocol.OutpostNotebookStateInSync {
				return true
			}
		}
		return false
	})
	enrolled := len(gh.Requests())

	onHome := pullRequestWatcher{name: "the home agent", cli: cli, session: home.Spawn(app, fakeagent.Claude, home.Path("shop"))}
	outpostSession := home.Spawn(app, fakeagent.Claude, outpost.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	terminal := protocol.TerminalID(app.Terminal(outpostSession))
	outpost.LaunchedCarrying(string(terminal))
	onOutpost := pullRequestWatcher{name: "the outpost agent", cli: outpost.Client(), session: outpostSession}

	watch := func(w pullRequestWatcher, number int, mode protocol.PullRequestWatchMode, reviewer string) {
		t.Helper()
		url := shopPull(number)
		var err error
		if w.cli == cli {
			err = w.cli.RecordPullRequestCreated(protocol.SessionID(w.session), url)
		} else {
			err = w.cli.RecordPullRequestCreatedFromTerminal(terminal, url)
		}
		if err != nil {
			t.Fatalf("%s records #%d: %v", w.name, number, err)
		}
		if err := w.cli.WatchSessionPullRequest(protocol.SessionID(w.session), "", url, mode, reviewer); err != nil {
			t.Fatalf("%s watches #%d in %s mode: %v", w.name, number, mode, err)
		}
	}
	watch(onHome, 71, protocol.PullRequestWatchModeGreen, "")
	watch(onHome, 72, protocol.PullRequestWatchModeCodex, "")
	watch(onHome, 73, protocol.PullRequestWatchModeFormalReview, "alice")
	watch(onOutpost, 71, protocol.PullRequestWatchModeCodex, "")
	watch(onOutpost, 72, protocol.PullRequestWatchModeFormalReview, "alice")
	watch(onOutpost, 73, protocol.PullRequestWatchModeGreen, "")
	watch(onHome, 74, protocol.PullRequestWatchModeGreen, "")

	arm := func(w pullRequestWatcher, number int) string {
		t.Helper()
		seed := plantSeedAs(t, w.cli, w.session, fmt.Sprintf("Ship #%d", number))
		moveDelegationSeed(t, w.cli, w.session, seed, "tend", "")
		harvestArm(t, w.cli, w.session, seed, shopPull(number))
		return seed
	}
	armedOnAWatchedPull := arm(onOutpost, 71)
	armedOnAnUnwatchedPull := arm(onHome, 75)

	outpost.AwaitTimer(prPollTimer)
	home.AwaitTimer(prPollTimer)
	pollTicks := func(s *testworld.Stack) {
		t.Helper()
		for range 3 {
			s.FirePending(prPollTimer)
			s.AwaitTimer(prPollTimer)
		}
	}
	homeSearches, _ := pullRequestReadsBy(gh.Requests(), homeGitHubToken)
	pollTicks(outpost)
	pollTicks(home)
	if searches, _ := pullRequestReadsBy(gh.Requests(), homeGitHubToken); len(searches) == len(homeSearches) {
		t.Errorf("over three poll ticks the home made no pull request search, want it to keep polling")
	}

	pastHotInterval := func(times int) {
		t.Helper()
		for stepped := time.Duration(0); stepped <= time.Duration(times)*protocol.HeatHotInterval; stepped += bridgeSafeStep {
			home.Advance(bridgeSafeStep)
			home.AwaitTimer(bridge.StallTimer)
		}
		home.AwaitJobsSettled()
	}
	hears := func(when string, want map[pullRequestWatcher][]string, details ...string) {
		t.Helper()
		for _, w := range []pullRequestWatcher{onHome, onOutpost} {
			expected := append([]string{}, want[w]...)
			slices.Sort(expected)
			got, contents := w.news(t)
			if !slices.Equal(got, expected) {
				t.Errorf("%s, %s heard %q, want %q; its inbox = %q", when, w.name, got, expected, contents)
			}
			comments := 0
			for _, news := range expected {
				if strings.HasPrefix(news, "new comment ") {
					comments++
				}
			}
			for _, detail := range details {
				if strings.Count(contents, detail) != comments {
					t.Errorf("%s, %s's inbox = %q, want %q in each of its %d new comments", when, w.name, contents, detail, comments)
				}
			}
		}
	}
	seedStatusIs := func(when, seed, want string) {
		t.Helper()
		if got := seedStatus(t, cli, seed); got != want {
			t.Errorf("%s, seed %s is %q, want %q", when, seed, got, want)
		}
	}

	pastHotInterval(1)
	hears("once the watches start", map[pullRequestWatcher][]string{
		onHome:    {"ready #71", "ready #74"},
		onOutpost: {"ready #73"},
	})

	watched := []int{71, 72, 73}
	moveAll(watched, func(pr *mockserver.MockPR) { pr.FailingChecks, pr.MergeableState = []string{"test"}, "unstable" })
	pastHotInterval(1)
	hears("after checks fail", map[pullRequestWatcher][]string{
		onHome:    {"checks failed #71", "checks failed #72", "checks failed #73"},
		onOutpost: {"checks failed #71", "checks failed #72", "checks failed #73"},
	})

	moveAll(watched, func(pr *mockserver.MockPR) {
		pr.FailingChecks, pr.MergeableState = nil, "clean"
		pr.Comments = append(pr.Comments, mockserver.MockComment{ID: fmt.Sprintf("comment-alice-%d", pr.Number), Author: "alice", Body: "Please rename the flag", CreatedAt: "2026-11-17T10:00:00Z"})
	})
	pastHotInterval(1)
	hears("after the checks pass and alice comments", map[pullRequestWatcher][]string{
		onHome:    {"new comment #71", "new comment #72", "new comment #73", "ready #71"},
		onOutpost: {"new comment #71", "new comment #72", "new comment #73", "ready #73"},
	}, "alice: Please rename the flag")

	moveAll(watched, func(pr *mockserver.MockPR) {
		pr.CodexThumbsUp = true
		pr.Reviews = []mockserver.MockReview{{ID: fmt.Sprintf("review-alice-%d", pr.Number), Author: "alice", State: "APPROVED", SubmittedAt: "2026-11-17T11:00:00Z"}}
	})
	pastHotInterval(1)
	hears("after Codex and alice approve", map[pullRequestWatcher][]string{
		onHome:    {"ready #72", "ready #73"},
		onOutpost: {"ready #71", "ready #72"},
	})

	moveAll([]int{71, 73, 75}, func(pr *mockserver.MockPR) { pr.State, pr.Merged = "closed", true })
	gh.UpdatePR(repo, 72, func(pr *mockserver.MockPR) { pr.State = "closed" })
	pastHotInterval(1)
	hears("after #71 and #73 merge and #72 closes", map[pullRequestWatcher][]string{
		onHome:    {"merged #71", "closed #72", "merged #73"},
		onOutpost: {"merged #71", "closed #72", "merged #73"},
	})
	seedStatusIs("after #71 merges", armedOnAWatchedPull, "harvested")
	seedStatusIs("after #75 merges", armedOnAnUnwatchedPull, "harvested")

	mark := len(app.Log())
	pair.Disconnect()
	awaitOutpostState(app, mark, pair.Host, protocol.OutpostStateLost)
	outpost.Advance(protocol.HeatHotInterval)
	outpost.App()
	onOutpost.cli = cli
	pastHotIntervalsAlone := func(times int) {
		t.Helper()
		for range times {
			home.Advance(protocol.HeatHotInterval)
			home.AwaitJobsSettled()
		}
	}
	gh.UpdatePR(repo, 74, func(pr *mockserver.MockPR) { pr.Gone = true })
	pastHotIntervalsAlone(1)
	hears("once GitHub cannot find #74", map[pullRequestWatcher][]string{
		onHome: {"monitoring is delayed #74"},
	})
	pastHotIntervalsAlone(int(protocol.HeatColdInterval/protocol.HeatHotInterval) + 1)
	hears("after GitHub cannot find #74 for longer than the cold interval", map[pullRequestWatcher][]string{
		onHome: {"watch ended #74"},
	})

	ended := len(gh.Requests())
	pastHotIntervalsAlone(2)
	for _, request := range gh.Requests()[ended:] {
		if request.Path == "/graphql" && fmt.Sprint(request.Body["variables"].(map[string]any)["number"]) == "74" {
			t.Errorf("after its watch ended, #74 was read again: %+v", request)
			break
		}
	}
	hears("after every watch ended", nil)

	if searches, reads := pullRequestReadsBy(gh.Requests()[enrolled:], outpostGitHubToken); len(searches) > 0 || len(reads) > 0 {
		t.Errorf("once enrolled, the outpost asked GitHub %d searches and %d pull request reads, want none: %q %q", len(searches), len(reads), searches, reads)
	}
}
