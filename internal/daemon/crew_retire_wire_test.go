package daemon_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func createCrew(t *testing.T, cli *client.Client, name string) protocol.CrewMember {
	t.Helper()
	created, err := cli.CrewCreate(name)
	if err != nil {
		t.Fatalf("crew create %s: %v", name, err)
	}
	return created.Member
}

func retireCrew(t *testing.T, cli *client.Client, member string) *protocol.CrewRetireResult {
	t.Helper()
	retired, err := cli.CrewRetire(member)
	if err != nil {
		t.Fatalf("crew retire %s: %v", member, err)
	}
	return retired
}

func TestCreatingAMemberGivesItAHomeWithAnEmptyCharter(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	cli := w.Client()
	keel := createCrew(t, cli, "Keel")
	if keel.ID != "keel" || keel.Name != "Keel" || keel.Retired || keel.BindingSession != nil {
		t.Fatalf("created = %+v", keel)
	}
	if charter, err := os.ReadFile(keel.CharterPath); err != nil || len(charter) != 0 {
		t.Fatalf("charter = %q %v, want an empty file", charter, err)
	}
	if got := crewRosterMember(t, cli, "keel"); got.HomeDir != keel.HomeDir {
		t.Fatalf("roster = %+v", got)
	}
	_, err := cli.CrewCreate("keel")
	crewErrorContains(t, err, "Keel already exists")
	_, err = cli.CrewCreate("chief")
	crewErrorContains(t, err, `"chief" is reserved`)

	wake := wakeCrew(t, cli, "Keel", "")
	run := w.Launched(string(wake.SessionID))
	if instructions := crewLaunchFlag(run.Argv, "--append-system-prompt"); !strings.Contains(instructions, "this is your first day") {
		t.Fatalf("first wake instructions = %q", instructions)
	}
	run.Prompted()
	if err := os.WriteFile(keel.CharterPath, []byte("# Keel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	retireCrew(t, cli, "Keel")
	if err := os.RemoveAll(keel.HomeDir); err != nil {
		t.Fatal(err)
	}
	_, err = cli.CrewCreate("Keel")
	crewErrorContains(t, err, "Keel is retired", "attn crew restore Keel")
}

func TestANewMembersKeyStaysClearOfAnotherMembersHome(t *testing.T) {
	w := newWorld(t)
	writeCrewHomeFile(t, w, "fern", crew.CharterFileName, "")
	w.restart()
	cli := w.Client()
	setSetting(t, w.App(), "crew_v2_enabled", "true")
	if _, err := cli.CrewRename("fern", "Ivy"); err != nil {
		t.Fatal(err)
	}
	fern := createCrew(t, cli, "Fern")
	if fern.ID != "fern-2" {
		t.Fatalf("new Fern = %+v, want key fern-2 beside Ivy's home fern", fern)
	}
}

func TestRetiringAndRestoringAMemberKeepsItsMailAndReleasesItsClaims(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	keel := createCrew(t, cli, "Keel")
	sender := w.Spawn(app, fakeagent.Claude, w.Path("sender"))
	wake := wakeCrew(t, cli, "Keel", "")
	day := w.Launched(string(wake.SessionID))
	day.Prompted()
	seed := plantSeedAs(t, cli, string(wake.SessionID), "Keep building")
	lifeMove(t, cli, string(wake.SessionID), seed, "tend", "", "")
	message, err := cli.AgentMsg("Keel", protocol.SessionID(sender), "keep this mail")
	if err != nil || message.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("mail = %+v %v", message, err)
	}

	retired := retireCrew(t, cli, "Keel")
	if !retired.Member.Retired || retired.AlreadyRetired || len(retired.ReleasedSeeds) != 1 || retired.ReleasedSeeds[0] != seed ||
		retired.Unread != 1 || retired.Sleep == nil || protocol.Deref(retired.Sleep.SessionID) != wake.SessionID {
		t.Fatalf("retirement = %+v", retired)
	}
	if shown := lifeShow(t, cli, seed).Seed; shown.Status != "planted" || shown.TenderMember != "" || shown.TenderSession != "" {
		t.Fatalf("released seed = %+v", shown)
	}
	notes, err := cli.SeedNotes("", seed, client.SeedNotesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSeedNote(notes.Notes, crew.DaemonID, "Keel was retired, so attn released their claim.") {
		t.Fatalf("notes = %+v", notes.Notes)
	}
	refused, err := cli.AgentMsg("Keel", protocol.SessionID(sender), "new mail")
	if err != nil || refused.Status != protocol.AgentMsgStatusRefused || refused.Detail != "Keel is retired" {
		t.Fatalf("mail to a retired member = %+v %v", refused, err)
	}
	refused, err = cli.AgentMsg(string(wake.SessionID), protocol.SessionID(sender), "mail by session")
	if err != nil || refused.Status != protocol.AgentMsgStatusRefused || refused.Detail != "Keel is retired" {
		t.Fatalf("mail to a retired member's session = %+v %v", refused, err)
	}
	_, err = cli.SeedTransition(wake.SessionID, seed, "tend", "", "", false, client.SeedTransitionOptions{})
	crewErrorContains(t, err, "Keel is retired")
	_, err = cli.SeedTransition("", seed, "tend", "", "Keel", false, client.SeedTransitionOptions{})
	crewErrorContains(t, err, "Keel is retired")
	if roster := crewRoster(t, cli); len(roster) != 0 {
		t.Fatalf("active roster = %+v", roster)
	}
	if update := testworld.Await(app, protocol.EventCrewUpdated, func(e protocol.CrewUpdatedMessage) bool { return len(e.Members) == 0 }); len(update.Members) != 0 {
		t.Fatalf("app roster = %+v", update.Members)
	}
	all, err := cli.CrewListAll()
	if err != nil || len(all.Members) != 1 || !all.Members[0].Retired {
		t.Fatalf("roster with retired members = %+v %v", all, err)
	}
	again := retireCrew(t, cli, "Keel")
	if !again.AlreadyRetired || len(again.ReleasedSeeds) != 0 {
		t.Fatalf("repeat retire = %+v", again)
	}
	if _, err := cli.CrewHandoff(wake.SessionID, "Retired for now.", false, ""); err != nil {
		t.Fatal(err)
	}
	awaitClosed(app, string(wake.SessionID))
	_, err = cli.CrewWake("Keel", "", "")
	crewErrorContains(t, err, "Keel is retired", "attn crew restore Keel")

	w.restart()
	app, cli = w.App(), w.Client()
	all, err = cli.CrewListAll()
	if err != nil || len(all.Members) != 1 || !all.Members[0].Retired || all.Members[0].BindingSession != nil {
		t.Fatalf("retirement after restart = %+v %v", all, err)
	}
	restored, err := cli.CrewRestore("Keel")
	if err != nil || restored.AlreadyActive || restored.Member.Retired {
		t.Fatalf("restore = %+v %v", restored, err)
	}
	update := testworld.Await(app, protocol.EventCrewUpdated, func(e protocol.CrewUpdatedMessage) bool {
		return len(e.Members) == 1 && e.Members[0].BindingSession != nil
	})
	successor := protocol.Deref(update.Members[0].BindingSession)
	next := w.Launched(string(successor))
	next.Prompted()
	next.Reply("Ready. <!-- attn:state=idle -->")
	if prompt := next.Prompted(); !strings.Contains(prompt, inboxDoorbell) {
		t.Fatalf("kept mail notification = %s", prompt)
	}
	mail := readInbox(t, cli, string(successor), 0)
	if len(mail.Items) != 1 || mail.Items[0].Content != "keep this mail" {
		t.Fatalf("kept mail = %+v", mail)
	}
	if member := crewRosterMember(t, cli, keel.ID); member.Name != "Keel" || lifeShow(t, cli, seed).Seed.TenderMember != "" {
		t.Fatal("restore changed the name or reclaimed the seed")
	}
	if again, err := cli.CrewRestore("Keel"); err != nil || !again.AlreadyActive {
		t.Fatalf("repeat restore = %+v %v", again, err)
	}
}

func hasSeedNote(notes []protocol.SeedNote, author, body string) bool {
	for _, note := range notes {
		if note.AuthorMember == author && note.Body == body {
			return true
		}
	}
	return false
}

func TestRetiringAMemberRemovesItsGardenAndPullRequestWatches(t *testing.T) {
	serveWatchedPullRequest(t, watchedPullRequestGitHub{state: "OPEN"})
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	wake := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(wake.SessionID)).Prompted()
	watchPullRequestAs(t, cli, string(wake.SessionID), protocol.PullRequestWatchModeCodex, "")
	awaitWatchedPullRequest(app, string(wake.SessionID), func(pr protocol.SessionPullRequest) bool { return protocol.Deref(pr.Watching) })
	seed := plantSeedAs(t, cli, "", "Watch this work")
	gardenNudgeWatch(t, cli, string(wake.SessionID), seed, false)

	retired := retireCrew(t, cli, "trellis")
	if retired.RemovedWatches != 2 {
		t.Fatalf("retire watches = %+v", retired)
	}
	awaitWatchedPullRequest(app, string(wake.SessionID), func(pr protocol.SessionPullRequest) bool { return !protocol.Deref(pr.Watching) })
	if watched := gardenNudgeWatch(t, cli, string(wake.SessionID), seed, true); watched.Changed {
		t.Fatalf("the seed watch survived retirement: %+v", watched)
	}
}

func TestRetiredMembersDoNotBlockProfileDeletion(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	original := app.SelectedProfile()
	createProfile(app, "Keep")
	cli := w.Client().WithGardenProfile(original, "")
	createCrew(t, cli, "Keel")
	remove := func(id string) protocol.ProfileActionResultMessage {
		var revision int
		for _, profile := range w.AppOn(original).Initial.Profiles {
			if profile.ID == original {
				revision = profile.Revision
			}
		}
		return profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: original, ExpectedRevision: revision}, id)
	}
	if refused := remove("delete-with-crew"); refused.Success || !strings.Contains(protocol.Deref(refused.Error), "1 crew members") {
		t.Fatalf("profile with an active member: %+v", refused)
	}
	retireCrew(t, cli, "Keel")
	if deleted := remove("delete-retired"); !deleted.Success {
		t.Fatalf("profile with a retired member: %+v", deleted)
	}
}

func TestARetiredMembersLetterClosesTheDayWithoutANap(t *testing.T) {
	for _, how := range []struct {
		name  string
		close protocol.CrewDayClose
	}{
		{name: "default"}, {name: "nap", close: protocol.CrewDayCloseNap},
	} {
		t.Run(how.name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			keel := createCrew(t, cli, "Keel")
			wake := wakeCrew(t, cli, "Keel", "")
			w.Launched(string(wake.SessionID)).Prompted()
			retireCrew(t, cli, "Keel")
			filed, err := cli.CrewHandoff(wake.SessionID, "Keep my letter for a later return.", false, how.close)
			if err != nil {
				t.Fatal(err)
			}
			if protocol.Deref(filed.Outcome) != protocol.CrewDayCloseSleep || filed.SessionID != nil || filed.NapError != nil {
				t.Fatalf("retired closure = %+v", filed)
			}
			letter, err := os.ReadFile(filed.Path)
			if err != nil || !strings.Contains(string(letter), "Keep my letter for a later return.") {
				t.Fatalf("letter = %q %v", letter, err)
			}
			awaitClosed(app, string(wake.SessionID))
			all, err := cli.CrewListAll()
			if err != nil || len(all.Members) != 1 || all.Members[0].ID != keel.ID || !all.Members[0].Retired || all.Members[0].BindingSession != nil {
				t.Fatalf("retired roster = %+v %v", all, err)
			}
		})
	}
}

func TestRetiringAMemberClosesItsAsksRefusesNewOnesAndRestoringKeepsThemClosed(t *testing.T) {
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

	retired := retireCrew(t, cli, "trellis")
	if len(retired.ClosedAsks) != 1 || retired.ClosedAsks[0] != asked.ID {
		t.Fatalf("retire = %+v, want it to close %s", retired, asked.ID)
	}
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool { return len(m.Asks) == 0 })
	late := testworld.Request(app, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: "late", AskID: asked.ID, Answers: []protocol.AskAnswer{}, Note: protocol.Ptr("too late"),
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == "late" })
	if late.Success {
		t.Fatal("an ask of a retired member took an answer")
	}
	if _, err := cli.AskOpen(day.SessionID, "", cacheAsk, "", w.Path("trellis")); err == nil || !strings.Contains(err.Error(), "you are retired") {
		t.Fatalf("a retired member still awake asked again: %v, want a refusal saying it is retired", err)
	}

	if _, err := cli.CrewRestore("trellis"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if shown := w.App().Initial.Asks; len(shown) != 0 {
		t.Fatalf("after restore the app shows %+v, want no asks", shown)
	}
}
