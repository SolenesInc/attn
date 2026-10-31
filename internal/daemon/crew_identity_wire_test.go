package daemon_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func newCrewV2World(t *testing.T, agents ...fakeagent.Harness) *world {
	t.Helper()
	w := newCrewWorld(t, agents...)
	setSetting(t, w.App(), "crew_v2_enabled", "true")
	return w
}

func TestACrewNameResolvesOnlyInItsProfile(t *testing.T) {
	w := newCrewV2World(t, fakeagent.Claude)
	app := w.App()
	home := crewRosterMember(t, w.Client(), "keel").ProfileID
	side := createProfile(app, "Side")
	selectProfile(app, side.ID)
	writeCrewHomeFile(t, w, "bob", crew.CharterFileName, "# Bob\n")
	w.restart()
	app = w.AppOn(side.ID)
	plain := w.Spawn(app, fakeagent.Claude, w.Path("side"))
	scoped := w.Client().WithGardenProfile("", protocol.SessionID(plain))
	sideSeed := plantSeedAs(t, w.Client(), plain, "Work for Side")
	roster, err := scoped.CrewList()
	if err != nil {
		t.Fatal(err)
	}
	members := slices.DeleteFunc(roster.Members, func(m protocol.CrewMember) bool { return protocol.Deref(m.Chief) })
	if len(members) != 1 || members[0].Name != "Bob" || members[0].ID != "bob" {
		t.Fatalf("Side roster besides its Chief: %+v", roster)
	}
	for _, name := range []string{"Keel", "Nobody", "member:keel"} {
		_, err := scoped.CrewWake(name, "", "")
		crewErrorContains(t, err, "no crew member named", `profile "Side"`)
		_, err = scoped.CrewSleep(name)
		crewErrorContains(t, err, "no crew member named", `profile "Side"`)
		_, err = scoped.CrewSet(name, protocol.Ptr(w.Path("side")), nil, nil, nil, nil)
		crewErrorContains(t, err, "no crew member named", `profile "Side"`)
		_, err = scoped.CrewRename(name, "Alfred")
		crewErrorContains(t, err, "no crew member named", `profile "Side"`)
		_, err = scoped.AgentPeek(name)
		crewErrorContains(t, err, "session_not_found")
		_, err = scoped.AgentMsg(name, protocol.SessionID(plain), "Hello")
		crewErrorContains(t, err, "no session or crew member matches")
		reloaded, err := scoped.AgentReload([]string{name}, protocol.SessionID(plain), false)
		if err != nil || len(reloaded.Outcomes) != 1 || !strings.Contains(protocol.Deref(reloaded.Outcomes[0].Error), "no session or awake crew member matches") {
			t.Fatalf("reload %s from Side: %+v %v", name, reloaded, err)
		}
		response := testworld.Request(app, protocol.CrewCharterGetMessage{Cmd: protocol.CmdCrewCharterGet, Member: name, RequestID: protocol.Ptr(name)}, protocol.EventCrewCharterGetResult, func(r protocol.CrewCharterGetResultMessage) bool { return r.RequestID == name })
		if response.Success || !strings.Contains(protocol.Deref(response.Error), `profile "Side"`) {
			t.Fatalf("foreign charter: %+v", response)
		}
	}
	for _, member := range []string{"Keel", "keel", "member:keel"} {
		_, err := scoped.SeedTransition(protocol.SessionID(plain), sideSeed, "tend", "", false, client.SeedTransitionOptions{Assignee: member})
		crewErrorContains(t, err, "no crew member named", `profile "Side"`)
	}
	if tended, err := scoped.SeedTransition(protocol.SessionID(plain), sideSeed, "tend", "", false, client.SeedTransitionOptions{Assignee: "bob"}); err != nil || tenderMemberOf(tended.Seed) != "bob" {
		t.Fatalf("tend for Bob: %+v %v", tended, err)
	}
	if _, err := scoped.CrewRename("Bob", "Robert"); err != nil {
		t.Fatal(err)
	}
	testworld.Await(app, protocol.EventCrewUpdated, func(e protocol.CrewUpdatedMessage) bool {
		for _, member := range e.Members {
			if member.ID == "bob" && member.Name == "Robert" && member.ProfileID == side.ID {
				return true
			}
		}
		return false
	})
	if got, err := w.Client().WithGardenProfile(home, "").CrewList(); err != nil || len(got.Members) != 4 || !slices.ContainsFunc(got.Members, func(m protocol.CrewMember) bool { return protocol.Deref(m.Chief) }) {
		t.Fatalf("home roster: %+v %v", got, err)
	}
}

func TestRenamingAMemberKeepsEveryReference(t *testing.T) {
	w := newCrewV2World(t, fakeagent.Claude)
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
	testworld.AwaitSession(app, string(day.SessionID), func(s protocol.Session) bool {
		return s.Label == "Alfred" && protocol.Deref(s.CrewMemberName) == "Alfred" && protocol.Deref(s.CrewMember) == "keel"
	})
	after := crewRosterMember(t, cli, "keel")
	if after.Name != "Alfred" || after.HomeDir != before.HomeDir || after.CharterPath != before.CharterPath || protocol.Deref(after.BindingSession) != day.SessionID {
		t.Fatalf("references changed: %+v -> %+v", before, after)
	}
	if _, err := cli.CrewWake("Keel", "", ""); err == nil {
		t.Fatal("the old name still resolves")
	}
	if woken, err := cli.CrewWake("member:keel", "", ""); err != nil || !woken.AlreadyAwake || woken.Name != "Alfred" {
		t.Fatalf("wake by key: %+v %v", woken, err)
	}
	priming, err := cli.CrewPrime(day.SessionID)
	if err != nil || !strings.Contains(protocol.Deref(priming.Guidance), "Alfred") || !strings.Contains(protocol.Deref(priming.Guidance), seed) {
		t.Fatalf("renamed priming: %+v %v", priming, err)
	}
	plain := w.Spawn(app, fakeagent.Claude, w.Path("sender"))
	if sent, err := cli.AgentMsg("Alfred", protocol.SessionID(plain), "same mailbox"); err != nil || sent.TargetSessionID != day.SessionID {
		t.Fatalf("message by the new name: %+v %v", sent, err)
	}
	if _, err := cli.CrewSleep("alfred"); err != nil {
		t.Fatal(err)
	}
	w.restart()
	persisted := crewRosterMember(t, w.Client(), "keel")
	if persisted.Name != "Alfred" || persisted.HomeDir != before.HomeDir {
		t.Fatalf("restart identity: %+v", persisted)
	}
	if _, err := os.Stat(filepath.Join(persisted.HomeDir, crew.HandoffsDirName, crewHomeLetters["keel"])); err != nil {
		t.Fatalf("the letter left the home: %v", err)
	}
}

func TestRenamingNeedsCrewV2(t *testing.T) {
	w := newCrewWorld(t)
	_, err := w.Client().CrewRename("Keel", "Alfred")
	crewErrorContains(t, err, "crew_v2_enabled")
	if member := crewRosterMember(t, w.Client(), "keel"); member.Name != "Keel" {
		t.Fatalf("refused rename changed the name: %+v", member)
	}
}

func TestCrewNamesFollowTheNameRules(t *testing.T) {
	w := newCrewV2World(t)
	cli := w.Client()
	cases := []struct{ name, rule string }{
		{"chief", "reserved"}, {"Chief", "reserved"}, {"attn", "reserved"}, {"user", "reserved"}, {"you", "reserved"},
		{"s-7k3f9m", "looks like an ID"}, {"m-7k3f9m", "looks like an ID"}, {"", "required"}, {"9lives", "starting with a letter"},
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
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
	cli := w.Client()
	app := w.App()
	plain := w.Spawn(app, fakeagent.Claude, w.Path("plain"))
	plainRun := w.Launched(plain)
	plainRun.Reply("Keep this conversation. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, plain, func(s protocol.Session) bool { return s.State == protocol.StateIdle })
	day := wakeCrew(t, cli, "Keel", "")
	w.Launched(string(day.SessionID))
	for _, agent := range []string{"codex", "claude"} {
		switchCrewHarness(t, cli, "Keel", agent)
		result := crewHandoff(t, cli, string(day.SessionID), "Leave this for tomorrow.", false)
		if result.SuccessorError != nil || result.SessionID == nil {
			t.Fatalf("successor: %+v", result)
		}
		day.SessionID = *result.SessionID
		run := w.Launched(string(day.SessionID))
		run.Prompted()
		run.Reply("Ready. <!-- attn:state=idle -->")
	}
	check := func() {
		all := ledger(t, cli, client.SessionListOptions{All: true})
		var memberRows int
		for _, entry := range all.Entries {
			if protocol.Deref(entry.MemberKey) == "keel" {
				memberRows++
				if protocol.Deref(entry.MemberName) != "Keel" || entry.ID != day.SessionID {
					t.Fatalf("member ledger row: %+v", entry)
				}
			}
		}
		if len(all.Entries) != 2 || memberRows != 1 || !strings.Contains(strings.Join(ledgerIDs(all), ","), plain) {
			t.Fatalf("ledger rows: %+v", all.Entries)
		}
		if closed := ledger(t, cli, client.SessionListOptions{Closed: true}).Entries; len(closed) != 0 {
			t.Fatalf("earlier days in the closed ledger: %+v", closed)
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
		if _, err := cli.CrewHandoff(day.SessionID, "Keep this day in the ledger.", false); err != nil {
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

func TestASleepingMemberKeepsItsLastDayInTheLedger(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	day := wakeCrew(t, cli, "Keel", "")
	run := w.Launched(string(day.SessionID))
	run.Prompted()
	run.Reply("A day worth keeping. <!-- attn:state=idle -->")
	crewHandoff(t, cli, string(day.SessionID), "Sleep on it.", false)
	page := ledger(t, cli, client.SessionListOptions{All: true})
	if len(page.Entries) != 1 || page.Entries[0].ID != day.SessionID || protocol.Deref(page.Entries[0].MemberKey) != "keel" || page.Entries[0].ClosedAt == nil {
		t.Fatalf("asleep member ledger: %+v", page.Entries)
	}
}

func TestAResumedCrewConversationStaysInTheOpenLedgerAfterANewWake(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	day := wakeCrew(t, cli, "Keel", "")
	run := w.Launched(string(day.SessionID))
	run.Prompted()
	run.Reply("A day worth keeping. <!-- attn:state=idle -->")
	crewHandoff(t, cli, string(day.SessionID), "Resume this conversation later.", false)
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: day.SessionID}); err != nil {
		t.Fatal(err)
	}
	w.Launched(string(day.SessionID))
	next := wakeCrew(t, cli, "Keel", "")
	w.Launched(string(next.SessionID))
	page := ledger(t, cli, client.SessionListOptions{})
	found := map[protocol.SessionID]bool{}
	for _, entry := range page.Entries {
		found[entry.ID] = true
	}
	if len(page.Entries) != 2 || !found[day.SessionID] || !found[next.SessionID] {
		t.Fatalf("resumed conversation hidden: %+v", page.Entries)
	}
}

func TestACrewRequesterRefusesAConflictingProfile(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	day := wakeCrew(t, cli, "Keel", "")
	w.Launched(string(day.SessionID))
	side := createProfile(app, "Side")
	_, err := cli.WithGardenProfile(side.ID, day.SessionID).CrewList()
	crewErrorContains(t, err, "belongs to profile", `"Default"`, `"Side"`)
	roster, err := cli.WithGardenProfile("Default", day.SessionID).CrewList()
	if err != nil || len(roster.Members) != 3 {
		t.Fatalf("matching profile name: %+v %v", roster, err)
	}
}

func TestAPlotKeepsItsPlanterAcrossCrewRenames(t *testing.T) {
	w := newCrewV2World(t, fakeagent.Claude)
	cli := w.Client()
	if _, err := cli.CrewRename("Keel", "Alfred"); err != nil {
		t.Fatal(err)
	}
	day := wakeCrew(t, cli, "Alfred", "").SessionID
	w.Launched(string(day))
	plot, err := cli.SeedPlot(day, protocol.SeedPlotMessage{Title: "renamed planter", Children: []protocol.SeedPlotChild{{Title: "its child"}}})
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
		if err != nil || planterMemberOf(shown.Seed) != "keel" {
			t.Fatalf("plot seed after another rename and restart: %+v %v", shown, err)
		}
	}
}

func TestATenderNamedByTheUserIsStoredByKey(t *testing.T) {
	w := newCrewV2World(t, fakeagent.Claude)
	cli := w.Client()
	if _, err := cli.CrewRename("Keel", "Alfred"); err != nil {
		t.Fatal(err)
	}
	app := w.App()
	planter := w.Spawn(app, fakeagent.Claude, w.Path("planter"))
	seed := plantSeedAs(t, cli, planter, "Alfred takes this")
	if tended := lifeMove(t, cli, planter, seed, "tend", "", "alfred"); tenderMemberOf(tended) != "keel" {
		t.Fatalf("tender: %+v", tended)
	}
}

func TestHomeImportAllocatesAValidUnusedName(t *testing.T) {
	w := newCrewV2World(t, fakeagent.Claude)
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
	if again := crewRosterMember(t, w.Client(), key); again.Name != member.Name || again.ID != key {
		t.Fatalf("imported identity changed on restart: %+v", again)
	}
}
