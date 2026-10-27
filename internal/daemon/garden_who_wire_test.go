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

func TestACrewClaimOutlivesItsSessionThroughNapAndClear(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	first := wakeCrew(t, cli, "keel", "").SessionID
	w.Launched(string(first))
	seed := plantSeedAs(t, cli, string(first), "Keep the claim across conversations")
	if _, err := cli.SeedTransition(first, seed, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	handed := crewHandoff(t, cli, string(first), "Continue the same work.", false, protocol.CrewSessionCloseNap)
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
	handed := crewHandoff(t, cli, string(first), "The watched tree continues.", false, protocol.CrewSessionCloseNap)
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
	day := wakeCrew(t, cli, "Keel", "").SessionID
	w.Launched(string(day)).Prompted()
	assigned, err := cli.SeedTransition("", seed, "tend", "", false, client.SeedTransitionOptions{Assignee: "Keel"})
	if err != nil {
		t.Fatal(err)
	}
	if !assigned.Seed.Claimed || assigned.Seed.Tender == nil || assigned.Seed.Tender.Ref != "member:keel" {
		t.Fatalf("assigned: %+v", assigned)
	}
	if items := readInbox(t, cli, string(day), 0).Items; len(items) != 1 || protocol.Deref(items[0].Hint) != "tended" {
		t.Fatalf("user assignment excluded its tender: %+v", items)
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
	crewHandoff(t, cli, string(first), "Ready to wake later.", false, protocol.CrewSessionCloseSleep)
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
	handed := crewHandoff(t, cli, string(first), "My delegate is still working.", false, protocol.CrewSessionCloseNap)
	next := protocol.Deref(handed.SessionID)
	w.Launched(string(next))
	if _, err := cli.AgentClose(string(delegated.SessionID), next, "The assigned work is complete."); err != nil {
		t.Fatalf("successor close: %v", err)
	}
}

func TestRetiredMembersCannotReclaimWorkThroughResume(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	if _, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel"}); err != nil {
		t.Fatal(err)
	}
	first := wakeCrew(t, cli, "Keel", "").SessionID
	w.Launched(string(first)).Prompted()
	seed := plantSeedAs(t, cli, string(first), "Leave this work released")
	lifeMove(t, cli, string(first), seed, "tend", "", "")
	if _, err := cli.CrewRetire("Keel"); err != nil {
		t.Fatal(err)
	}
	released := lifeShow(t, cli, seed).Seed
	if resumed := seedResumeRequest(app, seed); resumed.Success || !strings.Contains(protocol.Deref(resumed.Error), "Keel is retired") {
		t.Fatalf("retired Resume: %+v", resumed)
	}
	if shown := lifeShow(t, cli, seed).Seed; shown.Rev != released.Rev || shown.Claimed || shown.Tender != nil {
		t.Fatalf("retired actions changed seed: %+v", shown)
	}
}

func TestSendToChiefNeedsOnlyTheRevision(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	chief := configureChiefOn(t, w, app, fakeagent.Claude, "sonnet")
	w.Launched(chief).Prompted()
	roster, err := cli.CrewList()
	if err != nil {
		t.Fatal(err)
	}
	var key string
	for _, member := range roster.Members {
		if member.Chief {
			key = member.Key
		}
	}
	seed := plantSeedAs(t, cli, "", "Let Keel choose the next context")
	sent, err := cli.SeedSendToChief("", lifeShow(t, cli, seed).Seed, "")
	if err != nil {
		t.Fatal(err)
	}
	if sent.Chief.Ref != protocol.PartyRef("member:"+key) || sent.Seed.Tender == nil || sent.Seed.Tender.Ref != protocol.PartyRef("member:"+key) || !sent.Seed.Claimed {
		t.Fatalf("crew Chief assignment: %+v", sent)
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
		testworld.Await(app, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
			for _, s := range m.Seeds {
				if s.ID == seed && s.Claimed == claimed && s.Ready != claimed && s.Tender != nil {
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
		if peek, err := cli.AgentPeek(tended); err != nil || string(peek.SessionID) != id || peek.Exit == nil {
			t.Fatalf("seed target lost its exited tender: %+v %v", peek, err)
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
	}, fakeagent.Claude)
}
