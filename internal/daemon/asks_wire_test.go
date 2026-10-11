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

	asked, err := cli.AskOpen(protocol.SessionID(asker), cacheAsk, "", w.Path("shop"))
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
	for _, want := range []string{asked.ID, "Which store should keep the cache?", "Answer: Postgres (your recommendation)", "> Keep it boring."} {
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
	if _, err := cli.AskOpen(protocol.SessionID(without), cacheAsk, "", w.Path("before")); err == nil || !strings.Contains(err.Error(), "launched without them") {
		t.Fatalf("an agent launched before asks were on asked: %v", err)
	}

	setSetting(t, app, "asks_enabled", "false")
	if !taught(respawn(with)) {
		t.Fatal("an agent launched with asks lost them when it came back with the setting off")
	}
	if _, err := cli.AskOpen(protocol.SessionID(with), cacheAsk, "", w.Path("after")); err != nil {
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
	asked, err := cli.AskOpen(protocol.SessionID(asker), cacheAsk, "Cache store", w.Path("shop"))
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
	if _, err := cli.AskOpen(protocol.SessionID(plain), cacheAsk, "", w.Path("shop")); err != nil {
		t.Fatalf("ask: %v", err)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 1 })
	if closed := closeFromApp(app, plain); !closed.Accepted {
		t.Fatalf("close %s: %+v", plain, closed)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 0 })

	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID))
	asked, err := cli.AskOpen(day.SessionID, cacheAsk, "", w.Path("trellis"))
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
	homeAsk, err := cli.AskOpen(protocol.SessionID(homeAsker), cacheAsk, "", w.Path("home"))
	if err != nil {
		t.Fatalf("home ask: %v", err)
	}
	sideAsk, err := cli.AskOpen(protocol.SessionID(sideAsker), cacheAsk, "", w.Path("side"))
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
