package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestACrewClaimOutlivesItsSessionThroughANap(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	first := wakeCrew(t, cli, "keel", "").SessionID
	w.Launched(string(first))
	seed := plantSeedAs(t, cli, string(first), "Keep the claim across sessions")
	if _, err := cli.SeedTransition(first, seed, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	handed := crewHandoff(t, cli, string(first), "Continue the same work.", false, protocol.CrewDayCloseNap)
	next := protocol.Deref(handed.SessionID)
	w.Launched(string(next)).Prompted()
	shown := lifeShow(t, cli, seed).Seed
	if !shown.Claimed || shown.Tender == nil || shown.Tender.Ref != "member:keel" || protocol.Deref(shown.Tender.SessionID) != next {
		t.Fatalf("after the nap the seed shows %+v, want it claimed by member:keel in session %s", shown.Tender, next)
	}
}

func TestTendingForAnUnknownNameIsRefused(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	seed := plantSeedAs(t, cli, "", "Assign this work")
	before := lifeShow(t, cli, seed).Seed
	_, err := cli.SeedTransition("", seed, "tend", "", false, client.SeedTransitionOptions{Assignee: "bob"})
	lifeRefusal(t, "an unknown assignee", err, "bob", "no crew member")
	_, err = cli.SeedTransition("", seed, "tend", "", false, client.SeedTransitionOptions{})
	lifeRefusal(t, "a user claim without an assignee", err, "--for")
	if after := lifeShow(t, cli, seed).Seed; after.Rev != before.Rev || after.Claimed || after.Tender != nil {
		t.Fatalf("the refusals changed the seed: %+v", after)
	}
	day := wakeCrew(t, cli, "Keel", "").SessionID
	w.Launched(string(day)).Prompted()
	assigned, err := cli.SeedTransition("", seed, "tend", "", false, client.SeedTransitionOptions{Assignee: "Keel"})
	if err != nil {
		t.Fatal(err)
	}
	if !assigned.Seed.Claimed || assigned.Seed.Tender == nil || assigned.Seed.Tender.Ref != "member:keel" || assigned.Seed.Tender.Name != "Keel" {
		t.Fatalf("assigned: %+v", assigned.Seed.Tender)
	}
	if items := readInbox(t, cli, string(day), 0).Items; len(items) != 1 || protocol.Deref(items[0].Hint) != "tended" {
		t.Fatalf("the member was not told it now tends the seed: %+v", items)
	}
}

func TestAMembersNextSessionClosesTheDelegateItsLastSessionDispatched(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
	cli := w.Client()
	first := wakeCrew(t, cli, "keel", "").SessionID
	w.Launched(string(first))
	seed := plantSeedAs(t, cli, string(first), "Pass this work to a delegate")
	if _, err := cli.SeedTransition(first, seed, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	delegated, err := cli.Delegate(seedHandoverRequest(string(first), crewRosterMember(t, cli, "keel").HomeDir, seed, "Continue from the seed."))
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(string(delegated.SessionID))
	handed := crewHandoff(t, cli, string(first), "My delegate is still working.", false, protocol.CrewDayCloseNap)
	next := protocol.Deref(handed.SessionID)
	w.Launched(string(next))
	closed, err := cli.AgentClose(string(delegated.SessionID), next, "The assigned work is complete.")
	if err != nil || closed.Rule != protocol.AgentCloseRuleDispatcher {
		t.Fatalf("the member's next session could not close its delegate as its dispatcher: %+v %v", closed, err)
	}
}

func TestSessionCloseAndReopenRefreshItsGardenClaim(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	id := w.Spawn(app, fakeagent.Codex, w.Path("worker"))
	run := w.Launched(id)
	takeTurn(app, run, id)
	seed := plantSeedAs(t, cli, id, "Refresh the claim with its session")
	lifeMove(t, cli, id, seed, "tend", "", "")
	awaitClaim := func(claimed bool) {
		t.Helper()
		testworld.Await(app, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
			for _, s := range m.Seeds {
				if s.ID == seed && s.Claimed == claimed && s.Ready != claimed && s.Tender != nil && s.Tender.Ref == protocol.PartyRef("session:"+id) {
					return (protocol.Deref(s.Tender.SessionID) == protocol.SessionID(id)) == claimed
				}
			}
			return false
		})
	}
	awaitClaim(true)
	if _, err := cli.AgentClose(id, protocol.SessionID(id), "Pause the conversation."); err != nil {
		t.Fatal(err)
	}
	awaitClaim(false)
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(id), Action: string(protocol.SessionReopenActionReopen)}); err != nil {
		t.Fatal(err)
	}
	w.Launched(id)
	awaitClaim(true)
}

func TestAnExitedTenderKeepsSeedBellsUntilItsAgentResumes(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		run := w.bubbleClaude(t, app, "worker")
		id := run.id
		run.reply("Ready. <!-- attn:state=idle -->")
		registerSessions(t, w, cli, "sender")
		tended := plantSeedAs(t, cli, id, "Keep the tender informed")
		lifeMove(t, cli, id, tended, "tend", "", "")
		watched := plantSeedAs(t, cli, "sender", "Keep the watcher informed")
		gardenNudgeWatch(t, cli, id, watched, false)
		app.TypeLine(id, "work before the update")
		w.advance(0)
		if session := queriedSession(t, cli, id); session.State != protocol.SessionStateWorking {
			t.Fatalf("before exit: %+v", session)
		}
		gardenNudgeNote(t, cli, "sender", tended, "Before the agent exits.", true)
		run.term.Exit(143)
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == id })
		if shown := lifeShow(t, cli, tended).Seed; shown.Claimed || !shown.Ready {
			t.Fatalf("exited tender still holds the claim: %+v", shown)
		}
		gardenNudgeNote(t, cli, "sender", watched, "After the agent exits.", true)
		resumed := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(id), Cols: 100, Rows: 30},
			protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == id })
		if !resumed.Success {
			t.Fatal(protocol.Deref(resumed.Error))
		}
		resumedRun := w.bootBubbleClaude(t, id)
		app.TypeLine(id, "pick up where we left off")
		resumedRun.reply("Ready again. <!-- attn:state=idle -->")
		w.advance(30 * time.Second)
		if got := resumedRun.prompts(); resumedRun.promptsContaining(inboxDoorbell) != 1 {
			t.Fatalf("resumed agent received %q, want the inbox doorbell", got)
		}
		items := readInbox(t, cli, id, 0).Items
		if len(items) != 2 || !strings.Contains(inboxContents(items), tended+" moved: note.added") || !strings.Contains(inboxContents(items), watched+" moved: note.added") {
			t.Fatalf("resumed inbox lost the tender or watcher bell: %+v", items)
		}
	})
}
