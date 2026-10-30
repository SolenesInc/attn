package daemon_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestArmingASeedParksItOnItsPullRequest(t *testing.T) {
	github := newHarvestGitHub(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "shipper")
		url := github.open(71, "Ship the daemon")
		planted, err := cli.SeedPlant("shipper", "ship the daemon", "", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		seed := planted.Seed.ID
		if planted.Seed.HarvestWhen != nil {
			t.Fatalf("an unarmed seed carries a condition: %+v", planted.Seed.HarvestWhen)
		}
		lifeMove(t, cli, "shipper", seed, "tend", "", "trellis")

		armedAt := time.Now()
		armed := harvestArmAs(t, cli, "shipper", "trellis", seed, url).Seed
		if armed.Status != "dormant" || armed.TenderSession != "" || armed.TenderMember != "" {
			t.Errorf("arming a growing seed left it %s held by %q/%q, want it dormant and released", armed.Status, armed.TenderSession, armed.TenderMember)
		}
		condition := armed.HarvestWhen
		if condition == nil || condition.PullRequest != "github.test:acme/shop#71" || condition.URL != url || protocol.Deref(condition.SetByMember) != "trellis" {
			t.Fatalf("the armed seed waits on %+v, want PR 71 set by trellis", condition)
		}
		if at, err := time.Parse(time.RFC3339Nano, condition.SetAt); err != nil || !at.Equal(armedAt) {
			t.Errorf("the condition was set at %q, want %s", condition.SetAt, armedAt.UTC().Format(time.RFC3339Nano))
		}
		if notes := lifeNoteBodies(t, cli, seed); !slices.Contains(notes, "harvests when acme/shop#71 merges") {
			t.Errorf("the log = %q, want it to say what the seed waits on", notes)
		}
		if pulls := harvestSessionPullRequests(t, cli, "shipper"); !slices.Equal(pulls, []string{url}) {
			t.Errorf("the arming session tracks pull requests %v, want %s", pulls, url)
		}

		for _, parked := range []bool{false, true} {
			other := plantSeedAs(t, cli, "shipper", "wait on the merge")
			want := "planted"
			if parked {
				lifeMove(t, cli, "shipper", other, "tend", "", "")
				lifeMove(t, cli, "shipper", other, "park", "", "")
				want = "dormant"
			}
			armed := harvestArm(t, cli, "shipper", other, url).Seed
			if armed.Status != want || armed.HarvestWhen == nil || protocol.Deref(armed.HarvestWhen.SetBySession) != "shipper" {
				t.Errorf("arming a %s seed left it %s with condition %+v, want it unmoved and armed by shipper", want, armed.Status, armed.HarvestWhen)
			}
		}

		unreachable := plantSeedAs(t, cli, "shipper", "waits on an unreachable pull request")
		harvestArm(t, cli, "shipper", unreachable, github.open(73, "Behind an outage"))
		github.fail(73, true)
		w.advance(protocol.HeatHotInterval)
		fetchedAt := time.Now()
		github.fail(71, true)
		w.advance(protocol.HeatHotInterval)

		shown := lifeShow(t, cli, seed)
		checked := protocol.Deref(shown.Seed.HarvestWhen.CheckedAt)
		if at, err := time.Parse(time.RFC3339Nano, checked); err != nil || !at.Equal(fetchedAt) {
			t.Errorf("the condition reports a check at %q, want the last successful fetch at %s", checked, fetchedAt.UTC().Format(time.RFC3339Nano))
		}
		if references := shown.References; len(references) != 1 || protocol.Deref(references[0].URL) != url {
			t.Errorf("the seed's references = %+v, want the pull request", references)
		}
		if never := lifeShow(t, cli, unreachable).Seed.HarvestWhen; never == nil || never.CheckedAt != nil {
			t.Errorf("a condition whose every fetch failed reads %+v, want it armed with no check", never)
		}
	})
}

func TestArmingWithoutAURLWaitsOnTheSessionsOnlyOpenPullRequest(t *testing.T) {
	github := newHarvestGitHub(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "shipper")
		seed := plantSeedAs(t, cli, "shipper", "infer it")
		armWithoutURL := func() (*protocol.SeedTransitionResult, error) {
			return cli.SeedTransition("shipper", seed, "harvest", "", "", false, client.SeedTransitionOptions{WhenMerged: true})
		}

		_, err := armWithoutURL()
		lifeRefusal(t, "arming a session with no pull request", err, "has no open pull request", "--when-merged <pr-url>")

		old, current := github.open(70, "An old one"), github.open(71, "The current one")
		for _, url := range []string{old, current} {
			if err := cli.RecordPullRequestCreated("shipper", url); err != nil {
				t.Fatal(err)
			}
		}
		_, err = armWithoutURL()
		lifeRefusal(t, "arming a session with two open pull requests", err, "has 2 open pull requests", old, current)

		github.close(70, "An old one")
		w.advance(protocol.HeatHotInterval)
		armed, err := armWithoutURL()
		if err != nil {
			t.Fatalf("arming once only one pull request is open: %v", err)
		}
		if condition := armed.Seed.HarvestWhen; condition == nil || condition.URL != current {
			t.Errorf("arming picked %+v, want the open pull request %s", condition, current)
		}
	})
}

func TestArmingRefusalsNameTheirReason(t *testing.T) {
	github := newHarvestGitHub(t)
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	shipper := spawnPanes(w, app, w.Path("shipper"))[0].session
	url := github.open(71, "Ship it")
	seed := plantSeedAs(t, cli, shipper, "never armed")
	withered := plantSeedAs(t, cli, shipper, "abandoned")
	lifeMove(t, cli, shipper, withered, "wither", "not needed", "")

	for _, refusal := range []struct {
		name    string
		session string
		seed    string
		reason  string
		opts    client.SeedTransitionOptions
		want    string
	}{
		{"arming without a session", "", seed, "", client.SeedTransitionOptions{WhenMerged: true, PullRequestURL: url}, "needs a session to track the pull request"},
		{"arming a withered seed", shipper, withered, "", client.SeedTransitionOptions{WhenMerged: true, PullRequestURL: url}, "waits on nothing"},
		{"arming with an oversized reason", shipper, seed, strings.Repeat("x", garden.MaxReasonChars+1), client.SeedTransitionOptions{WhenMerged: true, PullRequestURL: url}, "limit is 400"},
		{"clearing a seed that waits on nothing", shipper, seed, "", client.SeedTransitionOptions{WhenMerged: true, ClearHarvestWhen: true}, "has no harvest condition"},
	} {
		_, err := cli.SeedTransition(protocol.SessionID(refusal.session), refusal.seed, "harvest", refusal.reason, "", false, refusal.opts)
		lifeRefusal(t, refusal.name, err, refusal.want)
	}
	if shown := lifeShow(t, cli, seed).Seed; shown.Status != "planted" || shown.HarvestWhen != nil {
		t.Errorf("after the refusals the seed is %s waiting on %+v, want it planted and unarmed", shown.Status, shown.HarvestWhen)
	}
}

func TestAMergedPullRequestHarvestsTheSeedArmedOnIt(t *testing.T) {
	github := newHarvestGitHub(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "shipper", "holder", "keeper")

		landed := github.merge(70, "Carry a harvest condition on the seed")
		if err := cli.RecordPullRequestCreated("shipper", landed); err != nil {
			t.Fatal(err)
		}
		w.advance(protocol.HeatHotInterval)
		already := plantSeedAs(t, cli, "shipper", "already landed")
		harvested := harvestArm(t, cli, "shipper", already, landed).Seed
		if harvested.Status != "harvested" || protocol.Deref(harvested.Reason) != "PR #70 merged: Carry a harvest condition on the seed" || harvested.HarvestWhen != nil {
			t.Errorf("arming on a merged pull request left %s with reason %q waiting on %+v, want it harvested with the merge as its reason",
				harvested.Status, protocol.Deref(harvested.Reason), harvested.HarvestWhen)
		}

		pending := github.open(71, "Keep harvest-on-merge alive")
		armed := plantSeedAs(t, cli, "shipper", "outlives its session")
		harvestArm(t, cli, "shipper", armed, pending)
		lifeMove(t, cli, "holder", armed, "tend", "", "")
		untouched := plantSeedAs(t, cli, "shipper", "waits on nothing")
		forgotten := github.open(72, "Nobody tracks this one")
		untracked := plantSeedAs(t, cli, "keeper", "armed on an untracked pull request")
		harvestArm(t, cli, "keeper", untracked, forgotten)
		if err := cli.ForgetSessionPullRequest("keeper", forgotten); err != nil {
			t.Fatalf("forget the pull request: %v", err)
		}
		if err := cli.Unregister("shipper"); err != nil {
			t.Fatal(err)
		}

		title := strings.Repeat("a very long pull request title ", 40)
		github.merge(71, title)
		github.merge(72, "Nobody tracks this one")
		w.advance(protocol.HeatHotInterval)

		got := lifeShow(t, cli, armed).Seed
		reason := protocol.Deref(got.Reason)
		if got.Status != "harvested" || got.HarvestWhen != nil {
			t.Errorf("after the merge the armed seed is %s waiting on %+v, want it harvested", got.Status, got.HarvestWhen)
		}
		if n := utf8.RuneCountInString(reason); n > garden.MaxReasonChars || n < garden.MaxReasonChars-2 || !strings.HasPrefix(reason, "PR #71 merged: a very long") || !strings.HasSuffix(reason, "…") {
			t.Errorf("the harvest reason is %d characters: %q, want the merge trimmed to fit %d with an ellipsis", n, reason, garden.MaxReasonChars)
		}
		if notes := lifeNoteBodies(t, cli, armed); !slices.Contains(notes, "attn forced `attn seed harvest "+armed+"`; holder held the seed.") {
			t.Errorf("the log = %q, want the forced harvest over the holder recorded", notes)
		}
		if status := lifeShow(t, cli, untouched).Seed.Status; status != "planted" {
			t.Errorf("an unarmed seed moved to %s", status)
		}
		if shown := lifeShow(t, cli, untracked).Seed; shown.Status != "planted" || shown.HarvestWhen == nil {
			t.Errorf("the seed armed on an untracked pull request is %s waiting on %+v, want it left armed", shown.Status, shown.HarvestWhen)
		}
	})
}

func TestAPullRequestClosedWithoutMergingClearsTheHarvestCondition(t *testing.T) {
	github := newHarvestGitHub(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "shipper", "watcher")
		seed := plantSeedAs(t, cli, "shipper", "waits on a doomed pull request")
		if _, err := cli.SeedWatch("watcher", seed, false); err != nil {
			t.Fatal(err)
		}
		lifeMove(t, cli, "shipper", seed, "tend", "", "")
		harvestArm(t, cli, "shipper", seed, github.open(71, "Harvest on merge"))
		w.advance(0)
		readInbox(t, cli, "watcher", 0)
		notesBefore := len(lifeNoteBodies(t, cli, seed))

		github.close(71, "Harvest on merge")
		w.advance(protocol.HeatHotInterval)

		got := lifeShow(t, cli, seed).Seed
		if got.Status != "dormant" || got.HarvestWhen != nil {
			t.Errorf("after the pull request closed the seed is %s waiting on %+v, want it dormant and unarmed", got.Status, got.HarvestWhen)
		}
		if notes := lifeNoteBodies(t, cli, seed); len(notes) != notesBefore+1 || notes[0] != "PR #71 closed without merging; harvest-on-merge cleared" {
			t.Errorf("the log = %q, want one new line saying the pull request closed without merging", notes)
		}
		if items := readInbox(t, cli, "watcher", 0).Items; len(items) != 1 || !strings.Contains(items[0].Content, seed+" moved: harvest_when.cleared") {
			t.Errorf("the watcher holds %q, want one harvest_when.cleared bell about %s", inboxContents(items), seed)
		}
	})
}

func TestAClearedOrEditedHarvestConditionMeetsTheMergeAsItStandsNow(t *testing.T) {
	github := newHarvestGitHub(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "shipper")
		cleared := plantSeedAs(t, cli, "shipper", "changed my mind")
		lifeMove(t, cli, "shipper", cleared, "tend", "", "")
		harvestArm(t, cli, "shipper", cleared, github.open(71, "Harvest on merge"))
		result, err := cli.SeedTransition("shipper", cleared, "harvest", "", "", false, client.SeedTransitionOptions{WhenMerged: true, ClearHarvestWhen: true})
		if err != nil {
			t.Fatalf("clear: %v", err)
		}
		if result.Seed.HarvestWhen != nil || result.Seed.Status != "dormant" {
			t.Errorf("clearing left the seed %s waiting on %+v, want it dormant and unarmed", result.Seed.Status, result.Seed.HarvestWhen)
		}
		if notes := lifeNoteBodies(t, cli, cleared); !slices.Contains(notes, "harvest-on-merge cleared") {
			t.Errorf("the log = %q, want it to say the seed stopped waiting", notes)
		}

		edited := plantSeedAs(t, cli, "shipper", "edited while it waits")
		harvestArm(t, cli, "shipper", edited, github.open(72, "Another merge"))
		if _, err := editSeedBody(t, cli, edited, "the same promise, a better body"); err != nil {
			t.Fatal(err)
		}

		github.merge(71, "Harvest on merge")
		github.merge(72, "Another merge")
		w.advance(protocol.HeatHotInterval)
		if status := lifeShow(t, cli, cleared).Seed.Status; status != "dormant" {
			t.Errorf("the merge of the pull request a cleared seed used to wait on moved it to %s", status)
		}
		if got := lifeShow(t, cli, edited).Seed; got.Status != "harvested" || got.Body != "the same promise, a better body" {
			t.Errorf("the edited armed seed is %s with body %q after the merge, want it harvested with the edit", got.Status, got.Body)
		}
	})
}

func TestArmingASeedSomebodyElseHoldsTakesForce(t *testing.T) {
	github := newHarvestGitHub(t)
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	panes := spawnPanes(w, app, w.Path("shipper"), w.Path("holder"))
	shipper, holder := panes[0].session, panes[1].session
	url := github.open(71, "Ship it")
	seed := plantSeedAs(t, cli, shipper, "held elsewhere")
	lifeMove(t, cli, holder, seed, "tend", "", "")

	options := client.SeedTransitionOptions{WhenMerged: true, PullRequestURL: url}
	_, err := cli.SeedTransition(protocol.SessionID(shipper), seed, "harvest", "", "trellis", false, options)
	lifeRefusal(t, "arming a seed another session tends", err, "is being tended by")
	forced, err := cli.SeedTransition(protocol.SessionID(shipper), seed, "harvest", "", "trellis", true, options)
	if err != nil {
		t.Fatalf("forced arm: %v", err)
	}
	if forced.Seed.Status != "dormant" {
		t.Errorf("a forced arm left the seed %s, want it dormant", forced.Seed.Status)
	}
	if notes := lifeNoteBodies(t, cli, seed); !slices.Contains(notes, "Trellis forced `attn seed park "+seed+"`; "+holder+" held the seed.") {
		t.Errorf("the log = %q, want the takeover recorded", notes)
	}
}

func harvestArm(t *testing.T, cli *client.Client, session, seedID, url string) *protocol.SeedTransitionResult {
	t.Helper()
	return harvestArmAs(t, cli, session, "", seedID, url)
}

func TestRepeatedSeedMovesKeepTheStateAndDoNotRing(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "worker", "watcher")
		for _, verb := range []string{"tend", "park", "harvest", "wither", "replant"} {
			seed := plantSeedAs(t, cli, "worker", verb+" twice")
			reason := ""
			if verb == "harvest" || verb == "wither" {
				reason = "finished"
			}
			before := lifeMove(t, cli, "worker", seed, verb, reason, "")
			gardenNudgeWatch(t, cli, "watcher", seed, false)
			w.advance(0)
			again, err := cli.SeedTransition("worker", seed, verb, reason, "", false, client.SeedTransitionOptions{})
			if err != nil {
				t.Fatalf("repeat %s: %v", verb, err)
			}
			if again.Seed.Rev != before.Rev || again.Seed.StateChangedAt != before.StateChangedAt || protocol.Deref(again.Moved) || len(again.RingRecipients) != 0 {
				t.Errorf("repeat %s changed the seed: before %+v, after %+v", verb, before, again.Seed)
			}
			w.advance(0)
			gardenNudgeInboxIsEmpty(t, cli, "watcher", "after repeated "+verb)
			if got := lifeNoteBodies(t, cli, seed); len(got) != 0 {
				t.Errorf("repeat %s added notes: %q", verb, got)
			}
		}
		seed := plantSeedAs(t, cli, "worker", "retain the second closer's reason")
		before := lifeMove(t, cli, "worker", seed, "harvest", "original result", "")
		gardenNudgeWatch(t, cli, "watcher", seed, false)
		again, err := cli.SeedTransition("watcher", seed, "harvest", "second receipt", "", false, client.SeedTransitionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if again.Seed.Rev != before.Rev || protocol.Deref(again.Seed.Reason) != "original result" {
			t.Fatalf("second close replaced the original: %+v", again.Seed)
		}
		shown := lifeShow(t, cli, seed)
		if again.Noted == nil || len(shown.Notes) != 1 || shown.Notes[0].ID != again.Noted.ID || shown.Notes[0].Body != "second receipt" || shown.Notes[0].AuthorSession != "watcher" {
			t.Fatalf("second close's note: %+v", shown.Notes)
		}
	})
}

func TestHarvestOnMergeKeepsTheClosersReason(t *testing.T) {
	github := newHarvestGitHub(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "shipper")
		for _, alreadyMerged := range []bool{false, true} {
			url := github.open(71, "Ship the change")
			if alreadyMerged {
				url = github.merge(71, "Ship the change")
			}
			seed := plantSeedAs(t, cli, "shipper", "keep the receipt")
			_, err := cli.SeedTransition("shipper", seed, "harvest", "verified the change", "", false,
				client.SeedTransitionOptions{WhenMerged: true, PullRequestURL: url})
			if err != nil {
				t.Fatalf("arm with a reason: %v", err)
			}
			github.merge(71, "Ship the change")
			w.advance(protocol.HeatHotInterval)
			before := lifeShow(t, cli, seed).Seed
			if before.Status != "harvested" || protocol.Deref(before.Reason) != "verified the change" {
				t.Fatalf("merge discarded the reason: %+v", before)
			}
			if again := harvestArm(t, cli, "shipper", seed, url); again.Seed.Rev != before.Rev {
				t.Fatalf("arming a harvested seed rewrote it: %+v", again)
			}
		}
	})
}

func harvestArmAs(t *testing.T, cli *client.Client, session, member, seedID, url string) *protocol.SeedTransitionResult {
	t.Helper()
	armed, err := cli.SeedTransition(protocol.SessionID(session), seedID, "harvest", "", member, false, client.SeedTransitionOptions{WhenMerged: true, PullRequestURL: url})
	if err != nil {
		t.Fatalf("arm %s on %s: %v", seedID, url, err)
	}
	return armed
}

func harvestSessionPullRequests(t *testing.T, cli *client.Client, session string) []string {
	t.Helper()
	sessions, err := cli.Query("")
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, s := range sessions {
		if string(s.ID) != session {
			continue
		}
		for _, pull := range s.PullRequests {
			urls = append(urls, pull.URL)
		}
	}
	return urls
}

type harvestPull struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
}

type harvestGitHub struct {
	mu      sync.Mutex
	pulls   map[int]harvestPull
	failing map[int]bool
}

func newHarvestGitHub(t *testing.T) *harvestGitHub {
	t.Helper()
	g := &harvestGitHub{pulls: map[int]harvestPull{}, failing: map[int]bool{}}
	server := httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(server.Close)
	t.Setenv("ATTN_MOCK_GH_URL", server.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	t.Setenv("ATTN_MOCK_GH_HOST", "github.test")
	return g
}

func (g *harvestGitHub) serve(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Connection", "close")
	rw.Header().Set("Content-Type", "application/json")
	rest, found := strings.CutPrefix(r.URL.Path, "/repos/acme/shop/pulls/")
	number, reviews := strings.CutSuffix(rest, "/reviews")
	n, err := strconv.Atoi(number)
	g.mu.Lock()
	pull, known := g.pulls[n]
	failing := g.failing[n]
	g.mu.Unlock()
	switch {
	case !found || err != nil || !known:
		rw.WriteHeader(http.StatusNotFound)
		_, _ = rw.Write([]byte(`{"message":"Not Found"}`))
	case failing:
		rw.WriteHeader(http.StatusBadGateway)
		_, _ = rw.Write([]byte(`{"message":"GitHub is unavailable"}`))
	case reviews:
		_, _ = rw.Write([]byte(`[]`))
	default:
		_ = json.NewEncoder(rw).Encode(pull)
	}
}

func (g *harvestGitHub) set(pull harvestPull) string {
	pull.HTMLURL = "https://github.test/acme/shop/pull/" + strconv.Itoa(pull.Number)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pulls[pull.Number] = pull
	return pull.HTMLURL
}

func (g *harvestGitHub) open(number int, title string) string {
	return g.set(harvestPull{Number: number, Title: title, State: "open"})
}

func (g *harvestGitHub) merge(number int, title string) string {
	return g.set(harvestPull{Number: number, Title: title, State: "closed", Merged: true})
}

func (g *harvestGitHub) close(number int, title string) string {
	return g.set(harvestPull{Number: number, Title: title, State: "closed"})
}

func (g *harvestGitHub) fail(number int, failing bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failing[number] = failing
}

func TestPlotsRefuseClosingBeforeAnyChildMoves(t *testing.T) {
	github := newHarvestGitHub(t)
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "owner")
		plot := gardenNudgePlant(t, cli, "owner", "Finish the children first", "")
		first := gardenNudgePlant(t, cli, "owner", "First open child", plot)
		second := gardenNudgePlant(t, cli, "owner", "Second open child", plot)
		lifeMove(t, cli, "owner", second, "park", "", "")
		before := lifeShow(t, cli, plot).Seed
		assertRefusal := func(err error) {
			t.Helper()
			if err == nil {
				t.Fatal("closed a plot over open children")
			}
			for _, want := range []string{first, second, "attn seed replant " + plot} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("plot refusal %q lacks %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "\n") {
				t.Errorf("multiline plot refusal: %v", err)
			}
			if got := lifeShow(t, cli, plot).Seed; got.Rev != before.Rev || got.Status != before.Status {
				t.Fatalf("refusal changed plot: %+v", got)
			}
		}
		for _, verb := range []string{"harvest", "wither"} {
			for _, force := range []bool{false, true} {
				_, err := cli.SeedTransition("owner", plot, verb, "my part is done", "", force, client.SeedTransitionOptions{})
				assertRefusal(err)
				result := testworld.Request(app, protocol.SeedTransitionMessage{Cmd: protocol.CmdSeedTransition, RequestID: protocol.Ptr(verb + strconv.FormatBool(force)), SourceSessionID: protocol.Ptr(protocol.SessionID("owner")), SeedID: plot, Verb: verb, Reason: protocol.Ptr("my part is done"), Force: protocol.Ptr(force)}, protocol.EventSeedTransitionResult, func(m protocol.SeedTransitionResultMessage) bool {
					return m.RequestID == verb+strconv.FormatBool(force)
				})
				if result.Success {
					t.Fatal("the app closed a plot over open children")
				}
				assertRefusal(fmt.Errorf("%s", protocol.Deref(result.Error)))
			}
		}
		_, err := cli.SeedTransition("owner", plot, "harvest", "verified", "", true, client.SeedTransitionOptions{WhenMerged: true, PullRequestURL: github.open(81, "Close when ready")})
		assertRefusal(err)
		if got := lifeShow(t, cli, plot).Seed; got.HarvestWhen != nil {
			t.Fatalf("refused arming left a condition: %+v", got.HarvestWhen)
		}
		if tracked := harvestSessionPullRequests(t, cli, "owner"); len(tracked) != 0 {
			t.Fatalf("refused arming tracked a pull request: %v", tracked)
		}
	})
}

func TestAMergeLeavesAPlotOpenWhenNewChildrenNeedWork(t *testing.T) {
	github := newHarvestGitHub(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "closer")
		plot := gardenNudgePlant(t, cli, "closer", "armed before the next step", "")
		gardenNudgeMove(t, cli, "closer", plot, "tend")
		harvestArm(t, cli, "closer", plot, github.open(82, "Finish the original work"))
		child := gardenNudgePlant(t, cli, "closer", "new work before merge", plot)
		github.merge(82, "Finish the original work")
		w.advance(protocol.HeatHotInterval)
		shown := lifeShow(t, cli, plot).Seed
		if shown.Status != "dormant" || shown.HarvestWhen != nil {
			t.Fatalf("merge closed the plot or left an impossible condition armed: %+v", shown)
		}
		notes := strings.Join(lifeNoteBodies(t, cli, plot), "\n")
		if !strings.Contains(notes, "Harvest-on-merge cleared:") || !strings.Contains(notes, child+" planted") {
			t.Fatalf("the plot did not explain why it stayed open: %s", notes)
		}
		w.advance(protocol.HeatHotInterval)
		if after := strings.Join(lifeNoteBodies(t, cli, plot), "\n"); after != notes {
			t.Fatalf("later refresh repeated the refusal note: %s", after)
		}
	})
}
