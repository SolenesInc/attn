package daemon_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const askGuidanceLead = "ask it with `attn ask`"

const cacheAsk = `Deploys empty the cache.

:::question
Which store should keep the cache?
- [ ] Postgres — already deployed
- [ ] Redis — one more service
Recommended: Postgres
:::
`

func TestAnAnsweredAskReachesTheAgentsInboxAndSettlesItsTurn(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(asker)
	if guidance := launchGuidance(t, agent); !strings.Contains(guidance, askGuidanceLead) {
		t.Fatalf("an agent launched with asks on was not taught them:\n%s", guidance)
	}
	idle := testworld.AwaitSession(app, asker, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	app.TypeLine(asker, "pick a cache store")
	agent.Prompted()
	working := testworld.AwaitStateAfter(app, idle, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	asked, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if asked.Subject != "Which store should keep the cache?" || len(asked.Parts) != 2 || asked.Parts[1].Question == nil {
		t.Fatalf("the ask reads %+v, want the prose then the question, subject from the question", asked)
	}
	shown := testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 1 })
	if shown.Asks[0].ID != asked.ID || string(shown.Asks[0].SessionID) != asker {
		t.Fatalf("the app shows %+v, want %s from %s", shown.Asks[0], asked.ID, asker)
	}
	if listed, err := cli.AskList(protocol.SessionID(asker)); err != nil || !listed.Enabled || len(listed.Asks) != 1 {
		t.Fatalf("the agent lists %+v (%v), want its one open ask", listed, err)
	}

	agent.Reply("Asked about the store. <!-- attn:state=waiting_input -->")
	waiting := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.TurnOwed)
	})

	requestID := uuid.NewString()
	answered := testworld.Request(app, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: requestID, AskID: asked.ID,
		Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Postgres"}}},
		Note:    protocol.Ptr("Keep it boring."),
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == requestID })
	if !answered.Success {
		t.Fatalf("answering failed: %s", protocol.Deref(answered.Error))
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 0 })
	testworld.AwaitStateAfter(app, waiting, func(s protocol.Session) bool { return s.TurnOwed == nil })

	if got := agent.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("the agent was prompted with %q, want the inbox doorbell", got)
	}
	read := inboxContents(readInbox(t, cli, asker, 0).Items)
	for _, want := range []string{asked.ID, "Which store should keep the cache?", "Answer: Postgres (the recommended choice)", "> Keep it boring."} {
		if !strings.Contains(read, want) {
			t.Errorf("the agent read %q, missing %q", read, want)
		}
	}

	again := testworld.Request(app, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: "again", AskID: asked.ID, Answers: []protocol.AskAnswer{}, Note: protocol.Ptr("twice"),
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == "again" })
	if again.Success {
		t.Fatal("an answered ask took a second answer")
	}
}

func TestAnAgentKeepsTheAsksSettingItLaunchedWith(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	taught := func(run *fakeagent.Run) bool { return strings.Contains(launchGuidance(t, run), askGuidanceLead) }
	respawn := func(session string) *fakeagent.Run {
		t.Helper()
		app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: protocol.SessionID(session)})
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
		reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30},
			protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
		if !reloaded.Success {
			t.Fatalf("respawning %s: %s", session, protocol.Deref(reloaded.Error))
		}
		return w.Launched(session)
	}
	without := w.Spawn(app, fakeagent.Claude, w.Path("before"))
	if taught(w.Launched(without)) {
		t.Fatal("an agent launched with asks off was taught them")
	}

	setSetting(t, app, "asks_enabled", "true")
	with := w.Spawn(app, fakeagent.Claude, w.Path("after"))
	if !taught(w.Launched(with)) {
		t.Fatal("an agent launched with asks on was not taught them")
	}
	if taught(respawn(without)) {
		t.Fatal("an agent launched with asks off was taught them when it came back")
	}
	if _, err := cli.AskOpen(protocol.SessionID(without), "", cacheAsk, "", w.Path("before")); err == nil || !strings.Contains(err.Error(), "launched without them") {
		t.Fatalf("an agent launched before asks were on asked: %v", err)
	}

	setSetting(t, app, "asks_enabled", "false")
	if !taught(respawn(with)) {
		t.Fatal("an agent launched with asks lost them when it came back with the setting off")
	}
	if _, err := cli.AskOpen(protocol.SessionID(with), "", cacheAsk, "", w.Path("after")); err != nil {
		t.Fatalf("an agent launched with asks lost them when the setting went off: %v", err)
	}
}

func TestAnAgentWithdrawsItsOwnAskOnly(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)
	other := w.Spawn(app, fakeagent.Claude, w.Path("other"))
	w.Launched(other)
	asked, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "Cache store", w.Path("shop"))
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if asked.Subject != "Cache store" {
		t.Errorf("subject = %q, want the one given", asked.Subject)
	}
	if err := cli.AskClose(protocol.SessionID(other), asked.ID); err == nil {
		t.Fatal("another agent withdrew the ask")
	}
	if err := cli.AskClose(protocol.SessionID(asker), asked.ID); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 0 })
}

func TestAnAskFollowsItsAddresseeNotTheSessionThatFiledIt(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")

	plain := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(plain)
	if _, err := cli.AskOpen(protocol.SessionID(plain), "", cacheAsk, "", w.Path("shop")); err != nil {
		t.Fatalf("ask: %v", err)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 1 })
	if closed := closeFromApp(app, plain); !closed.Accepted {
		t.Fatalf("close %s: %+v", plain, closed)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 0 })

	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID))
	asked, err := cli.AskOpen(day.SessionID, "", cacheAsk, "", w.Path("trellis"))
	if err != nil {
		t.Fatalf("crew ask: %v", err)
	}
	successor := protocol.Deref(crewHandoff(t, cli, string(day.SessionID), "Still waiting on the cache answer.", false, protocol.CrewDayCloseNap).SessionID)
	w.Launched(string(successor))
	if shown := w.App().Initial.Asks; len(shown) != 1 || shown[0].ID != asked.ID || protocol.Deref(shown[0].CrewMember) != "trellis" {
		t.Fatalf("after trellis napped the app shows %+v, want its open ask", shown)
	}
	if listed, err := cli.AskList(successor); err != nil || len(listed.Asks) != 1 || listed.Asks[0].ID != asked.ID {
		t.Fatalf("trellis's next day lists %+v (%v), want the ask filed before the nap", listed, err)
	}
}

func TestAsksBelongToTheAskingSessionsProfile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	home := app.SelectedProfile()
	side := createProfile(app, "Side")
	sideApp := w.AppOn(side.ID)
	homeAsker := w.Spawn(app, fakeagent.Claude, w.Path("home"))
	w.Launched(homeAsker)
	sideAsker := w.Spawn(sideApp, fakeagent.Claude, w.Path("side"))
	w.Launched(sideAsker)
	homeAsk, err := cli.AskOpen(protocol.SessionID(homeAsker), "", cacheAsk, "", w.Path("home"))
	if err != nil {
		t.Fatalf("home ask: %v", err)
	}
	sideAsk, err := cli.AskOpen(protocol.SessionID(sideAsker), "", cacheAsk, "", w.Path("side"))
	if err != nil {
		t.Fatalf("side ask: %v", err)
	}
	only := func(profile, id string) func(protocol.AsksUpdatedMessage) bool {
		return func(m protocol.AsksUpdatedMessage) bool {
			return m.ProfileID == profile && len(m.Asks) == 1 && m.Asks[0].ID == id
		}
	}
	testworld.Await(app, protocol.EventAsksUpdated, only(home, homeAsk.ID))
	testworld.Await(sideApp, protocol.EventAsksUpdated, only(side.ID, sideAsk.ID))

	crossed := testworld.Request(sideApp, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: "across", AskID: homeAsk.ID, Answers: []protocol.AskAnswer{}, Note: protocol.Ptr("from the side"),
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == "across" })
	if crossed.Success {
		t.Fatal("an app on another profile answered the ask")
	}

	selectProfile(app, side.ID)
	testworld.Await(app, protocol.EventAsksUpdated, only(side.ID, sideAsk.ID))
	if shown := w.AppOn(home).Initial.Asks; len(shown) != 1 || shown[0].ID != homeAsk.ID {
		t.Fatalf("the home profile starts with %+v, want only its own ask", shown)
	}
}

func TestEachHarnessLaunchedWithAsksIsTaughtThem(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex, fakeagent.Copilot)
	app := w.App()
	setSetting(t, app, "asks_enabled", "true")
	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex, fakeagent.Copilot} {
		if guidance := launchGuidance(t, w.Launched(w.Spawn(app, h, w.Path(string(h))))); !strings.Contains(guidance, askGuidanceLead) {
			t.Errorf("%s launched with asks on was not taught them:\n%s", h, guidance)
		}
	}
}

func TestADelegatesAskIsAboutItsSeedAndItsAnswerIsANoteForTheNextTender(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	cwd := registerDelegationCaller(t, w, cli, "caller")
	seed := plantDelegationSeed(t, cli, "caller", "Migrate the store")
	first, err := cli.Delegate(delegateAtSeed("caller", cwd, seed))
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(string(first.SessionID))

	asked, err := cli.AskOpen(first.SessionID, "", cacheAsk, "", cwd)
	if err != nil {
		t.Fatalf("the delegate asks: %v", err)
	}
	if protocol.Deref(asked.SeedID) != seed || protocol.Deref(asked.SeedTitle) != "Migrate the store" || protocol.Deref(asked.SeedSlug) != "migrate-store" {
		t.Fatalf("the delegate's ask reads %+v, want it about %s", asked, seed)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool {
		return len(m.Asks) == 1 && protocol.Deref(m.Asks[0].SeedID) == seed && protocol.Deref(m.Asks[0].SeedTitle) == "Migrate the store"
	})

	handover := delegateAtSeed("caller", cwd, seed)
	handover.Assignment.Handover = &protocol.DelegateHandover{}
	second, err := cli.Delegate(handover)
	if err != nil {
		t.Fatal(err)
	}
	successor := w.Launched(string(second.SessionID))
	if closed := closeFromApp(app, string(first.SessionID)); !closed.Accepted {
		t.Fatalf("close %s: %+v", first.SessionID, closed)
	}
	if shown := w.App().Initial.Asks; len(shown) != 1 || shown[0].ID != asked.ID || protocol.Deref(shown[0].SeedID) != seed {
		t.Fatalf("after the delegate closed the app shows %+v, want its ask about %s", shown, seed)
	}
	if listed, err := cli.AskList(second.SessionID); err != nil || len(listed.Asks) != 1 || listed.Asks[0].ID != asked.ID {
		t.Fatalf("the seed's next tender lists %+v (%v), want the ask its predecessor filed", listed, err)
	}
	if err := cli.AskClose("caller", asked.ID); err == nil {
		t.Fatal("the dispatcher, who does not tend the seed, withdrew the ask")
	}

	successor.Prompted()
	successor.Reply("Picked up the migration. <!-- attn:state=waiting_input -->")
	waiting := testworld.AwaitSession(app, string(second.SessionID), func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.TurnOwed)
	})
	answered := testworld.Request(app, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: "answer", AskID: asked.ID,
		Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Postgres"}}},
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == "answer" })
	if !answered.Success {
		t.Fatalf("answering failed: %s", protocol.Deref(answered.Error))
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 0 })
	testworld.AwaitStateAfter(app, waiting, func(s protocol.Session) bool { return s.TurnOwed == nil })
	if got := successor.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("the seed's tender was prompted with %q, want the inbox doorbell", got)
	}
	items := readInbox(t, cli, string(second.SessionID), 0).Items
	if read := inboxContents(items); !strings.Contains(read, seed) || strings.Contains(read, "Answer: Postgres") {
		t.Errorf("the seed's tender read %q, want a bell about %s and not a copy of the answer", read, seed)
	}
	notes, err := cli.SeedNotes("", seed, 1)
	if err != nil || len(notes.Notes) != 1 {
		t.Fatalf("the seed's notes are %+v, %v; want the answer", notes, err)
	}
	answer := notes.Notes[0]
	for _, want := range []string{asked.ID, "Answer: Postgres (the recommended choice)"} {
		if !strings.Contains(answer.Body, want) {
			t.Errorf("the seed's newest note reads %q, missing %q", answer.Body, want)
		}
	}
	if answer.Author.Ref != "user" {
		t.Errorf("the answer is noted by %q, want the user", answer.Author.Ref)
	}
}

func TestClosingASeedClosesTheAsksAboutIt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	cwd := registerDelegationCaller(t, w, cli, "caller")
	delegated, err := cli.Delegate(delegateFrom("caller", cwd, "Migrate the store", fakeagent.Codex))
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(string(delegated.SessionID))
	side := plantSeedAs(t, cli, string(delegated.SessionID), "Tune the queue")
	moveDelegationSeed(t, cli, string(delegated.SessionID), side, "tend", "")

	for _, close := range []struct{ seed, named, verb string }{{delegated.SeedID, "", "wither"}, {side, side, "harvest"}} {
		asked, err := cli.AskOpen(delegated.SessionID, close.named, cacheAsk, "", cwd)
		if err != nil || protocol.Deref(asked.SeedID) != close.seed {
			t.Fatalf("asking about %s = %+v, %v", close.seed, asked, err)
		}
		testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 1 })
		moveDelegationSeed(t, cli, string(delegated.SessionID), close.seed, close.verb, "settled without asking")
		testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 0 })
	}
	if after, err := cli.AskOpen(delegated.SessionID, "", cacheAsk, "", cwd); err != nil || after.SeedID != nil {
		t.Errorf("a delegate whose stake withered asked %+v (%v), want an ask about no seed", after, err)
	}
}

func TestAnAgentAsksAboutASeedItTendsAndNoOther(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)
	registerSessions(t, w, cli, "holder")
	tended := plantSeedAs(t, cli, asker, "Pick the cache store")
	moveDelegationSeed(t, cli, asker, tended, "tend", "")
	held := plantSeedAs(t, cli, asker, "Tune the queue")
	moveDelegationSeed(t, cli, "holder", held, "tend", "")
	untended := plantSeedAs(t, cli, asker, "Nobody has this")

	seedless, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "", w.Path("shop"))
	if err != nil || seedless.SeedID != nil {
		t.Fatalf("an agent with no stake asked %+v (%v), want an ask about no seed", seedless, err)
	}
	about, err := cli.AskOpen(protocol.SessionID(asker), tended, cacheAsk, "", w.Path("shop"))
	if err != nil || protocol.Deref(about.SeedID) != tended {
		t.Fatalf("asking about a seed the agent tends = %+v, %v; want it about %s", about, err, tended)
	}
	for seed, refusal := range map[string]string{held: "is being tended by holder", untended: "nobody is tending " + untended} {
		if _, err := cli.AskOpen(protocol.SessionID(asker), seed, cacheAsk, "", w.Path("shop")); err == nil || !strings.Contains(err.Error(), refusal) {
			t.Errorf("asking about %s = %v, want a refusal saying %q", seed, err, refusal)
		}
	}
	if listed, err := cli.AskList(protocol.SessionID(asker)); err != nil || len(listed.Asks) != 2 {
		t.Fatalf("the agent lists %+v (%v), want both its asks", listed, err)
	}
}

func TestClosingASessionClosesTheAsksAddressedToIt(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)
	testworld.AwaitSession(app, asker, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	asked, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 1 })

	if err := cli.Unregister(protocol.SessionID(asker)); err != nil {
		t.Fatalf("close %s: %v", asker, err)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 0 })
	late := testworld.Request(app, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: "late", AskID: asked.ID, Answers: []protocol.AskAnswer{}, Note: protocol.Ptr("too late"),
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == "late" })
	if late.Success {
		t.Fatal("an ask of a closed session took an answer")
	}
}

func TestRetiringAMemberClosesItsAsksAndRestoringKeepsThemClosed(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID))
	asked, err := cli.AskOpen(day.SessionID, "", cacheAsk, "", w.Path("trellis"))
	if err != nil {
		t.Fatalf("crew ask: %v", err)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 1 })

	retired, err := cli.CrewRetire("trellis")
	if err != nil || len(retired.ClosedAsks) != 1 || retired.ClosedAsks[0] != asked.ID {
		t.Fatalf("retire = %+v (%v), want it to close %s", retired, err, asked.ID)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 0 })
	late := testworld.Request(app, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: "late", AskID: asked.ID, Answers: []protocol.AskAnswer{}, Note: protocol.Ptr("too late"),
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == "late" })
	if late.Success {
		t.Fatal("an ask of a retired member took an answer")
	}

	if _, err := cli.CrewRestore("trellis"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if shown := w.App().Initial.Asks; len(shown) != 0 {
		t.Fatalf("after restore the app shows %+v, want no asks", shown)
	}
}
