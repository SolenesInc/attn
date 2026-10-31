package daemon_test

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestACrewClaimOutlivesItsSessionThroughAHarnessSwitch(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
	cli := w.Client()
	first := wakeCrew(t, cli, "keel", "").SessionID
	w.Launched(string(first))
	seed := plantSeedAs(t, cli, string(first), "Keep the claim across sessions")
	if _, err := cli.SeedTransition(first, seed, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	switchCrewHarness(t, cli, "keel", "codex")
	handed := crewHandoff(t, cli, string(first), "Continue the same work.", false)
	next := protocol.Deref(handed.SessionID)
	w.Launched(string(next)).Prompted()
	shown := lifeShow(t, cli, seed).Seed
	if !shown.Claimed || shown.Tender == nil || shown.Tender.Ref != "member:keel" || protocol.Deref(shown.Tender.SessionID) != next {
		t.Fatalf("after the switch the seed shows %+v, want it claimed by member:keel in session %s", shown.Tender, next)
	}
}

func TestAMembersWatchRingsItsNextSessionAfterAHarnessSwitch(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
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
	switchCrewHarness(t, cli, "keel", "codex")
	handed := crewHandoff(t, cli, string(first), "The watched tree continues.", false)
	next := protocol.Deref(handed.SessionID)
	w.Launched(string(next))
	gardenNudgeNote(t, cli, sender, child.Seed.ID, "The child changed.", true)
	items := readInbox(t, cli, string(next), 0).Items
	if len(items) != 1 || items[0].Address != "member:keel" || !strings.Contains(items[0].Content, child.Seed.ID) {
		t.Fatalf("the member's next session reads %+v, want the watched child's update addressed to member:keel", items)
	}
	gardenNudgeNote(t, cli, sender, child.Seed.ID, "Another child update.", true)
	gardenNudgeWatch(t, cli, string(next), plot, true)
	if items := readInbox(t, cli, string(next), 0).Items; len(items) != 0 {
		t.Fatalf("unwatching from the next session left pending updates: %+v", items)
	}
	gardenNudgeNote(t, cli, string(next), child.Seed.ID, "From the member.", true)
	if items := readInbox(t, cli, sender, 0).Items; len(items) != 1 || items[0].Address != "session:"+sender {
		t.Fatalf("a plain session's watch reads %+v, want one update to its session", items)
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
	switchCrewHarness(t, cli, "keel", "codex")
	handed := crewHandoff(t, cli, string(first), "My delegate is still working.", false)
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
	crewHandoff(t, cli, string(first), "Ready to wake later.", false)
	if offered := seedReaderDocument(app, seed); !offered.Success || offered.Document.Seed.Continuation == nil || !offered.Document.Seed.Continuation.ResumeAvailable {
		t.Fatalf("an asleep member's seed does not offer Resume: %+v", offered.Document)
	}
	resumed := seedResumeRequest(app, seed)
	if !resumed.Success || resumed.SessionID == nil || *resumed.SessionID == first || protocol.Deref(resumed.AlreadyRunning) {
		t.Fatalf("resuming an asleep member's seed = %+v, want a new session for the member", resumed)
	}
	w.Launched(string(*resumed.SessionID))
	again := seedResumeRequest(app, seed)
	if !again.Success || !protocol.Deref(again.AlreadyRunning) || protocol.Deref(again.SessionID) != *resumed.SessionID {
		t.Fatalf("resuming an awake member's seed = %+v, want its session %s", again, *resumed.SessionID)
	}
	shown := lifeShow(t, cli, seed).Seed
	if shown.Rev != rev || shown.Tender == nil || shown.Tender.Ref != "member:keel" || protocol.Deref(shown.Tender.SessionID) != *resumed.SessionID {
		t.Fatalf("resume changed the claim: %+v", shown)
	}
}

func TestRetiredMembersCannotReclaimWorkThroughResume(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	createCrew(t, cli, "Keel")
	first := wakeCrew(t, cli, "Keel", "").SessionID
	w.Launched(string(first)).Prompted()
	seed := plantSeedAs(t, cli, string(first), "Leave this work released")
	lifeMove(t, cli, string(first), seed, "tend", "", "")
	retireCrew(t, cli, "Keel")
	released := lifeShow(t, cli, seed).Seed
	if resumed := seedResumeRequest(app, seed); resumed.Success || !strings.Contains(protocol.Deref(resumed.Error), "Keel is retired") {
		t.Fatalf("resuming a retired member's seed = %+v, want it refused", resumed)
	}
	if shown := lifeShow(t, cli, seed).Seed; shown.Rev != released.Rev || shown.Claimed || shown.Tender != nil {
		t.Fatalf("the refused resume changed the seed: %+v", shown)
	}
}

func TestGardenEventsNameWhoCausedThemAndWhoWasTold(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	day := wakeCrew(t, cli, "keel", "").SessionID
	w.Launched(string(day))
	plain := spawnPanes(w, app, w.Path("plain"))[0].session
	seed := plantSeedAs(t, cli, plain, "Name the cause")
	lifeMove(t, cli, string(day), seed, "tend", "", "")
	gardenNudgeNote(t, cli, plain, seed, "from the plain session", true)
	if _, err := cli.SeedTransition("", seed, "park", "", true, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite3", "file:"+filepath.Join(w.Dir, "attn.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name, coalesce(json_extract(payload, '$.caused_by'), ''), coalesce(json_extract(payload, '$.caused_by_session_id'), '') FROM bus_events WHERE subject = ? ORDER BY seq`, seed)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name, by, session string
		if err := rows.Scan(&name, &by, &session); err != nil {
			t.Fatal(err)
		}
		got = append(got, name+" by "+by+" from "+session)
	}
	want := []string{
		"garden.seed.planted by session:" + plain + " from " + plain,
		"garden.seed.tended by member:keel from " + string(day),
		"garden.seed.note.added by member:keel from " + string(day),
		"garden.seed.note.added by session:" + plain + " from " + plain,
		"garden.seed.parked by user from ",
		"garden.seed.note.added by user from ",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("garden events for %s =\n%s\nwant\n%s", seed, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
