package daemon

import (
	"net"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func armSeed(t *testing.T, d *Daemon, seedID string, condition garden.HarvestCondition) {
	t.Helper()
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seeds collection: %v", err)
	}
	seed, doc, err := d.readSeed(seedID)
	if err != nil {
		t.Fatalf("read %s: %v", seedID, err)
	}
	seed.HarvestWhen = &condition
	if _, err := d.writeSeed(*schema, seed, doc.Rev, FactGardenHarvestWhenChanged); err != nil {
		t.Fatalf("write %s: %v", seedID, err)
	}
}

func TestArmedSeedsAreFoundByTheirPullRequest(t *testing.T) {
	d := newGardenDaemon(t)
	armed := plant(t, d, protocol.SeedPlantMessage{Title: "ship the protocol"})
	other := plant(t, d, protocol.SeedPlantMessage{Title: "ship the daemon"})
	plant(t, d, protocol.SeedPlantMessage{Title: "nothing to wait on"})

	armSeed(t, d, armed.ID, garden.HarvestCondition{
		PullRequest: "github.com:victorarias/attn#113",
		URL:         "https://github.com/victorarias/attn/pull/113",
		SetAt:       "2026-09-02T00:21:00Z",
	})
	armSeed(t, d, other.ID, garden.HarvestCondition{
		PullRequest: "github.com:victorarias/attn#114",
		URL:         "https://github.com/victorarias/attn/pull/114",
		SetAt:       "2026-09-02T00:22:00Z",
	})

	ids := func(t *testing.T, filters []docstore.Filter) []string {
		t.Helper()
		read, _, err := d.runDocQuery(docstore.Query{
			Namespace:  garden.Namespace,
			Collection: garden.CollectionSeeds,
			Filters:    filters,
		})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		out := make([]string, 0, len(read.Documents))
		for _, doc := range read.Documents {
			out = append(out, doc.ID)
		}
		return out
	}

	one := ids(t, []docstore.Filter{{Field: "harvest_when_pull_request", Op: docstore.OpEq, Value: "github.com:victorarias/attn#113"}})
	if len(one) != 1 || one[0] != armed.ID {
		t.Fatalf("a query for one pull request returned %v, want just %s", one, armed.ID)
	}

	all := ids(t, []docstore.Filter{{Field: "harvest_when_pull_request", Op: docstore.OpGt, Value: ""}})
	if len(all) != 2 {
		t.Fatalf("a query for every armed seed returned %v, want the two armed ones", all)
	}
}

func TestSeedShowCarriesTheHarvestCondition(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "ship the protocol"})
	if before := show(t, d, seed.ID); before.Seed.HarvestWhen != nil {
		t.Fatalf("an unarmed seed carries a condition on the wire: %+v", before.Seed.HarvestWhen)
	}

	armSeed(t, d, seed.ID, garden.HarvestCondition{
		PullRequest:  "github.com:victorarias/attn#113",
		URL:          "https://github.com/victorarias/attn/pull/113",
		SetAt:        "2026-09-02T00:21:00Z",
		SetBySession: "sess-a",
		SetByMember:  "trellis",
	})

	got := show(t, d, seed.ID).Seed.HarvestWhen
	if got == nil {
		t.Fatalf("the wire lost the harvest condition")
	}
	if got.PullRequest != "github.com:victorarias/attn#113" || got.URL != "https://github.com/victorarias/attn/pull/113" {
		t.Fatalf("the wire changed the pull request: %+v", got)
	}
	if protocol.Deref(got.SetBySession) != "sess-a" || protocol.Deref(got.SetByMember) != "trellis" {
		t.Fatalf("the wire lost who armed the seed: %+v", got)
	}
	if got.SetAt != "2026-09-02T00:21:00Z" {
		t.Fatalf("the wire changed when it was armed: %+v", got)
	}
}

func armWhenMerged(t *testing.T, d *Daemon, session, seedID, url string) protocol.Response {
	t.Helper()
	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seedID, Verb: string(garden.VerbHarvest),
		WhenMerged: &protocol.SeedHarvestWhenMerged{},
	}
	if url != "" {
		msg.WhenMerged.PullRequestURL = protocol.Ptr(url)
	}
	if session != "" {
		msg.SourceSessionID = protocol.Ptr(session)
	}
	msg.Member = protocol.Ptr("trellis")
	return gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
}

func disarm(t *testing.T, d *Daemon, session, seedID string) protocol.Response {
	t.Helper()
	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seedID, Verb: string(garden.VerbHarvest),
		ClearHarvestWhen: protocol.Ptr(true),
	}
	if session != "" {
		msg.SourceSessionID = protocol.Ptr(session)
	}
	return gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
}

func recordPullRequest(t *testing.T, d *Daemon, session, url string) store.SessionPullRequestRecord {
	t.Helper()
	rec, err := d.sessionPullRequestIdentity(session, url)
	if err != nil {
		t.Fatalf("identify %s: %v", url, err)
	}
	if err := d.recordSessionPullRequest(rec); err != nil {
		t.Fatalf("record %s: %v", url, err)
	}
	return rec
}

func settlePullRequest(t *testing.T, d *Daemon, prID, state, title string) {
	t.Helper()
	err := d.store.UpdateSessionPullRequestStatus(prID, store.SessionPullRequestStatus{
		Title: title, State: state,
	}, time.Now())
	if err != nil {
		t.Fatalf("settle %s as %s: %v", prID, state, err)
	}
}

func seedLog(t *testing.T, d *Daemon, seedID string) []protocol.SeedNote {
	t.Helper()
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedNotes(c, &protocol.SeedNotesMessage{Cmd: protocol.CmdSeedNotes, SeedID: seedID})
	})
	if !resp.Ok {
		t.Fatalf("notes of %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedNotesResult.Notes
}

func logCarries(t *testing.T, d *Daemon, seedID, body string) bool {
	t.Helper()
	for _, note := range seedLog(t, d, seedID) {
		if note.Body == body {
			return true
		}
	}
	return false
}

func TestArmingWithAUrlRecordsThePullRequestAndParksTheSeed(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "ship the daemon"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")

	resp := armWhenMerged(t, d, "sess-a", seed.ID, "https://github.com/victorarias/attn/pull/71")
	if !resp.Ok {
		t.Fatalf("arm: %v", protocol.Deref(resp.Error))
	}
	armed := resp.SeedTransitionResult.Seed
	if armed.Status != garden.StatusDormant {
		t.Fatalf("arming a growing seed left it %q, want the claim released", armed.Status)
	}
	if armed.TenderSession != "" || armed.TenderMember != "" {
		t.Fatalf("an armed seed is still held: %+v", armed)
	}
	if armed.HarvestWhen == nil {
		t.Fatalf("the seed came back without the condition it now waits on")
	}
	if armed.HarvestWhen.PullRequest != "github.com:victorarias/attn#71" {
		t.Fatalf("condition waits on %q", armed.HarvestWhen.PullRequest)
	}
	if armed.HarvestWhen.URL != "https://github.com/victorarias/attn/pull/71" ||
		protocol.Deref(armed.HarvestWhen.SetByMember) != "trellis" ||
		armed.HarvestWhen.SetAt == "" {
		t.Fatalf("the condition does not say who armed it and when: %+v", armed.HarvestWhen)
	}

	records := d.store.ListSessionPullRequests("sess-a")
	if len(records) != 1 || records[0].PRID != "github.com:victorarias/attn#71" {
		t.Fatalf("arming did not record the pull request on the session: %+v", records)
	}

	if !logCarries(t, d, seed.ID, "harvests when victorarias/attn#71 merges") {
		t.Fatalf("the log does not say what the seed waits on: %+v", seedLog(t, d, seed.ID))
	}
	references := show(t, d, seed.ID).References
	if len(references) != 1 || references[0].URL == nil ||
		*references[0].URL != "https://github.com/victorarias/attn/pull/71" {
		t.Fatalf("the pull request is not a reference on the seed: %+v", references)
	}
}

func TestArmingASeedThatIsNotGrowingLeavesItsStateAlone(t *testing.T) {
	d := newGardenDaemon(t)
	for _, tc := range []struct {
		name  string
		park  bool
		state string
	}{
		{name: "planted", state: garden.StatusPlanted},
		{name: "dormant", park: true, state: garden.StatusDormant},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "wait on " + tc.name})
			if tc.park {
				move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")
				move(t, d, "sess-a", seed.ID, garden.VerbPark, "", "trellis")
			}
			resp := armWhenMerged(t, d, "sess-a", seed.ID, "https://github.com/victorarias/attn/pull/71")
			if !resp.Ok {
				t.Fatalf("arm: %v", protocol.Deref(resp.Error))
			}
			armed := resp.SeedTransitionResult.Seed
			if armed.Status != tc.state {
				t.Fatalf("arming moved a %s seed to %q", tc.name, armed.Status)
			}
			if armed.HarvestWhen == nil {
				t.Fatalf("a %s seed came back unarmed", tc.name)
			}
		})
	}
}

func TestArmingInfersTheSessionsOnlyOpenPullRequest(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "infer it"})
	closed := recordPullRequest(t, d, "sess-a", "https://github.com/victorarias/attn/pull/70")
	settlePullRequest(t, d, closed.PRID, sessionPullRequestClosed, "an old one")
	recordPullRequest(t, d, "sess-a", "https://github.com/victorarias/attn/pull/71")

	resp := armWhenMerged(t, d, "sess-a", seed.ID, "")
	if !resp.Ok {
		t.Fatalf("arm: %v", protocol.Deref(resp.Error))
	}
	if got := resp.SeedTransitionResult.Seed.HarvestWhen; got == nil || got.PullRequest != "github.com:victorarias/attn#71" {
		t.Fatalf("inference picked %+v, want the one still open", got)
	}
}

func TestArmingWithoutOneOpenPullRequestSaysHowToNameIt(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "name it"})

	none := armWhenMerged(t, d, "sess-a", seed.ID, "")
	if none.Ok {
		t.Fatalf("a session with no pull request armed the seed anyway")
	}
	if got := protocol.Deref(none.Error); !strings.Contains(got, "has no open pull request") ||
		!strings.Contains(got, "--when-merged <pr-url>") {
		t.Fatalf("the refusal does not say what to do: %q", got)
	}

	recordPullRequest(t, d, "sess-a", "https://github.com/victorarias/attn/pull/71")
	recordPullRequest(t, d, "sess-a", "https://github.com/victorarias/attn/pull/72")
	many := armWhenMerged(t, d, "sess-a", seed.ID, "")
	if many.Ok {
		t.Fatalf("two open pull requests and the daemon picked one")
	}
	got := protocol.Deref(many.Error)
	for _, want := range []string{
		"has 2 open pull requests",
		"https://github.com/victorarias/attn/pull/71",
		"https://github.com/victorarias/attn/pull/72",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the refusal does not name %q: %q", want, got)
		}
	}
}

func TestArmingWithoutASessionSaysSo(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "no session"})

	resp := armWhenMerged(t, d, "", seed.ID, "https://github.com/victorarias/attn/pull/71")
	if resp.Ok {
		t.Fatalf("arming without a session was accepted")
	}
	if got := protocol.Deref(resp.Error); !strings.Contains(got, "needs a session to track the pull request") {
		t.Fatalf("the refusal reads %q", got)
	}
}

func TestArmingAMergedPullRequestHarvestsRightAway(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "already landed"})
	rec := recordPullRequest(t, d, "sess-a", "https://github.com/victorarias/attn/pull/71")
	settlePullRequest(t, d, rec.PRID, sessionPullRequestMerged, "Carry a harvest condition on the seed")

	resp := armWhenMerged(t, d, "sess-a", seed.ID, "https://github.com/victorarias/attn/pull/71")
	if !resp.Ok {
		t.Fatalf("arm: %v", protocol.Deref(resp.Error))
	}
	harvested := resp.SeedTransitionResult.Seed
	if harvested.Status != garden.StatusHarvested {
		t.Fatalf("a merged pull request left the seed %q", harvested.Status)
	}
	if protocol.Deref(harvested.Reason) != "PR #71 merged: Carry a harvest condition on the seed" {
		t.Fatalf("the harvest reason reads %q", protocol.Deref(harvested.Reason))
	}
	if harvested.HarvestWhen != nil {
		t.Fatalf("a harvested seed still waits on something: %+v", harvested.HarvestWhen)
	}
	stored, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatalf("read %s: %v", seed.ID, err)
	}
	if stored.HarvestWhenPullRequest != "" {
		t.Fatalf("the armed-seed query still finds it: %q", stored.HarvestWhenPullRequest)
	}
}

func TestArmingAClosedSeedIsRefused(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "over already"})
	move(t, d, "sess-a", seed.ID, garden.VerbHarvest, "shipped it", "trellis")
	recordPullRequest(t, d, "sess-a", "https://github.com/victorarias/attn/pull/71")

	resp := armWhenMerged(t, d, "sess-a", seed.ID, "")
	if resp.Ok {
		t.Fatalf("a harvested seed was armed")
	}
	if got := protocol.Deref(resp.Error); !strings.Contains(got, "waits on nothing") {
		t.Fatalf("the refusal reads %q", got)
	}
}

func TestArmingRefusesAReasonBecauseTheMergeWritesIt(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "reasoned"})
	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seed.ID, Verb: string(garden.VerbHarvest),
		SourceSessionID: protocol.Ptr("sess-a"), Reason: protocol.Ptr("done enough"),
		WhenMerged: &protocol.SeedHarvestWhenMerged{},
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
	if resp.Ok {
		t.Fatalf("a reason rode along with --when-merged")
	}
	if got := protocol.Deref(resp.Error); !strings.Contains(got, "the merge writes the reason") {
		t.Fatalf("the refusal reads %q", got)
	}
}

func TestClearingASeedThatWaitsOnNothingIsRefused(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "never armed"})

	resp := disarm(t, d, "sess-a", seed.ID)
	if resp.Ok {
		t.Fatalf("clearing an unarmed seed was accepted")
	}
	if got := protocol.Deref(resp.Error); !strings.Contains(got, "has no harvest condition") {
		t.Fatalf("the refusal reads %q", got)
	}
}

func TestClearingDropsTheConditionAndSaysSoOnTheLog(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "changed my mind"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")
	if resp := armWhenMerged(t, d, "sess-a", seed.ID, "https://github.com/victorarias/attn/pull/71"); !resp.Ok {
		t.Fatalf("arm: %v", protocol.Deref(resp.Error))
	}

	resp := disarm(t, d, "sess-a", seed.ID)
	if !resp.Ok {
		t.Fatalf("clear: %v", protocol.Deref(resp.Error))
	}
	cleared := resp.SeedTransitionResult.Seed
	if cleared.HarvestWhen != nil {
		t.Fatalf("the condition survived the clear: %+v", cleared.HarvestWhen)
	}
	if cleared.Status != garden.StatusDormant {
		t.Fatalf("clearing moved the seed to %q; disarming is not a move", cleared.Status)
	}
	if !logCarries(t, d, seed.ID, harvestWhenClearedNote) {
		t.Fatalf("the log does not say the seed stopped waiting: %+v", seedLog(t, d, seed.ID))
	}
	stored, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatalf("read %s: %v", seed.ID, err)
	}
	if stored.HarvestWhenPullRequest != "" {
		t.Fatalf("the armed-seed query still finds a cleared seed: %q", stored.HarvestWhenPullRequest)
	}
}

func TestAMergedPullRequestHarvestsAnArmedSeedAsAttn(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "waiting on the merge"})
	rec := recordPullRequest(t, d, "sess-a", "https://github.com/victorarias/attn/pull/71")
	if resp := armWhenMerged(t, d, "sess-a", seed.ID, rec.URL); !resp.Ok {
		t.Fatalf("arm: %v", protocol.Deref(resp.Error))
	}
	settlePullRequest(t, d, rec.PRID, sessionPullRequestMerged, "Arm harvest-on-merge from the transition handler")

	armed, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatalf("read %s: %v", seed.ID, err)
	}
	merged, ok := sessionPullRequestByID(d.store.ListSessionPullRequests("sess-a"), rec.PRID)
	if !ok {
		t.Fatalf("the merged row went missing")
	}
	harvested, _, err := d.fulfilHarvestWhen(armed, merged)
	if err != nil {
		t.Fatalf("fulfil: %v", err)
	}
	if harvested.Status != garden.StatusHarvested || harvested.HarvestWhen != nil {
		t.Fatalf("fulfilment left the seed %+v", harvested)
	}
	if harvested.Reason != "PR #71 merged: Arm harvest-on-merge from the transition handler" {
		t.Fatalf("the harvest reason reads %q", harvested.Reason)
	}
}

func TestTheDaemonsHarvestReasonFitsTheSeedsLimit(t *testing.T) {
	reason := harvestWhenMergedReason(store.SessionPullRequestRecord{
		Number: 71, Title: strings.Repeat("a very long pull request title ", 40),
	})
	if n := utf8.RuneCountInString(reason); n > garden.MaxReasonChars || n < garden.MaxReasonChars-2 {
		t.Fatalf("a long title produced a %d character reason, want it trimmed to fit %d", n, garden.MaxReasonChars)
	}
	if !strings.HasPrefix(reason, "PR #71 merged: ") || !strings.HasSuffix(reason, "…") {
		t.Fatalf("the trimmed reason reads %q", reason)
	}
}

func TestArmingASeedSomebodyElseHoldsRefusesAndForceRecordsIt(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "held elsewhere"})
	move(t, d, "sess-b", seed.ID, garden.VerbTend, "", "")

	refused := armWhenMerged(t, d, "sess-a", seed.ID, "https://github.com/victorarias/attn/pull/71")
	if refused.Ok {
		t.Fatalf("arming took the seed from its tender without --force")
	}
	if got := protocol.Deref(refused.Error); !strings.Contains(got, "is being tended by") {
		t.Fatalf("the refusal reads %q", got)
	}

	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seed.ID, Verb: string(garden.VerbHarvest),
		SourceSessionID: protocol.Ptr("sess-a"), Member: protocol.Ptr("trellis"),
		Force: protocol.Ptr(true), WhenMerged: &protocol.SeedHarvestWhenMerged{
			PullRequestURL: protocol.Ptr("https://github.com/victorarias/attn/pull/71"),
		},
	}
	forced := gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
	if !forced.Ok {
		t.Fatalf("forced arm: %v", protocol.Deref(forced.Error))
	}
	if forced.SeedTransitionResult.Seed.Status != garden.StatusDormant {
		t.Fatalf("a forced arm left the seed %q", forced.SeedTransitionResult.Seed.Status)
	}
	audit := forcedSeedMoveBody(seed.ID, garden.VerbPark, garden.Tender{Member: "trellis"}, garden.Tender{Session: "sess-b"})
	if !logCarries(t, d, seed.ID, audit) {
		t.Fatalf("the log does not record the takeover: %+v", seedLog(t, d, seed.ID))
	}
}

func TestTheLinesASeedGetsAboutItsPullRequest(t *testing.T) {
	rec := store.SessionPullRequestRecord{
		Repository: "github.com/victorarias/attn", Number: 71,
		URL: "https://github.com/victorarias/attn/pull/71",
	}
	if got := harvestWhenArmedNote(rec); got != "harvests when victorarias/attn#71 merges" {
		t.Fatalf("the armed line reads %q", got)
	}
	if got := harvestWhenClosedNote(rec); got != "PR #71 closed without merging; harvest-on-merge cleared" {
		t.Fatalf("the closed line reads %q", got)
	}
}
