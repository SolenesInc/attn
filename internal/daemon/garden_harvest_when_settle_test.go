package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const (
	settlePRID  = "github.com:victorarias/attn#113"
	settlePRURL = "https://github.com/victorarias/attn/pull/113"
)

func recordSettlePR(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	if _, err := d.store.RecordSessionPullRequest(store.SessionPullRequestRecord{
		SessionID: sessionID, PRID: settlePRID, Repository: "github.com/victorarias/attn",
		Number: 113, URL: settlePRURL,
	}, time.Now()); err != nil {
		t.Fatalf("record %s: %v", settlePRID, err)
	}
}

func setPRState(t *testing.T, d *Daemon, state, title string) {
	t.Helper()
	if err := d.store.UpdateSessionPullRequestStatus(settlePRID, store.SessionPullRequestStatus{
		Title: title, State: state,
	}, time.Now()); err != nil {
		t.Fatalf("set %s to %s: %v", settlePRID, state, err)
	}
}

func armOnSettlePR(t *testing.T, d *Daemon, seedID string) {
	t.Helper()
	armSeed(t, d, seedID, garden.HarvestCondition{
		PullRequest: settlePRID, URL: settlePRURL, SetAt: string(protocol.TimestampNow()),
	})
}

func seedNoteBodies(t *testing.T, d *Daemon, seedID string) []string {
	t.Helper()
	notes, err := d.readNotesDomain(seedID)
	if err != nil {
		t.Fatalf("notes on %s: %v", seedID, err)
	}
	bodies := make([]string, 0, len(notes))
	for _, note := range notes {
		bodies = append(bodies, note.Body)
	}
	return bodies
}

func TestSettle_MergedPullRequestHarvestsTheSeed(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "ship the sweep"})
	untouched := plant(t, d, protocol.SeedPlantMessage{Title: "waits on nothing"})
	recordSettlePR(t, d, "sess-a")
	setPRState(t, d, "merged", "Harvest a seed when its pull request merges")
	armOnSettlePR(t, d, seed.ID)

	if harvested, cleared := d.settleHarvestConditions(); harvested != 1 || cleared != 0 {
		t.Fatalf("settle = (%d harvested, %d cleared), want (1, 0)", harvested, cleared)
	}

	got := show(t, d, seed.ID).Seed
	if got.Status != garden.StatusHarvested {
		t.Fatalf("the armed seed is %q, want harvested", got.Status)
	}
	want := "PR #113 merged: Harvest a seed when its pull request merges"
	if protocol.Deref(got.Reason) != want {
		t.Fatalf("harvest reason = %q, want %q", protocol.Deref(got.Reason), want)
	}
	if got.HarvestWhen != nil {
		t.Fatalf("a harvested seed still waits on %+v", got.HarvestWhen)
	}
	if again, _ := d.settleHarvestConditions(); again != 0 {
		t.Fatalf("the second sweep harvested %d seeds, want none left armed", again)
	}
	if state := show(t, d, untouched.ID).Seed.Status; state != garden.StatusPlanted {
		t.Fatalf("an unarmed seed moved to %q", state)
	}
}

func TestSettle_HarvestOfAHeldSeedRecordsWhoTookIt(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "held while it merges"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")
	recordSettlePR(t, d, "sess-a")
	setPRState(t, d, "merged", "Harvest on merge")
	armOnSettlePR(t, d, seed.ID)

	if harvested, _ := d.settleHarvestConditions(); harvested != 1 {
		t.Fatalf("a seed a live session holds was not harvested")
	}
	bodies := seedNoteBodies(t, d, seed.ID)
	forced := "attn forced `attn seed harvest " + seed.ID + "`; sess-a held the seed."
	found := false
	for _, body := range bodies {
		found = found || body == forced
	}
	if !found {
		t.Fatalf("the log = %q, want the forced move recorded as %q", bodies, forced)
	}
}

func TestSettle_ClosedPullRequestClearsTheConditionAndRings(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "waits on a doomed PR"})
	move(t, d, "sess-a", seed.ID, garden.VerbPark, "", "")
	watchSeed(t, d, "sess-b", seed.ID, false)
	recordSettlePR(t, d, "sess-a")
	setPRState(t, d, "closed", "Harvest on merge")
	armOnSettlePR(t, d, seed.ID)

	if harvested, cleared := d.settleHarvestConditions(); harvested != 0 || cleared != 1 {
		t.Fatalf("settle = (%d harvested, %d cleared), want (0, 1)", harvested, cleared)
	}

	got := show(t, d, seed.ID).Seed
	if got.Status != garden.StatusDormant {
		t.Fatalf("a closed pull request moved the seed to %q; it closes nothing", got.Status)
	}
	if got.HarvestWhen != nil {
		t.Fatalf("the condition survived the close: %+v", got.HarvestWhen)
	}
	bodies := seedNoteBodies(t, d, seed.ID)
	want := "PR #113 closed without merging; harvest-on-merge cleared"
	if len(bodies) != 1 || bodies[0] != want {
		t.Fatalf("the log = %q, want %q", bodies, want)
	}
	assertOneSeedBell(t, d, "sess-b", seed.ID, harvestWhenClearedRing)
}

func TestSettle_ARefreshOnlySweepsWhenSomethingMoved(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "merged while attn was down"})
	recordSettlePR(t, d, "sess-a")
	setPRState(t, d, "merged", "Harvest on merge")
	armOnSettlePR(t, d, seed.ID)

	// A merged row is no longer refreshed, so the tick changes nothing and must not sweep.
	if fetched, changed := d.refreshSessionPullRequests(time.Now()); fetched != 0 || changed != 0 {
		t.Fatalf("refresh = (%d fetched, %d changed), want a tick with nothing to do", fetched, changed)
	}
	if status := show(t, d, seed.ID).Seed.Status; status != garden.StatusPlanted {
		t.Fatalf("a refresh that changed nothing moved the seed to %q", status)
	}

	if harvested, _ := d.settleHarvestConditions(); harvested != 1 {
		t.Fatalf("the sweep at start missed a merge that landed while the daemon was down")
	}
	if status := show(t, d, seed.ID).Seed.Status; status != garden.StatusHarvested {
		t.Fatalf("the seed is %q after the start sweep, want harvested", status)
	}
}

func TestSettle_TheRefreshHarvestsWhenTheMergeLands(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "harvests on the merge"})
	recordSettlePR(t, d, "sess-a")
	armOnSettlePR(t, d, seed.ID)
	serveHost(d, "github.com", &fakePRHost{snapshot: &github.PullRequestSnapshot{
		Number: 113, State: "closed", Merged: true, Title: "Harvest a seed when its pull request merges",
	}})

	if fetched, changed := d.refreshSessionPullRequests(time.Now()); fetched != 1 || changed != 1 {
		t.Fatalf("refresh = (%d fetched, %d changed), want the merge to land", fetched, changed)
	}
	got := show(t, d, seed.ID).Seed
	if got.Status != garden.StatusHarvested || got.HarvestWhen != nil {
		t.Fatalf("the refresh did not settle the seed: %+v", got)
	}
	if !strings.HasPrefix(protocol.Deref(got.Reason), "PR #113 merged: ") {
		t.Fatalf("harvest reason = %q, want the merged pull request and its title", protocol.Deref(got.Reason))
	}
}

func TestSettle_AConditionNobodyTracksStaysArmedAndIsSaidOnce(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "armed on an untracked PR"})
	armOnSettlePR(t, d, seed.ID)

	for range 2 {
		if harvested, cleared := d.settleHarvestConditions(); harvested != 0 || cleared != 0 {
			t.Fatalf("settle = (%d harvested, %d cleared) for a pull request nobody records", harvested, cleared)
		}
	}
	if got := show(t, d, seed.ID).Seed; got.Status != garden.StatusPlanted || got.HarvestWhen == nil {
		t.Fatalf("an untracked condition was not left alone: %+v", got)
	}
	d.harvestWhenMu.Lock()
	defer d.harvestWhenMu.Unlock()
	if !d.harvestWhenUntracked[seed.ID] {
		t.Fatalf("the untracked condition was not remembered, so every tick would say it again")
	}
}

func TestSettle_TheHarvestReasonFitsTheGardensLimit(t *testing.T) {
	reason := harvestWhenReason(store.SessionPullRequestRecord{
		Number: 113, Title: strings.Repeat("long ", 200),
	})
	if n := len([]rune(reason)); n != garden.MaxReasonChars {
		t.Fatalf("a very long title made a %d character reason, want it trimmed to %d", n, garden.MaxReasonChars)
	}
	if !strings.HasPrefix(reason, "PR #113 merged: ") || !strings.HasSuffix(reason, "…") {
		t.Fatalf("the trimmed reason lost its shape: %q", reason)
	}
	if bare := harvestWhenReason(store.SessionPullRequestRecord{Number: 7}); bare != "PR #7 merged" {
		t.Fatalf("a row with no title made %q", bare)
	}
}
