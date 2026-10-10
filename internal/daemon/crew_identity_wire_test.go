package daemon_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestACrewNameResolvesOnlyInItsProfile(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	home := app.SelectedProfile()
	side := createProfile(app, "Side")
	writeCrewHomeFile(t, w, filepath.Join(side.ID, "bob"), crew.CharterFileName, "# Bob\n")
	w.restart()
	app = w.AppOn(side.ID)
	plain := w.Spawn(app, fakeagent.Claude, w.Path("side"))
	scoped := w.Client().WithRequester("", protocol.SessionID(plain))
	roster, err := scoped.CrewList()
	if err != nil || len(roster.Members) != 1 || roster.Members[0].Name != "Bob" {
		t.Fatalf("Side roster: %+v %v", roster, err)
	}
	for _, name := range []string{"Keel", "Nobody", "member:keel"} {
		_, err := scoped.CrewWake(name, "", "")
		crewErrorContains(t, err, "no crew member named", `profile "Side"`)
		_, err = scoped.CrewSleep(name)
		crewErrorContains(t, err, "no crew member named", `profile "Side"`)
		_, err = scoped.CrewSet(name, nil, nil, nil, nil, nil)
		crewErrorContains(t, err, "no crew member named", `profile "Side"`)
		_, err = scoped.CrewRestart(name, "restart-foreign")
		if err == nil {
			t.Fatalf("restart %s succeeded", name)
		}
		_, err = scoped.CrewRename(name, "Alfred")
		crewErrorContains(t, err, "no crew member named", `profile "Side"`)
		_, err = scoped.AgentPeek(name)
		crewErrorContains(t, err, "session_not_found")
		_, err = scoped.AgentMsg(name, protocol.SessionID(plain), "Hello")
		crewErrorContains(t, err, "no session or crew member matches")
		response := testworld.Request(app, protocol.CrewCharterGetMessage{Cmd: protocol.CmdCrewCharterGet, Member: name, RequestID: protocol.Ptr(name)}, protocol.EventCrewCharterGetResult, func(r protocol.CrewCharterGetResultMessage) bool { return r.RequestID == name })
		if response.Success || !strings.Contains(protocol.Deref(response.Error), `profile "Side"`) {
			t.Fatalf("foreign charter: %+v", response)
		}
	}
	if len(app.Initial.Crew) != 1 || app.Initial.Crew[0].Key != "bob" {
		t.Fatalf("app roster: %+v", app.Initial.Crew)
	}
	_, err = scoped.CrewRename("Bob", "Robert")
	if err != nil {
		t.Fatal(err)
	}
	update := testworld.Await(app, protocol.EventCrewUpdated, func(e protocol.CrewUpdatedMessage) bool {
		return e.ProfileID == side.ID && len(e.Members) == 1 && e.Members[0].Name == "Robert"
	})
	if update.Members[0].Key != "bob" {
		t.Fatalf("roster update: %+v", update)
	}
	if got := w.AppOn(home).Initial.Crew; len(got) != 3 {
		t.Fatalf("home roster: %+v", got)
	}
}

func TestRenamingAMemberKeepsEveryReference(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	app := w.App()
	day := wakeCrew(t, cli, "Keel", "")
	w.Launched(string(day.SessionID))
	before := crewRosterMember(t, cli, "keel")
	seed := plantSeedAs(t, cli, string(day.SessionID), "Remember this across a rename")
	lifeMove(t, cli, string(day.SessionID), seed, "tend", "", "")
	renamed, err := cli.CrewRename("Keel", "Alfred")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Member != "keel" || renamed.Name != "Alfred" || renamed.PreviousName != "Keel" || protocol.Deref(renamed.SessionID) != day.SessionID {
		t.Fatalf("rename: %+v", renamed)
	}
	testworld.AwaitSession(app, string(day.SessionID), func(s protocol.Session) bool { return s.Label == "Alfred" })
	after := crewRosterMember(t, cli, "keel")
	if after.HomeDir != before.HomeDir || after.CharterPath != before.CharterPath || protocol.Deref(after.BindingSession) != day.SessionID {
		t.Fatalf("references changed: %+v -> %+v", before, after)
	}
	if _, err := cli.CrewWake("Keel", "", ""); err == nil {
		t.Fatal("old name resolves")
	}
	if _, err := cli.CrewWake("member:keel", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CrewSleep("Alfred"); err != nil {
		t.Fatal(err)
	}
	priming, err := cli.CrewPrime(day.SessionID)
	if err != nil || !strings.Contains(protocol.Deref(priming.Guidance), "Alfred") || !strings.Contains(protocol.Deref(priming.Guidance), seed) {
		t.Fatalf("renamed priming: %+v %v", priming, err)
	}
	plain := w.Spawn(app, fakeagent.Claude, w.Path("sender"))
	if _, err := cli.AgentMsg("member:keel", protocol.SessionID(plain), "same mailbox"); err != nil {
		t.Fatal(err)
	}
	letters, err := os.ReadDir(filepath.Join(before.HomeDir, crew.HandoffsDirName))
	if err != nil {
		t.Fatal(err)
	}
	w.restart()
	cli = w.Client()
	persisted := crewRosterMember(t, cli, "keel")
	if persisted.Name != "Alfred" || persisted.HomeDir != before.HomeDir {
		t.Fatalf("restart identity: %+v", persisted)
	}
	for _, letter := range letters {
		if _, err := os.Stat(filepath.Join(persisted.HomeDir, crew.HandoffsDirName, letter.Name())); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCrewNamesFollowTheNameRules(t *testing.T) {
	w := newCrewWorld(t)
	cli := w.Client()
	cases := []struct{ name, rule string }{
		{"chief", "reserved"}, {"Chief", "reserved"}, {"attn", "who acts"}, {"user", "who acts"}, {"you", "who acts"},
		{"s-7k3f9m", "reads as an id"}, {"m-7k3f9m", "reads as an id"}, {"", "required"}, {"9lives", "starting with a letter"},
		{strings.Repeat("a", 41), "limit is 40 characters, asked for 41"}, {"TRELLIS", "already taken"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { _, err := cli.CrewRename("Keel", c.name); crewErrorContains(t, err, c.rule) })
	}
	result, err := cli.CrewRename("Keel", "KEEL")
	if err != nil || result.Name != "KEEL" {
		t.Fatalf("case rename: %+v %v", result, err)
	}
}

func TestAMemberIsOneLedgerRow(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	app := w.App()
	plain := w.Spawn(app, fakeagent.Claude, w.Path("plain"))
	day := wakeCrew(t, cli, "Keel", "")
	w.Launched(string(day.SessionID))
	for range 2 {
		result := crewHandoff(t, cli, string(day.SessionID), "Leave this for tomorrow.", false, protocol.CrewDayCloseNap)
		if result.NapError != nil || result.SessionID == nil {
			t.Fatalf("nap: %+v", result)
		}
		day.SessionID = *result.SessionID
		run := w.Launched(string(day.SessionID))
		run.Prompted()
		run.Reply("Ready. <!-- attn:state=idle -->")
		path := filepath.Join(crewHome(w, "keel"), crew.HandoffsDirName)
		if err := os.Rename(result.Path, filepath.Join(path, filepath.Base(result.Path)+".previous")); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		all := ledger(t, cli, client.SessionListOptions{All: true})
		if len(all.Entries) != 2 {
			t.Fatalf("ledger: %+v", all.Entries)
		}
		var memberRows int
		for _, entry := range all.Entries {
			if protocol.Deref(entry.MemberKey) == "keel" {
				memberRows++
				if protocol.Deref(entry.MemberName) != "Keel" || entry.ID != day.SessionID {
					t.Fatalf("member ledger: %+v", entry)
				}
			}
		}
		if memberRows != 1 || !strings.Contains(strings.Join(ledgerIDs(all), ","), plain) {
			t.Fatalf("ledger rows: %+v", all.Entries)
		}
		for _, entry := range ledger(t, cli, client.SessionListOptions{Closed: true}).Entries {
			if entry.MemberKey != nil {
				t.Fatalf("old day in closed ledger: %+v", entry)
			}
		}
	}
	check()
	w.restart()
	cli = w.Client()
	check()
}

func TestAFailedCrewWakeKeepsItsLastLedgerRow(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "keel")
		w.restart()
		cli := w.Client()
		day := wakeCrew(t, cli, "Keel", "")
		if _, err := cli.CrewHandoff(day.SessionID, "Keep this day in the ledger.", false, protocol.CrewDayCloseSleep); err != nil {
			t.Fatal(err)
		}
		w.terms.RefuseNextSpawn(errors.New("PTY allocation failed"))
		_, err := cli.CrewWake("Keel", "", "")
		crewErrorContains(t, err, "PTY allocation failed")
		check := func() {
			page := ledger(t, cli, client.SessionListOptions{All: true})
			if len(page.Entries) != 1 || page.Entries[0].ID != day.SessionID || protocol.Deref(page.Entries[0].MemberKey) != "keel" {
				t.Fatalf("failed wake ledger: %+v", page.Entries)
			}
		}
		check()
		w.restart()
		cli = w.Client()
		check()
	})
}

func TestAResumedCrewConversationStaysInTheOpenLedgerAfterANewWake(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	day := wakeCrew(t, cli, "Keel", "")
	run := w.Launched(string(day.SessionID))
	run.Prompted()
	run.Reply("A day worth keeping. <!-- attn:state=idle -->")
	if _, err := cli.CrewHandoff(day.SessionID, "Resume this conversation later.", false, protocol.CrewDayCloseSleep); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: day.SessionID}); err != nil {
		t.Fatal(err)
	}
	w.Launched(string(day.SessionID))
	next := wakeCrew(t, cli, "Keel", "")
	w.Launched(string(next.SessionID))
	check := func() {
		page := ledger(t, cli, client.SessionListOptions{})
		if len(page.Entries) != 2 {
			t.Fatalf("live member/conversation ledger: %+v", page.Entries)
		}
		found := map[protocol.SessionID]bool{}
		for _, entry := range page.Entries {
			found[entry.ID] = true
		}
		if !found[day.SessionID] || !found[next.SessionID] {
			t.Fatalf("resumed conversation hidden: %+v", page.Entries)
		}
	}
	check()
}

func TestACrewRequesterRefusesAConflictingProfile(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	day := wakeCrew(t, cli, "Keel", "")
	w.Launched(string(day.SessionID))
	side := createProfile(app, "Side")
	_, err := cli.WithRequester(side.ID, day.SessionID).CrewList()
	crewErrorContains(t, err, "belongs to profile", `"Default"`, "not profile")
	roster, err := cli.WithRequester("Default", day.SessionID).CrewList()
	if err != nil || len(roster.Members) != 3 {
		t.Fatalf("matching profile name: %+v %v", roster, err)
	}
}

func TestAnIncompleteLegacyHomeDoesNotImportItsWorkingDirectories(t *testing.T) {
	w := newWorld(t)
	writeCrewHomeFile(t, w, filepath.Join("keel", "project"), crew.CharterFileName, "# A project document, not a member.\n")
	w.restart()
	roster, err := w.Client().CrewList()
	if err != nil {
		t.Fatal(err)
	}
	if len(roster.Members) != 0 {
		t.Fatalf("working directory imported as crew: %+v", roster.Members)
	}
}

func TestAPlotKeepsItsPlanterAcrossCrewRenames(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	registerSessions(t, w, cli, "planter")
	if _, err := cli.CrewRename("Keel", "Alfred"); err != nil {
		t.Fatal(err)
	}
	plot, err := cli.SeedPlot("planter", "Alfred", protocol.SeedPlotMessage{Title: "renamed planter", Children: []protocol.SeedPlotChild{{Title: "its child"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CrewRename("Alfred", "Robert"); err != nil {
		t.Fatal(err)
	}
	w.restart()
	cli = w.Client()
	for _, planted := range append([]protocol.Seed{plot.Crown}, plot.Children...) {
		shown, err := cli.SeedShow("", planted.ID)
		if err != nil || shown.Seed.PlanterMember != "keel" {
			t.Fatalf("plot seed after another rename and restart: %+v %v", shown, err)
		}
	}
}

func TestHomeImportAllocatesAValidUnusedName(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	key := strings.Repeat("a", 40)
	base := "A" + key[1:]
	if _, err := cli.CrewRename("Keel", base); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CrewRename("Trellis", base[:38]+"-2"); err != nil {
		t.Fatal(err)
	}
	writeCrewHomeFile(t, w, key, crew.CharterFileName, "# A new imported member\n")
	w.restart()
	member := crewRosterMember(t, w.Client(), key)
	if member.Name != base[:38]+"-3" {
		t.Fatalf("imported member lost or misnamed: %+v", member)
	}
	if err := crew.ValidateName(member.Name); err != nil {
		t.Fatal(err)
	}
	w.restart()
	if again := crewRosterMember(t, w.Client(), key); again.Name != member.Name || again.Key != key {
		t.Fatalf("imported identity changed on restart: %+v", again)
	}
}
