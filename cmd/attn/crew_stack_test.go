package main_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func crewRoster(t *testing.T, s *testworld.Stack) map[string]protocol.CrewMember {
	t.Helper()
	var members []protocol.CrewMember
	s.Attn("crew", "list", "--json").JSON(t, &members)
	roster := map[string]protocol.CrewMember{}
	for _, member := range members {
		roster[member.ID] = member
	}
	return roster
}

func crewRow(t *testing.T, table, name string) string {
	t.Helper()
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, name+" ") {
			return line
		}
	}
	t.Fatalf("no row for %s:\n%s", name, table)
	return ""
}

func requireStdout(t *testing.T, got testworld.Result, want ...string) {
	t.Helper()
	if got.Code != 0 {
		t.Fatalf("exited %d: %s", got.Code, got.Stderr)
	}
	requireLines(t, "stdout", got.Stdout, want...)
}

func writeCharter(t *testing.T, s *testworld.Stack, name string) {
	t.Helper()
	home := filepath.Join(s.Dir, "crew", name)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "CHARTER.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCrewMembersWakeSleepAndKeepTheirLaunchSettings(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex))
	requireRefusals(t, s, []argvRefusal{
		{args: []string{"crew", "list", "extra"}, want: "takes no arguments"},
		{args: []string{"crew", "list", "--nope"}, want: "not defined: -nope"},
		{args: []string{"crew", "wake"}, want: "takes one member name"},
		{args: []string{"crew", "wake", "one", "two"}, want: `takes one member name, not "two"`},
		{args: []string{"crew", "wake", "--nope", "keel"}, want: "not defined: -nope"},
		{args: []string{"crew", "sleep"}, want: "takes one member name"},
		{args: []string{"crew", "sleep", "one", "two"}, want: `takes one member name, not "two"`},
		{args: []string{"crew", "sleep", "keel", "--request-id", "retry-1"}, want: "not defined: -request-id"},
		{args: []string{"crew", "set", "keel"}, want: "nothing to set"},
	})

	s.Start()
	requireStdout(t, s.Attn("crew", "list"), "No crew members in this profile", "attn crew create <name>")
	s.Stop()
	for _, name := range []string{"keel", "trellis"} {
		writeCharter(t, s, name)
	}
	s.Start()
	app := s.App()

	notes, specs := s.Path("notes"), s.Path("specs")
	for _, dir := range []string{notes, specs} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	requireStdout(t, s.Attn("crew", "set", "keel", "--agent", "codex", "--effort", "high", "--awareness-dir", notes, "--awareness-dir", specs),
		"Keel launches in - on codex, model ", ", effort high\n", "awareness dirs: "+notes+", "+specs+"\n")
	roster := crewRoster(t, s)
	if keel := roster["keel"]; keel.ResolvedAgent != "codex" || protocol.Deref(keel.ResolvedEffort) != "high" || !slices.Equal(keel.AwarenessDirs, []string{notes, specs}) {
		t.Fatalf("keel after crew set = %+v", keel)
	}
	if trellis := roster["trellis"]; trellis.ResolvedAgent != "claude" || trellis.Agent != nil || trellis.Model != nil || trellis.Effort != nil || len(trellis.AwarenessDirs) != 0 {
		t.Fatalf("setting keel touched trellis: %+v", trellis)
	}
	table := s.Attn("crew", "list").Stdout
	requireLines(t, "crew list", table, "MEMBER", "STATE", "AGENT", "MODEL", "EFFORT", "SESSION", "HOME")
	requireLines(t, "keel's row", crewRow(t, table, "Keel"), " asleep ", " codex ", " high ", filepath.Join(s.Dir, "crew", "keel"))
	requireLines(t, "trellis's row", crewRow(t, table, "Trellis"), " asleep ", " claude ", " "+protocol.Deref(roster["trellis"].ResolvedModel)+" ", filepath.Join(s.Dir, "crew", "trellis"))

	requireStdout(t, s.Attn("crew", "set", "keel", "--agent", "", "--effort", "", "--awareness-dir", ""), "Keel launches in - on claude", "awareness dirs: -\n")
	if keel := crewRoster(t, s)["keel"]; keel.Agent != nil || keel.Model != nil || keel.Effort != nil || keel.ResolvedAgent != "claude" || len(keel.AwarenessDirs) != 0 {
		t.Fatalf("keel after clearing its settings = %+v", keel)
	}
	requireStdout(t, s.Attn("crew", "set", "keel", "--model", "claude-fake-sonnet"), "Keel launches in - on claude, model claude-fake-sonnet, ")
	if keel := crewRoster(t, s)["keel"]; protocol.Deref(keel.Model) != "claude-fake-sonnet" {
		t.Fatalf("keel after crew set --model = %+v", keel)
	}
	requireStdout(t, s.Attn("crew", "set", "keel", "--model", ""), "Keel launches in - on claude")
	if keel := crewRoster(t, s)["keel"]; keel.Model != nil {
		t.Fatalf("keel after crew set --model \"\" still pins %q", protocol.Deref(keel.Model))
	}

	requireStdout(t, s.Attn("crew", "wake", "trellis"), "Trellis is awake in session ")
	day := protocol.Deref(crewRoster(t, s)["trellis"].BindingSession)
	trellis := s.Launched(string(day))
	trellis.Prompted()
	requireLines(t, "trellis's row", crewRow(t, s.Attn("crew", "list").Stdout, "Trellis"), " awake ", string(" "+day[:8]+" "))
	agents := s.Attn("agent", "list").Stdout
	requireLines(t, "agent list", agents, "MEMBER", "An ID or awake MEMBER here works with `attn agent peek <target>`")
	requireLines(t, "trellis's agent row", crewRow(t, agents, string(day[:8])), " Trellis ")
	requireStdout(t, s.Attn("agent", "peek", "trellis"), string("session "+day), "crew member: this session is Trellis today")
	requireFailure(t, s.Attn("agent", "peek", "keel"), "agent peek: ", "keel is asleep", "never wakes", "`attn crew wake keel`")
	requireStdout(t, s.Attn("crew", "wake", "trellis"), string("Trellis is already awake in session "+day[:8]+" — nothing was launched."))

	trellis.Exit(143)
	testworld.AwaitSession(app, string(day), func(x protocol.Session) bool { return protocol.Deref(x.StateReason) == "process_exited" })
	requireStdout(t, s.Attn("crew", "wake", "trellis"), string("Previous session "+day[:8]+" had exited; its binding was released.\n"), "Trellis is awake in session ")
	next := protocol.Deref(crewRoster(t, s)["trellis"].BindingSession)
	if next == day || next == "" {
		t.Fatalf("trellis woke into %q after %q exited", next, day)
	}
	s.Launched(string(next)).Prompted()
	requireStdout(t, s.Attn("crew", "sleep", "trellis"), string("Sleep request for Trellis is queued in session "+next[:8]+" — "))
	requireStdout(t, s.Attn("crew", "sleep", "keel"), "Keel is already asleep", "no sleep request was sent")

	var woken protocol.CrewWakeResult
	s.Attn("crew", "wake", "keel", "--agent", "codex", "--json").JSON(t, &woken)
	if woken.Member != "keel" || woken.AlreadyAwake || woken.SessionID == "" {
		t.Fatalf("crew wake keel --agent codex --json = %+v", woken)
	}
	if codex := s.Launched(string(woken.SessionID)); codex.Harness != fakeagent.Codex {
		t.Fatalf("keel woke on %s, want codex", codex.Harness)
	} else {
		codex.Exit(143)
	}
	testworld.AwaitSession(app, string(woken.SessionID), func(x protocol.Session) bool { return protocol.Deref(x.StateReason) == "process_exited" })
}

func TestCrewNamesResolveInTheCallersProfile(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	writeCharter(t, s, "keel")
	s.Start()
	app := s.App()
	home := app.SelectedProfile()
	requireStdout(t, s.Attn("crew", "wake", "Keel"), "Keel is awake in session")
	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "side", Name: "Side"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !created.Success {
		t.Fatalf("create Side: %+v", created)
	}
	if unscoped := s.Attn("agent", "peek", "Keel"); unscoped.Code == 0 || !strings.Contains(unscoped.Stderr, "choose --profile") {
		t.Fatalf("peek with two profiles and no caller: %+v", unscoped)
	}
	requireStdout(t, s.Attn("agent", "peek", "Keel", "--profile", home), "crew member: this session is Keel today")

	homeCaller := s.Spawn(app, fakeagent.Claude, s.Path("home"))
	sideApp := s.AppOn(created.Profile.ID)
	sideCaller := s.Spawn(sideApp, fakeagent.Claude, s.Path("side"))
	asCaller := func(caller string, peer *testworld.Peer, args ...string) testworld.Result {
		return s.Run(testworld.Invocation{Args: args, Terminal: peer.Terminal(caller)})
	}
	requireStdout(t, asCaller(homeCaller, app, "agent", "peek", "keel"), "crew member: this session is Keel today")
	if peek := asCaller(sideCaller, sideApp, "agent", "peek", "Keel"); peek.Code == 0 || !strings.Contains(peek.Stderr, "no session or crew member matches") {
		t.Fatalf("peek of another profile's member: %+v", peek)
	}
	if wake := asCaller(sideCaller, sideApp, "crew", "wake", "Keel"); wake.Code == 0 || !strings.Contains(wake.Stderr, `no crew member named "Keel" in profile "Side"`) {
		t.Fatalf("wake of another profile's member: %+v", wake)
	}
	if list := asCaller(sideCaller, sideApp, "crew", "list"); list.Code != 0 || !strings.Contains(list.Stdout, "No crew members in this profile") {
		t.Fatalf("Side roster: %+v", list)
	}
}

func TestCrewCreateRetireAndRestoreCommands(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	requireRefusals(t, s, []argvRefusal{
		{args: []string{"crew", "create"}, want: "takes one member name"},
		{args: []string{"crew", "create", "Keel", "extra"}, want: "takes one member name"},
		{args: []string{"crew", "retire"}, want: "takes one member name"},
		{args: []string{"crew", "restore", "Keel", "extra"}, want: "takes one member name"},
	})
	s.Start()
	requireStdout(t, s.Attn("crew", "create", "Keel"), "Keel joined profile", "with key member:keel.", "home: ", "attn crew wake Keel")
	var created protocol.CrewMember
	s.Attn("crew", "create", "Trellis", "--json").JSON(t, &created)
	if created.ID != "trellis" || created.Name != "Trellis" {
		t.Fatalf("created = %+v", created)
	}
	requireFailure(t, s.Attn("crew", "create", "keel"), "crew create: ", "Keel already exists")

	requireStdout(t, s.Attn("crew", "retire", "Keel"), "Keel is retired.", "released seeds: -", "removed watches: 0", "closed asks: -", "unread mail kept: 0", "attn crew restore Keel")
	requireStdout(t, s.Attn("crew", "retire", "Keel"), "Keel was already retired.")
	if roster := crewRoster(t, s); len(roster) != 1 {
		t.Fatalf("list without --all = %+v", roster)
	}
	if row := crewRow(t, s.Attn("crew", "list", "--all").Stdout, "Keel"); !strings.Contains(row, "retired") {
		t.Fatalf("retired row = %q", row)
	}
	requireFailure(t, s.Attn("crew", "wake", "Keel"), "crew wake: ", "Keel is retired")
	requireFailure(t, s.Attn("crew", "create", "Keel"), "crew create: ", "Keel is retired", "attn crew restore Keel")
	requireStdout(t, s.Attn("crew", "restore", "Keel"), "Keel is back.")
	requireStdout(t, s.Attn("crew", "restore", "Keel"), "Keel was not retired; nothing changed.")

	var retired protocol.CrewRetireResult
	s.Attn("crew", "retire", "Keel", "--json").JSON(t, &retired)
	if !retired.Member.Retired || retired.AlreadyRetired {
		t.Fatalf("retirement JSON = %+v", retired)
	}
	var restored protocol.CrewRestoreResult
	s.Attn("crew", "restore", "Keel", "--json").JSON(t, &restored)
	if restored.Member.Retired || restored.AlreadyActive {
		t.Fatalf("restore JSON = %+v", restored)
	}
}
