package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
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

func TestSettle_ARefreshOnlySweepsWhenSomethingMoved(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "merged while attn was down"})
	recordSettlePR(t, d, "sess-a")
	setPRState(t, d, "merged", "Harvest on merge")
	armOnSettlePR(t, d, seed.ID)

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
