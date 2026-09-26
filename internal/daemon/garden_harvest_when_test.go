package daemon

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
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
	occurrence, err := seedEvents.Occur(
		gardenSeedEventModel, gardenSeedEventVocabulary.HarvestWhenConfigured, seed.ID,
		seedEvents.HarvestWhenPayload{PullRequestID: condition.PullRequest},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.writeSeedWithEvents(*schema, seed, doc.Rev, occurrence); err != nil {
		t.Fatalf("write %s: %v", seedID, err)
	}
}

func TestArmedSeedsAreReadPastTheSnapshotPage(t *testing.T) {
	d := newGardenDaemon(t)
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seeds collection: %v", err)
	}
	for i := 0; i < gardenSnapshotLimit+1; i++ {
		id := fmt.Sprintf("s-%06d", i)
		pr := "github.com:victorarias/attn#275"
		if i == gardenSnapshotLimit {
			pr = "github.com:victorarias/attn#276"
		}
		seed := garden.Seed{
			ID: id, Title: id, StepSlug: id, Status: garden.StatusPlanted,
			StateChangedAt: "2026-09-12T00:00:00Z", Edges: []garden.Edge{}, Vars: []garden.Var{},
			HarvestWhen: &garden.HarvestCondition{PullRequest: pr},
		}
		body, encodeErr := seed.Encode()
		if encodeErr != nil {
			t.Fatalf("encode %s: %v", id, encodeErr)
		}
		if _, putErr := d.store.PutDocument(*schema, id, body, time.Now(), nil); putErr != nil {
			t.Fatalf("put %s: %v", id, putErr)
		}
	}

	seeds, err := d.armedSeeds()
	if err != nil {
		t.Fatalf("armed seeds: %v", err)
	}
	if len(seeds) != gardenSnapshotLimit+1 {
		t.Fatalf("armed seeds returned %d, want %d", len(seeds), gardenSnapshotLimit+1)
	}
	if got := seeds[len(seeds)-1].ID; got != fmt.Sprintf("s-%06d", gardenSnapshotLimit) {
		t.Fatalf("last armed seed is %s", got)
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

func TestAMergeThatLandsDuringArmingStillHarvests(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "raced by the refresh"})
	rec := recordPullRequest(t, d, "sess-a", "https://github.com/victorarias/attn/pull/71")
	if resp := armWhenMerged(t, d, "sess-a", seed.ID, rec.URL); !resp.Ok {
		t.Fatalf("arm: %v", protocol.Deref(resp.Error))
	}
	armed, doc, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatalf("read %s: %v", seed.ID, err)
	}
	settlePullRequest(t, d, rec.PRID, sessionPullRequestMerged, "landed between the check and the commit")

	harvested, _, err := d.settleFreshlyArmed(armed, doc, "sess-a")
	if err != nil {
		t.Fatalf("settle after arming: %v", err)
	}
	if harvested.Status != garden.StatusHarvested || harvested.HarvestWhen != nil {
		t.Fatalf("the merge that landed during arming was missed: %+v", harvested)
	}
}

func TestAClosureDuringArmingDoesNotRingItsInitiator(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "closed during arming"})
	watchSeed(t, d, "sess-a", seed.ID, false)
	rec := recordPullRequest(t, d, "sess-a", "https://github.com/victorarias/attn/pull/71")
	if resp := armWhenMerged(t, d, "sess-a", seed.ID, rec.URL); !resp.Ok {
		t.Fatalf("arm: %v", protocol.Deref(resp.Error))
	}
	armed, doc, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	settlePullRequest(t, d, rec.PRID, sessionPullRequestClosed, "closed between the check and the commit")

	cleared, _, err := d.settleFreshlyArmed(armed, doc, "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	if cleared.HarvestWhen != nil {
		t.Fatalf("closed pull request left harvest condition %+v", cleared.HarvestWhen)
	}
	if queued := queuedSeedBells(t, d, "sess-a"); len(queued) != 0 {
		t.Fatalf("immediate clear rang its initiating session: %q", queued)
	}
}

func TestAnEditBetweenTheMoveReadAndWriteIsRetried(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "edited mid-write"})
	rec := recordPullRequest(t, d, "sess-a", "https://github.com/victorarias/attn/pull/71")
	if resp := armWhenMerged(t, d, "sess-a", seed.ID, rec.URL); !resp.Ok {
		t.Fatalf("arm: %v", protocol.Deref(resp.Error))
	}
	observed, err := d.armedSeeds()
	if err != nil || len(observed) != 1 {
		t.Fatalf("armed seeds = %+v, %v", observed, err)
	}
	settlePullRequest(t, d, rec.PRID, sessionPullRequestMerged, "merged")
	merged, _ := d.store.SessionPullRequestByID(rec.PRID)

	crossings := 0
	d.beforeSeedMoveWrite = func(seedID string) {
		if crossings > 0 {
			return
		}
		crossings++
		if _, _, err := d.applySeedBodyEdit(seedID, "edited between the read and the write"); err != nil {
			t.Fatalf("edit: %v", err)
		}
	}
	harvested, _, err := d.fulfilHarvestWhen(observed[0], merged, observed[0].HarvestWhen)
	if err != nil {
		t.Fatalf("a write-time conflict on the same condition stopped the harvest: %v", err)
	}
	if crossings != 1 || harvested.Status != garden.StatusHarvested || harvested.Body != "edited between the read and the write" {
		t.Fatalf("crossings=%d, settlement left %+v", crossings, harvested)
	}
}
