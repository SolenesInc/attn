package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func chiefOfRoster(t *testing.T, members []protocol.CrewMember) protocol.CrewMember {
	t.Helper()
	return chiefOfProfileRoster(t, members, "")
}

func chiefOfProfileRoster(t *testing.T, members []protocol.CrewMember, profileID string) protocol.CrewMember {
	t.Helper()
	var chiefs []protocol.CrewMember
	for _, member := range members {
		if protocol.Deref(member.Chief) && (profileID == "" || member.ProfileID == profileID) {
			chiefs = append(chiefs, member)
		}
	}
	if len(chiefs) != 1 {
		t.Fatalf("the roster has %d Chiefs, want exactly one: %+v", len(chiefs), members)
	}
	return chiefs[0]
}

func turnOnCrewV2(t *testing.T, w *world) protocol.CrewMember {
	t.Helper()
	setSetting(t, w.App(), "crew_v2_enabled", "true")
	return chiefOfRoster(t, crewRoster(t, w.Client()))
}

func wakeConfiguredChief(t *testing.T, w *world, cli *client.Client) *fakeagent.Run {
	t.Helper()
	setCrew(t, cli, "chief", protocol.CrewSetMessage{Agent: protocol.Ptr("claude"), Model: protocol.Ptr("sonnet")})
	woken := wakeCrew(t, cli, "chief", "")
	run := w.Launched(string(woken.SessionID))
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	return run
}

func chiefSession(t *testing.T, cli *client.Client) protocol.SessionID {
	t.Helper()
	chief := chiefOfRoster(t, crewRoster(t, cli))
	if chief.BindingSession == nil {
		t.Fatalf("the Chief is asleep: %+v", chief)
	}
	return *chief.BindingSession
}

func sessionsMarkedChief(app *testworld.Peer) []protocol.SessionID {
	var marked []protocol.SessionID
	for _, session := range app.Initial.Sessions {
		if protocol.Deref(session.ChiefOfStaff) {
			marked = append(marked, session.ID)
		}
	}
	return marked
}

func TestWithCrewV2EveryProfileHasItsOwnChiefMember(t *testing.T) {
	w := newWorld(t)
	for _, member := range w.App().Initial.Crew {
		if protocol.Deref(member.Chief) {
			t.Fatalf("a Chief member exists with Crew v2 off: %+v", member)
		}
	}
	chief := turnOnCrewV2(t, w)
	defaultProfile := w.App().SelectedProfile()
	if chief.Name != "Chief" || chief.Agent != nil || chief.BindingSession != nil || chief.ProfileID != defaultProfile {
		t.Fatalf("a fresh Chief = %+v, want an asleep Chief without a harness", chief)
	}
	if _, err := os.Stat(chief.CharterPath); err != nil {
		t.Fatalf("the Chief has no charter file to write on its first day: %v", err)
	}
	work := createProfile(w.App(), "Work")
	workChief := chiefOfProfileRoster(t, w.AppOn(work.ID).Initial.Crew, work.ID)
	if workChief.ID == chief.ID || workChief.ProfileID != work.ID {
		t.Fatalf("Work's Chief = %+v, want its own member", workChief)
	}
	w.restart()
	if again := chiefOfProfileRoster(t, w.AppOn(defaultProfile).Initial.Crew, defaultProfile); again.ID != chief.ID {
		t.Fatalf("Default's Chief changed across a restart: %s, was %s", again.ID, chief.ID)
	}
	if again := chiefOfProfileRoster(t, w.AppOn(work.ID).Initial.Crew, work.ID); again.ID != workChief.ID {
		t.Fatalf("Work's Chief changed across a restart: %s, was %s", again.ID, workChief.ID)
	}
}

func TestAChiefWithoutAHarnessKeepsItsMailUntilItIsConfigured(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	turnOnCrewV2(t, w)
	registerSessions(t, w, cli, "sender")
	mail, err := cli.AgentMsg("chief", "sender", "keep this until configured")
	if err != nil || mail.Status == protocol.AgentMsgStatusRefused || !strings.Contains(mail.Detail, "no harness") {
		t.Fatalf("mail to an unconfigured Chief = %+v, %v; want it kept and the reason named", mail, err)
	}
	_, err = cli.CrewWake("chief", "", "")
	crewErrorContains(t, err, "no harness", "attn crew set chief --agent")
	_, err = cli.CrewSet("chief", nil, protocol.Ptr(""), nil, nil, nil)
	crewErrorContains(t, err, "needs a harness")
	if awake := chiefOfRoster(t, crewRoster(t, cli)).BindingSession; awake != nil {
		t.Fatalf("an unconfigured Chief woke in %s", *awake)
	}

	setCrew(t, cli, "chief", protocol.CrewSetMessage{Agent: protocol.Ptr("claude"), Model: protocol.Ptr("sonnet")})
	woken := wakeCrew(t, cli, "chief", "")
	desktop, _ := placedPane(t, w, string(woken.SessionID))
	if desktop.Name != "Chief" {
		t.Fatalf("the Chief's first wake landed on %+v, want its own desktop", desktop)
	}
	run := w.Launched(string(woken.SessionID))
	if guidance := launchGuidance(t, run); !strings.Contains(guidance, "You are this profile's Chief") || !strings.Contains(guidance, "CHARTER.md") || strings.Contains(guidance, "You are the chief of staff") {
		t.Fatalf("the Chief's launch guidance: %s", guidance)
	}
	if prompt := run.Prompted(); !strings.Contains(prompt, "You have been woken") {
		t.Fatalf("the Chief's first prompt: %s", prompt)
	}
	run.Reply("Ready. <!-- attn:state=idle -->")
	if prompt := run.Prompted(); !strings.Contains(prompt, "attn agent inbox") {
		t.Fatalf("the kept mail did not ring the Chief: %s", prompt)
	}
	items := readInbox(t, cli, string(woken.SessionID), 0).Items
	if len(items) != 1 || !strings.Contains(items[0].Content, "keep this until configured") {
		t.Fatalf("the Chief's inbox = %+v", items)
	}
	testworld.AwaitSession(app, string(woken.SessionID), func(s protocol.Session) bool { return protocol.Deref(s.ChiefOfStaff) })
}

func TestChiefMailReachesItAfterSleepAndRename(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	cli := w.Client()
	turnOnCrewV2(t, w)
	registerSessions(t, w, cli, "sender")
	wakeConfiguredChief(t, w, cli)
	first := chiefSession(t, cli)
	crewHandoff(t, cli, string(first), "Return when mail arrives.", false)
	if _, err := cli.CrewRename("chief", "Alfred"); err != nil {
		t.Fatal(err)
	}
	sent, err := cli.AgentMsg("chief", "sender", "the release landed")
	if err != nil {
		t.Fatal(err)
	}
	chief := chiefOfRoster(t, crewRoster(t, cli))
	if chief.Name != "Alfred" || chief.BindingSession == nil || *chief.BindingSession == first || sent.TargetSessionID != *chief.BindingSession {
		t.Fatalf("mail to `chief` after a rename = %+v, Chief %+v; want Alfred woken for it", sent, chief)
	}
	next := w.Launched(string(*chief.BindingSession))
	if guidance := launchGuidance(t, next); !strings.Contains(guidance, "You are **Alfred**") || !strings.Contains(guidance, "You are this profile's Chief") {
		t.Fatalf("the renamed Chief's guidance: %s", guidance)
	}
	next.Prompted()
	next.Reply("Ready. <!-- attn:state=idle -->")
	if prompt := next.Prompted(); !strings.Contains(prompt, "attn agent inbox") {
		t.Fatalf("mail did not ring: %s", prompt)
	}
	_, err = cli.CrewRetire("chief")
	crewErrorContains(t, err, "can't be retired", "attn crew sleep chief")
	app := w.App()
	app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: *chief.BindingSession})
	if refused := testworld.Refused(app); !strings.Contains(protocol.Deref(refused.Error), "Alfred is protected from closing; put Alfred to sleep first") {
		t.Fatalf("closing the Chief's session = %+v", refused)
	}
	crewHandoff(t, cli, string(*chief.BindingSession), "Back to the old name.", false)
	if renamed, err := cli.CrewRename("alfred", "Chief"); err != nil || renamed.Name != "Chief" {
		t.Fatalf("renaming the Chief back to Chief = %+v, %v", renamed, err)
	}
	registerSessions(t, w, cli, "pretender")
	_, err = cli.CrewCreate("chief")
	crewErrorContains(t, err, "reserved")
}

func TestTheChiefKeepsItsRoleAndAuthorityAcrossClear(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	turnOnCrewV2(t, w)
	run := wakeConfiguredChief(t, w, cli)
	id := chiefSession(t, cli)
	worker := spawnPanes(w, app, w.Path("worker"))[0].session
	next := clearClaude(app, run, string(id))
	testworld.AwaitSession(app, string(next.ID), func(s protocol.Session) bool { return protocol.Deref(s.ChiefOfStaff) })
	app.TypeLine(string(next.ID), "orient again")
	run.Prompted()
	working := testworld.AwaitSession(app, string(next.ID), func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	run.Reply("Ready again. <!-- attn:state=idle -->")
	settled := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if protocol.Deref(settled.TurnOwed) {
		t.Fatal("the Chief owes a turn after /clear")
	}
	closed, err := cli.AgentClose(worker, next.ID, "the worker is finished")
	if err != nil || closed.Rule != protocol.AgentCloseRuleChiefOfStaff {
		t.Fatalf("the cleared Chief closing a worker = %+v, %v", closed, err)
	}
}

func TestTheChiefIsNeverWarmedOrAskedToSleepWhileTheUserIsAway(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, member := crewDayInBubble(t, w, map[string]string{"crew.away_seconds": "60"})
		setSetting(t, app, "crew_v2_enabled", "true")
		setCrew(t, member.cli, "chief", protocol.CrewSetMessage{Agent: protocol.Ptr("claude")})
		chief := w.bootBubbleClaude(t, string(wakeCrew(t, member.cli, "chief", "").SessionID))
		chief.reply("Ready. <!-- attn:state=idle -->")
		member.term.OnSubmit(nil)
		chief.term.OnSubmit(nil)
		w.advance(56 * time.Minute)
		if len(readInbox(t, member.cli, member.id, 0).Items) != 1 {
			t.Fatal("an ordinary member was not asked to sleep while the user was away")
		}
		if pasted, items := chief.term.Pasted(), readInbox(t, chief.cli, chief.id, 0).Items; len(pasted) != 0 || len(items) != 0 {
			t.Fatalf("the Chief was sent %q and has inbox %q", pasted, inboxContents(items))
		}
	})
}

func TestTurningCrewV2OnHandsTodaysChiefRoleToAPrefilledChiefMember(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	directory := w.Path("chief-work")
	old := w.Spawn(app, fakeagent.Claude, directory, func(m *protocol.SpawnSessionMessage) {
		m.Model, m.Effort = protocol.Ptr("opus"), protocol.Ptr("high")
	})
	w.Launched(old)
	if made := setChiefOfStaff(app, old, true); !made.Success {
		t.Fatalf("making today's Chief = %+v", made)
	}
	w.Launched(old)

	chief := turnOnCrewV2(t, w)
	if protocol.Deref(chief.Agent) != "claude" || protocol.Deref(chief.Model) != "opus" || protocol.Deref(chief.Effort) != "high" || protocol.Deref(chief.Cwd) != directory || chief.BindingSession != nil {
		t.Fatalf("the new Chief = %+v, want today's Chief's harness, model, effort and directory", chief)
	}
	if marked := sessionsMarkedChief(w.App()); len(marked) != 0 {
		t.Fatalf("sessions %v keep the Chief role with Crew v2 on, want none until the member wakes", marked)
	}
	worker := spawnPanes(w, app, w.Path("worker"))[0].session
	if _, err := cli.AgentClose(worker, protocol.SessionID(old), "tidy up"); err == nil {
		t.Fatal("the old Chief session still closes other agents")
	}

	woken := wakeCrew(t, cli, "chief", "")
	run := w.Launched(string(woken.SessionID))
	guidance := launchGuidance(t, run)
	for _, want := range []string{"You are this profile's Chief", "attn agent msg " + old, "attn session transcript " + old} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("the first-day guidance misses %q: %s", want, guidance)
		}
	}
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	if marked := sessionsMarkedChief(w.App()); !slices.Equal(marked, []protocol.SessionID{woken.SessionID}) {
		t.Fatalf("the Chief role is on %v, want the member's session %s", marked, woken.SessionID)
	}
	if _, err := cli.AgentClose(worker, woken.SessionID, "tidy up"); err != nil {
		t.Fatalf("the Chief member closing a worker: %v", err)
	}
	if msg, err := cli.AgentMsg(old, woken.SessionID, "What were you working on?"); err != nil || msg.Status == protocol.AgentMsgStatusRefused {
		t.Fatalf("the Chief messaging the old session = %+v, %v", msg, err)
	}

	crewHandoff(t, cli, string(woken.SessionID), "Met the old session.", false)
	next := w.Launched(string(wakeCrew(t, cli, "chief", "").SessionID))
	if guidance := launchGuidance(t, next); strings.Contains(guidance, old) {
		t.Fatalf("the Chief's second day still points at the old session: %s", guidance)
	}

	setSetting(t, app, "crew_v2_enabled", "false")
	if marked := sessionsMarkedChief(w.App()); !slices.Equal(marked, []protocol.SessionID{protocol.SessionID(old)}) {
		t.Fatalf("with Crew v2 off the Chief role is on %v, want today's Chief %s back", marked, old)
	}
}

func TestTheNewChiefStartsWhereItCanWhenTodaysChiefLeftNoDirectoryOrModel(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	directory := w.Path("gone")
	old := w.Spawn(app, fakeagent.Claude, directory)
	w.Launched(old)
	if made := setChiefOfStaff(app, old, true); !made.Success {
		t.Fatalf("making today's Chief = %+v", made)
	}
	w.Launched(old)
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	chief := turnOnCrewV2(t, w)
	if chief.Cwd != nil || protocol.Deref(chief.Agent) != "claude" {
		t.Fatalf("the new Chief = %+v, want claude in its home", chief)
	}
	woken := wakeCrew(t, w.Client(), "chief", "")
	w.Launched(string(woken.SessionID))
	if got := queriedSession(t, w.Client(), string(woken.SessionID)).Directory; got != chief.HomeDir {
		t.Fatalf("the new Chief started in %s, want its home %s", got, chief.HomeDir)
	}
}

func TestDeletingAProfileRetiresItsChiefOnlyOnceItSleeps(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	turnOnCrewV2(t, w)
	work := createProfile(app, "Work")
	selectProfile(app, work.ID)
	chief := chiefOfProfileRoster(t, w.AppOn(work.ID).Initial.Crew, work.ID)
	requestID := uuid.NewString()
	set := testworld.Request(app, protocol.CrewSetMessage{Cmd: protocol.CmdCrewSet, RequestID: protocol.Ptr(requestID), ProfileID: protocol.Ptr(work.ID), Member: "chief", Agent: protocol.Ptr("claude")}, protocol.EventCrewSetResult, func(m protocol.CrewSetResultMessage) bool { return m.RequestID == requestID })
	if !set.Success {
		t.Fatalf("configuring Work's Chief = %+v", set)
	}
	requestID = uuid.NewString()
	woken := testworld.Request(app, protocol.CrewWakeMessage{Cmd: protocol.CmdCrewWake, RequestID: protocol.Ptr(requestID), ProfileID: protocol.Ptr(work.ID), Member: "chief"}, protocol.EventCrewWakeResult, func(m protocol.CrewWakeResultMessage) bool { return m.RequestID == requestID })
	if !woken.Success {
		t.Fatalf("waking Work's Chief = %+v", woken)
	}
	run := w.Launched(string(*woken.SessionID))
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	remove := func() protocol.ProfileActionResultMessage {
		requestID := uuid.NewString()
		return testworld.Request(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: requestID, ProfileID: work.ID, ExpectedRevision: work.Revision}, protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == requestID })
	}
	if refused := remove(); refused.Success || !strings.Contains(protocol.Deref(refused.Error), "put the chief to sleep first: attn crew sleep chief") {
		t.Fatalf("deleting a profile with its Chief awake = %+v", refused)
	}
	crewHandoff(t, w.Client(), string(*woken.SessionID), "The profile is going away.", false)
	if deleted := remove(); !deleted.Success {
		t.Fatalf("deleting a profile with its Chief asleep = %+v", deleted)
	}
	w.restart()
	cli := w.Client()
	registerSessions(t, w, cli, "sender")
	if msg, err := cli.AgentMsg("member:"+chief.ID, "sender", "this profile is gone"); err == nil && msg.Status != protocol.AgentMsgStatusRefused {
		t.Fatalf("the deleted profile's Chief still takes mail: %+v", msg)
	}
	for _, member := range crewRoster(t, cli) {
		if member.ID == chief.ID {
			t.Fatalf("the deleted profile's Chief is still on the roster: %+v", member)
		}
	}
}

func TestSendingASeedToASleepingChiefMemberWakesIt(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	cli := w.Client()
	turnOnCrewV2(t, w)
	setCrew(t, cli, "chief", protocol.CrewSetMessage{Agent: protocol.Ptr("claude")})
	planted, err := cli.SeedPlant("", "Choose where this runs", "Pick the working context.", "", "")
	if err != nil {
		t.Fatal(err)
	}
	shown, err := cli.SeedShow("", planted.Seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	sent, err := cli.SeedSendToChief("", shown.Seed, "")
	if err != nil {
		t.Fatalf("sending a seed to a sleeping Chief: %v", err)
	}
	chief := chiefOfRoster(t, crewRoster(t, cli))
	if sent.Seed.Tender == nil || sent.Seed.Tender.Ref != protocol.PartyRef("member:"+chief.ID) || chief.BindingSession == nil {
		t.Fatalf("the seed went to %+v and the Chief is %+v; want the Chief member to tend it and wake", sent.Seed.Tender, chief)
	}
	w.Launched(string(*chief.BindingSession))
	if home := filepath.Dir(chief.CharterPath); home != chief.HomeDir {
		t.Fatalf("charter %s is outside home %s", chief.CharterPath, chief.HomeDir)
	}
}
