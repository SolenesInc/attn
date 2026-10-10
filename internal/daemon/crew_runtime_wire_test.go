package daemon_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestACreatedMemberWakesIntoItsFirstDay(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	caller := w.Spawn(app, fakeagent.Claude, w.Path("caller"))
	cli := w.Client().WithRequester("", protocol.SessionID(caller))
	created, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel"})
	if err != nil {
		t.Fatal(err)
	}
	m := created.Member
	if m.Name != "Keel" || !strings.HasPrefix(m.Key, "m-") || m.BindingSession != nil || m.HomeDir != filepath.Join(w.Dir, "crew", app.SelectedProfile(), m.Key) || m.CharterPath != filepath.Join(m.HomeDir, "CHARTER.md") {
		t.Fatalf("created = %+v", m)
	}
	wake := wakeCrew(t, cli, "Keel", "")
	day := w.Launched(string(wake.SessionID))
	day.Prompted()
	day.Reply("Ready. <!-- attn:state=idle -->")
	session := queriedSession(t, cli, string(wake.SessionID))
	if session.Label != "Keel" || protocol.Deref(session.CrewMember) != m.Key {
		t.Fatalf("first day = %+v", session)
	}
	if argv := strings.Join(day.Argv, " "); !strings.Contains(argv, "this is your first day") || !strings.Contains(argv, "attn crew prime") {
		t.Fatalf("first day instructions = %s", argv)
	}
	w.restart()
	if roster := w.Client().WithRequester(m.ProfileID, ""); crewRosterMember(t, roster, m.Key).Name != "Keel" {
		t.Fatal("member did not survive restart")
	}
}

func TestAMemberNameRepeatsAcrossProfilesButNotWithinOne(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	work := app.SelectedProfile()
	home := createProfile(app, "Home")
	for _, profile := range []string{work, home.ID} {
		cli := w.Client().WithRequester(profile, "")
		created, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = cli.CrewCreate(protocol.CrewCreateMessage{Name: "keel"})
		crewErrorContains(t, err, "Keel", "already exists")
		roster := crewRoster(t, cli)
		if len(roster) != 1 || roster[0].Key != created.Member.Key || roster[0].ProfileID != profile {
			t.Fatalf("roster = %+v", roster)
		}
	}
}

func TestRetiringAndRestoringAMemberKeepsItsMailAndReleasesItsClaims(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	created, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel"})
	if err != nil {
		t.Fatal(err)
	}
	key := created.Member.Key
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
	retired, err := cli.CrewRetire("Keel")
	if err != nil {
		t.Fatal(err)
	}
	if !retired.Member.Retired || retired.AlreadyRetired || len(retired.ReleasedSeeds) != 1 || retired.ReleasedSeeds[0] != seed || retired.Unread != 1 || retired.Sleep == nil || protocol.Deref(retired.Sleep.SessionID) != wake.SessionID {
		t.Fatalf("retirement = %+v", retired)
	}
	show := lifeShow(t, cli, seed)
	if show.Seed.Status != "planted" || show.Seed.TenderMember != "" || show.Seed.TenderSession != "" {
		t.Fatalf("released seed = %+v", show.Seed)
	}
	notes, err := cli.SeedNotes("", seed, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, note := range notes.Notes {
		if note.AuthorMember == "attn" && note.Body == "Keel was retired; attn released its claim." {
			found = true
		}
	}
	if !found {
		t.Fatalf("notes = %+v", notes)
	}
	refused, err := cli.AgentMsg("Keel", protocol.SessionID(sender), "new mail")
	if err != nil || refused.Status != protocol.AgentMsgStatusRefused || refused.Detail != "Keel is retired" {
		t.Fatalf("retired mail = %+v %v", refused, err)
	}
	_, err = cli.CrewWake("Keel", "", "")
	crewErrorContains(t, err, "Keel is retired")
	_, err = cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel"})
	crewErrorContains(t, err, "Keel is retired", "restore")
	if members := crewRoster(t, cli); len(members) != 0 {
		t.Fatalf("active roster = %+v", members)
	}
	all, err := cli.CrewList(true)
	if err != nil || len(all.Members) != 1 || !all.Members[0].Retired {
		t.Fatalf("all roster = %+v %v", all, err)
	}
	again, err := cli.CrewRetire("Keel")
	if err != nil || !again.AlreadyRetired || len(again.ReleasedSeeds) != 0 {
		t.Fatalf("repeat retire = %+v %v", again, err)
	}
	if _, err := cli.CrewHandoff(wake.SessionID, "Retired for now.", false, protocol.CrewDayCloseSleep); err != nil {
		t.Fatal(err)
	}
	w.restart()
	cli = w.Client()
	all, err = cli.CrewList(true)
	if err != nil || !all.Members[0].Retired || all.Members[0].BindingSession != nil {
		t.Fatalf("retirement after restart = %+v %v", all, err)
	}
	app = w.App()
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
	if crewRosterMember(t, cli, key).Name != "Keel" || lifeShow(t, cli, seed).Seed.TenderMember != "" {
		t.Fatal("restore changed name or reclaimed seed")
	}
}

func TestRetiringAMemberRemovesItsPullRequestWatch(t *testing.T) {
	serveWatchedPullRequest(t, watchedPullRequestGitHub{state: "OPEN"})
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	wake := wakeCrew(t, cli, "Keel", "")
	w.Launched(string(wake.SessionID)).Prompted()
	watchPullRequestAs(t, cli, string(wake.SessionID), protocol.PullRequestWatchModeCodex, "")
	awaitWatchedPullRequest(app, string(wake.SessionID), func(pr protocol.SessionPullRequest) bool { return protocol.Deref(pr.Watching) })
	retired, err := cli.CrewRetire("Keel")
	if err != nil || retired.RemovedWatches != 1 {
		t.Fatalf("retire watches = %+v %v", retired, err)
	}
	session := queriedSession(t, cli, string(wake.SessionID))
	if len(session.PullRequests) != 1 || protocol.Deref(session.PullRequests[0].Watching) {
		t.Fatalf("retired PR = %+v", session.PullRequests)
	}
}

func TestRetiredMembersDoNotBlockProfileDeletion(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	profile := createProfile(app, "Retire here")
	cli := w.Client().WithRequester(profile.ID, "")
	if _, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CrewRetire("Keel"); err != nil {
		t.Fatal(err)
	}
	result := testworld.Request(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, ProfileID: profile.ID, ExpectedRevision: profile.Revision, RequestID: "delete-retired"}, protocol.EventProfileActionResult, func(e protocol.ProfileActionResultMessage) bool { return e.RequestID == "delete-retired" })
	if !result.Success {
		t.Fatalf("delete = %+v", result)
	}
}
