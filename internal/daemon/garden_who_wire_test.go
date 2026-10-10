package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestACrewClaimOutlivesItsSessionThroughNapAndClear(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	first := wakeCrew(t, cli, "keel", "").SessionID
	w.Launched(string(first))
	seed := plantSeedAs(t, cli, string(first), "Keep the claim across conversations")
	if _, err := cli.SeedTransition(first, seed, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	handed := crewHandoff(t, cli, string(first), "Continue the same work.", false, protocol.CrewDayCloseNap)
	next := protocol.Deref(handed.SessionID)
	run := w.Launched(string(next))
	run.Prompted()
	shown := lifeShow(t, cli, seed).Seed
	if !shown.Claimed || shown.Tender == nil || shown.Tender.Ref != "member:keel" || protocol.Deref(shown.Tender.SessionID) != next {
		t.Fatalf("after nap: %+v", shown)
	}
	successor := clearClaude(app, run, string(next))
	shown = lifeShow(t, cli, seed).Seed
	if !shown.Claimed || shown.Tender == nil || shown.Tender.Ref != "member:keel" {
		t.Fatalf("after clear: %+v", shown)
	}
	other := spawnPanes(w, app, w.Path("other"))[0].session
	_, err := cli.SeedTransition(protocol.SessionID(other), seed, "tend", "", false, client.SeedTransitionOptions{})
	lifeRefusal(t, "taking a member claim after clear", err, "Keel", "--force")
	if successor.ID == next {
		t.Fatal("clear did not create a successor")
	}
}

func TestAMembersWatchRingsItsNextSessionAfterANap(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	first := wakeCrew(t, cli, "keel", "").SessionID
	w.Launched(string(first))
	sender := spawnPanes(w, app, w.Path("sender"))[0].session
	plot := plantSeedAs(t, cli, sender, "Watch this tree")
	child, err := cli.SeedPlant(protocol.SessionID(sender), "A child", "", plot, "")
	if err != nil {
		t.Fatal(err)
	}
	gardenNudgeWatch(t, cli, string(first), plot, false)
	gardenNudgeWatch(t, cli, sender, plot, false)
	handed := crewHandoff(t, cli, string(first), "The watched tree continues.", false, protocol.CrewDayCloseNap)
	next := protocol.Deref(handed.SessionID)
	w.Launched(string(next))
	gardenNudgeNote(t, cli, sender, child.Seed.ID, "The child changed.", true)
	items := readInbox(t, cli, string(next), 0).Items
	if len(items) != 1 || items[0].Address != "member:keel" || !strings.Contains(items[0].Content, child.Seed.ID) {
		t.Fatalf("successor inbox: %+v", items)
	}
	gardenNudgeNote(t, cli, sender, child.Seed.ID, "Another child update.", true)
	gardenNudgeWatch(t, cli, string(next), plot, true)
	if items := readInbox(t, cli, string(next), 0).Items; len(items) != 0 {
		t.Fatalf("unwatch left pending bells: %+v", items)
	}
	gardenNudgeNote(t, cli, string(next), child.Seed.ID, "From the member.", true)
	if items := readInbox(t, cli, sender, 0).Items; len(items) != 1 || items[0].Address != protocol.AddressRef("session:"+sender) {
		t.Fatalf("plain watch: %+v", items)
	}
}

func TestTendingForAnUnknownNameIsRefused(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	seed := plantSeedAs(t, cli, "", "Assign this work")
	before := lifeShow(t, cli, seed).Seed
	_, err := cli.SeedTransition("", seed, "tend", "", false, client.SeedTransitionOptions{Assignee: "bob"})
	lifeRefusal(t, "unknown assignment", err, "bob", "no crew member")
	after := lifeShow(t, cli, seed).Seed
	if after.Rev != before.Rev || after.Claimed || after.Tender != nil {
		t.Fatalf("refusal changed seed: %+v", after)
	}
	_, err = cli.SeedTransition("", seed, "tend", "", false, client.SeedTransitionOptions{})
	lifeRefusal(t, "user claim without assignee", err, "--for")
	assigned, err := cli.SeedTransition("", seed, "tend", "", false, client.SeedTransitionOptions{Assignee: "Keel"})
	if err != nil {
		t.Fatal(err)
	}
	if !assigned.Seed.Claimed || assigned.Seed.Tender == nil || assigned.Seed.Tender.Ref != "member:keel" {
		t.Fatalf("assigned: %+v", assigned)
	}
}

func TestResumingAMemberClaimedSeedWakesTheMember(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	first := wakeCrew(t, cli, "keel", "").SessionID
	w.Launched(string(first))
	seed := plantSeedAs(t, cli, string(first), "Resume the member")
	if _, err := cli.SeedTransition(first, seed, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	rev := lifeShow(t, cli, seed).Seed.Rev
	crewHandoff(t, cli, string(first), "Ready to wake later.", false, protocol.CrewDayCloseSleep)
	resumed := seedResumeRequest(app, seed)
	if !resumed.Success || resumed.SessionID == nil || *resumed.SessionID == first || protocol.Deref(resumed.AlreadyRunning) {
		t.Fatalf("resume asleep member: %+v", resumed)
	}
	w.Launched(string(*resumed.SessionID))
	again := seedResumeRequest(app, seed)
	if !again.Success || !protocol.Deref(again.AlreadyRunning) || protocol.Deref(again.SessionID) != *resumed.SessionID {
		t.Fatalf("resume awake member: %+v", again)
	}
	shown := lifeShow(t, cli, seed).Seed
	if shown.Rev != rev || shown.Tender == nil || shown.Tender.Ref != "member:keel" || protocol.Deref(shown.Tender.SessionID) != *resumed.SessionID {
		t.Fatalf("resume changed claim: %+v", shown)
	}
}

func TestAHandoverByACrewSessionRecordsTheMember(t *testing.T) {
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
	shown := sessionOfDelegate(t, w, string(delegated.SessionID))
	if shown.Dispatcher == nil || shown.Dispatcher.Ref != "member:keel" {
		t.Fatalf("dispatcher: %+v", shown.Dispatcher)
	}
	handed := crewHandoff(t, cli, string(first), "My delegate is still working.", false, protocol.CrewDayCloseNap)
	next := protocol.Deref(handed.SessionID)
	w.Launched(string(next))
	if _, err := cli.AgentClose(string(delegated.SessionID), next, "The assigned work is complete."); err != nil {
		t.Fatalf("successor close: %v", err)
	}
}
