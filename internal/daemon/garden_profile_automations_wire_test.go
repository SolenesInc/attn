package daemon_test

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestDeletingAProfileStartsAFreshAutomationThread(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		home := app.SelectedProfile()
		side := createProfile(app, "Side")
		selectProfile(app, side.ID)
		folder := w.Path("sweep")
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		applied := testworld.Request(app, protocol.AutomationApplyMessage{Cmd: protocol.CmdAutomationApply, RequestID: protocol.Ptr(id), ProfileID: protocol.Ptr(side.ID), DefinitionYaml: automationSingletonSpec(w, "Sweep", "latest", "sweep")}, protocol.EventAutomationApplyResult, automationAnswer[protocol.AutomationApplyResultMessage](id))
		if !applied.Success {
			t.Fatalf("apply Side automation: %+v", applied)
		}
		w.advance(2*time.Minute + 30*time.Second)
		runs := automationRuns(t, cli, "sweep")
		if len(runs) != 1 || runs[0].State != "delivered" {
			t.Fatalf("first occurrence: %+v", runs)
		}
		first := runs[0]
		worker, seed := protocol.Deref(first.SessionID), protocol.Deref(first.SeedID)
		lifeMove(t, cli, worker, seed, "harvest", "finished", "")
		if _, err := cli.AgentClose(worker, worker, "finished this automation run"); err != nil {
			t.Fatal(err)
		}
		for _, current := range w.AppOn(side.ID).Initial.Profiles {
			if current.ID == side.ID {
				side = current
			}
		}
		id = uuid.NewString()
		deleted := profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision, DestinationProfileID: home}, id)
		if !deleted.Success {
			t.Fatalf("delete completed automation profile: %+v", deleted)
		}
		w.advance(2 * time.Minute)
		runs = automationRuns(t, cli, "sweep")
		if len(runs) != 2 || runs[0].State != "delivered" || protocol.Deref(runs[0].SeedID) == seed || protocol.Deref(runs[0].SessionID) == worker {
			t.Fatalf("next event must start fresh destination work: %+v", runs)
		}
		fresh, err := cli.WithGardenProfile(home, "").SeedShow("", protocol.Deref(runs[0].SeedID))
		if err != nil || fresh.Seed.ProfileID != home {
			t.Fatalf("new automation seed owner: %+v %v", fresh, err)
		}
		archived, err := cli.WithGardenProfile(home, "").SeedShow("", seed)
		if err != nil || archived.Seed.ProfileID != side.ID || archived.Seed.Status != "harvested" {
			t.Fatalf("original automation seed stays archived: %+v %v", archived, err)
		}
	})
}

func TestDeletingAProfileCancelsItsPendingAutomationRuns(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "false")
	r := newAutomationReviewWorld(t)
	home := r.app.SelectedProfile()
	side := createProfile(r.app, "Side")
	selectProfile(r.app, side.ID)
	id := uuid.NewString()
	applied := testworld.Request(r.app, protocol.AutomationApplyMessage{
		Cmd: protocol.CmdAutomationApply, RequestID: protocol.Ptr(id), ProfileID: protocol.Ptr(side.ID),
		DefinitionYaml: automationReviewSpec("held-review", "manual", automationReviewOverride(r.clone)),
	}, protocol.EventAutomationApplyResult, automationAnswer[protocol.AutomationApplyResultMessage](id))
	if !applied.Success {
		t.Fatalf("apply Side automation: %+v", applied)
	}
	upstream := newRepo(t, "upstream")
	unfetched := commitFile(t, upstream, "later.go", "package later\n")
	if _, err := r.cli.AutomationRun("held-review", "held", automationReviewInput(46, unfetched)); err == nil {
		t.Fatal("a run whose head cannot be fetched started")
	}
	held := automationRuns(t, r.cli, "held-review")
	if len(held) != 1 || held[0].State != "pending" {
		t.Fatalf("run must be held pending: %+v", held)
	}
	lifeMove(t, r.cli.WithGardenProfile(side.ID, ""), "", protocol.Deref(held[0].SeedID), "wither", "ending this profile's work", "")
	id = uuid.NewString()
	deleted := profileRequest(r.app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision, DestinationProfileID: home}, id)
	if !deleted.Success {
		t.Fatalf("delete profile with a held run: %+v", deleted)
	}
	runs := automationRuns(t, r.cli, "held-review")
	if len(runs) != 1 || runs[0].ID != held[0].ID || runs[0].State != "cancelled" || protocol.Deref(runs[0].CancelReason) != "profile_deleted" {
		t.Fatalf("source occurrence must stay cancelled: %+v", runs)
	}
}
