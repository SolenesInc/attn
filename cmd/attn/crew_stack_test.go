package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func crewRoster(t *testing.T, s *testworld.Stack) map[string]protocol.CrewMember {
	t.Helper()
	var members []protocol.CrewMember
	s.Attn("crew", "list", "--json").JSON(t, &members)
	roster := map[string]protocol.CrewMember{}
	for _, member := range members {
		roster[member.Key] = member
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
		{args: []string{"crew", "restart", "keel", "--request-id", ""}, want: "--request-id cannot be empty"},
		{args: []string{"crew", "restart", "keel", "--request-id", "   "}, want: "--request-id cannot be empty"},
		{args: []string{"crew", "set", "keel"}, want: "nothing to set"},
	})

	s.Start()
	requireStdout(t, s.Attn("crew", "list"), "Chief", "asleep")
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
	if keel := roster["keel"]; protocol.Deref(keel.ResolvedAgent) != "codex" || protocol.Deref(keel.ResolvedEffort) != "high" || !slices.Equal(keel.AwarenessDirs, []string{notes, specs}) {
		t.Fatalf("keel after crew set = %+v", keel)
	}
	if trellis := roster["trellis"]; protocol.Deref(trellis.ResolvedAgent) != "claude" || trellis.Agent != nil || trellis.Model != nil || trellis.Effort != nil || len(trellis.AwarenessDirs) != 0 {
		t.Fatalf("setting keel touched trellis: %+v", trellis)
	}
	table := s.Attn("crew", "list").Stdout
	requireLines(t, "crew list", table, "MEMBER", "STATE", "AGENT", "MODEL", "EFFORT", "SESSION", "HOME")
	requireLines(t, "keel's row", crewRow(t, table, "Keel"), " asleep ", " codex ", " high ", filepath.Join(s.Dir, "crew", "keel"))
	requireLines(t, "trellis's row", crewRow(t, table, "Trellis"), " asleep ", " claude ", " "+protocol.Deref(roster["trellis"].ResolvedModel)+" ", filepath.Join(s.Dir, "crew", "trellis"))

	requireStdout(t, s.Attn("crew", "set", "keel", "--agent", "", "--effort", "", "--awareness-dir", ""), "Keel launches in - on claude", "awareness dirs: -\n")
	if keel := crewRoster(t, s)["keel"]; keel.Agent != nil || keel.Model != nil || keel.Effort != nil || protocol.Deref(keel.ResolvedAgent) != "claude" || len(keel.AwarenessDirs) != 0 {
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
	requireLines(t, "agent list", agents, "MEMBER", "Use `attn agent peek <id>` to view a session")
	requireLines(t, "trellis's agent row", crewRow(t, agents, string(day[:8])), " Trellis ")
	requireStdout(t, s.Attn("agent", "peek", "trellis"), string("session "+day), "crew member: Trellis")
	requireFailure(t, s.Attn("agent", "peek", "keel"), "agent peek: ", "Keel is asleep", "attn agent msg Keel")
	requireStdout(t, s.Attn("crew", "wake", "trellis"), string("Trellis is already awake in session "+day[:8]+"."))

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

	restarts := make([]protocol.CrewRestartResult, 2)
	for i := range restarts {
		restarted := s.Attn("crew", "restart", "keel", "--request-id", " retry-1 ", "--json")
		var receipt struct {
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal([]byte(restarted.Stderr), &receipt); restarted.Code != 0 || err != nil || receipt.RequestID != "retry-1" {
			t.Fatalf("restart exited %d with stderr %q, want the JSON receipt for retry-1", restarted.Code, restarted.Stderr)
		}
		restarted.JSON(t, &restarts[i])
	}
	first, retried := restarts[0].Restart, restarts[1].Restart
	successor := protocol.Deref(first.SuccessorSessionID)
	if first.RequestID != "retry-1" || first.State != protocol.CrewRestartStateCompleted || successor == "" ||
		retried.RequestID != first.RequestID || retried.State != first.State || protocol.Deref(retried.SuccessorSessionID) != successor {
		t.Fatalf("a retried restart = %+v then %+v, want the same completed request", first, retried)
	}
	var sessions []struct {
		ID     string `json:"id"`
		Member string `json:"member"`
	}
	s.Attn("agent", "list", "--json").JSON(t, &sessions)
	var days []string
	for _, session := range sessions {
		if session.Member == "keel" {
			days = append(days, session.ID)
		}
	}
	if !slices.Equal(days, []string{string(successor)}) {
		t.Fatalf("a retried restart left keel in sessions %q, want only %s", days, successor)
	}
	keel := s.Launched(string(successor))
	keel.Prompted()
	keel.Reply("Read the charter. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, string(successor), func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	requireStdout(t, s.Attn("crew", "sleep", "keel"), string("Asked Keel in session "+successor[:8]+" to write its handoff and file it with `attn handoff --sleep`."))

	unknown := s.Attn("crew", "restart", "nobody")
	receipt, _, _ := strings.Cut(unknown.Stderr, "\n")
	if requestID, printed := strings.CutPrefix(receipt, "crew restart request: request_id="); unknown.Code != 1 || !printed || strings.TrimSpace(requestID) == "" {
		t.Fatalf("a restart without --request-id exited %d with stderr %q, want a generated receipt first", unknown.Code, unknown.Stderr)
	}
}

func TestCrewRenameKeepsTheKeyAndShowsTheNewName(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	writeCharter(t, s, "keel")
	s.Start()
	requireStdout(t, s.Attn("crew", "rename", "Keel", "Alfred"), "Keel is now Alfred (member:keel)")
	requireStdout(t, s.Attn("crew", "list"), "Alfred")
	result := s.Attn("crew", "rename", "Alfred", "Chief")
	if result.Code != 1 || !strings.Contains(result.Stderr, "reserved") {
		t.Fatalf("rename refusal: %+v", result)
	}
	roster := crewRoster(t, s)
	if roster["keel"].Name != "Alfred" {
		t.Fatalf("roster: %+v", roster)
	}
}

func TestAnInterruptedCrewLaunchIsTheLedgerRowWhenItsConversationSurvives(t *testing.T) {
	for _, newerFinished := range []bool{false, true} {
		t.Run(map[bool]string{false: "retained", true: "newer_day_finished"}[newerFinished], func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			writeCharter(t, s, "keel")
			s.Start()
			first, err := s.Client().CrewWake("Keel", "", "")
			if err != nil {
				t.Fatal(err)
			}
			run := s.Launched(string(first.SessionID))
			run.Prompted()
			run.Reply("Done. <!-- attn:state=idle -->")
			handed, err := s.Client().CrewHandoff(first.SessionID, "A completed day.", false, protocol.CrewDayCloseSleep)
			if err != nil {
				t.Fatal(err)
			}
			if newerFinished {
				if err := os.Rename(handed.Path, handed.Path+".prior"); err != nil {
					t.Fatal(err)
				}
			}
			s.Stop()
			commit := s.PauseAt(pausepoint.MemberLaunchCommit)
			s.StartCrashingAt("member-launch-commit")
			s.App()
			done := make(chan error, 1)
			go func() { _, err := s.Client().CrewWake("Keel", "", ""); done <- err }()
			commit.Await()
			roster, err := s.Client().CrewList()
			if err != nil {
				t.Fatal(err)
			}
			var binding *protocol.SessionID
			for _, member := range roster.Members {
				if member.Name == "Keel" {
					binding = member.BindingSession
				}
			}
			if binding == nil {
				t.Fatalf("pending member launch: %+v", roster)
			}
			successor := *binding
			run = s.Launched(string(successor))
			run.Prompted()
			run.Reply("The interrupted day has a conversation. <!-- attn:state=idle -->")
			if newerFinished {
				run.Exit(0)
			}
			commit.Release()
			s.AwaitCrash()
			if err := <-done; err == nil {
				t.Fatal("crashed launch unexpectedly succeeded")
			}
			expected := successor
			if newerFinished {
				recovery := s.PauseAt(pausepoint.DaemonStartupRecovery)
				s.StartHeldAt(recovery)
				type wake struct {
					result *protocol.CrewWakeResult
					err    error
				}
				next := make(chan wake, 1)
				go func() { result, err := s.Client().CrewWake("Keel", "", ""); next <- wake{result, err} }()
				commit.Await()
				commit.Release()
				launched := <-next
				if launched.err != nil {
					t.Fatal(launched.err)
				}
				expected = launched.result.SessionID
				if _, err := s.Client().CrewHandoff(expected, "The newer completed day.", false, protocol.CrewDayCloseSleep); err != nil {
					t.Fatal(err)
				}
				recovery.Release()
			} else {
				s.Start()
			}
			s.App()
			check := func() {
				page, err := s.Client().SessionList(client.SessionListOptions{All: true})
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Entries) != 1 || page.Entries[0].ID != expected || protocol.Deref(page.Entries[0].MemberKey) != "keel" {
					t.Fatalf("recovered member ledger: %+v", page.Entries)
				}
			}
			check()
			if newerFinished {
				s.Stop()
				recovery := s.PauseAt(pausepoint.DaemonStartupRecovery)
				s.StartHeldAt(recovery)
				recovery.Release()
				s.App()
				check()
			}
		})
	}
}

func TestCrewCommandsResolveTheCallingTerminalsProfile(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	writeCharter(t, s, "keel")
	s.Start()
	app := s.App()
	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, Name: "Side", RequestID: "side"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !created.Success || created.Profile == nil {
		t.Fatalf("create Side: %+v", created)
	}
	sideApp := s.AppOn(created.Profile.ID)
	caller := s.Spawn(sideApp, fakeagent.Claude, s.Path("side"))
	terminal := sideApp.Terminal(caller)
	for _, address := range []string{caller, caller[:8]} {
		if result := s.Run(testworld.Invocation{Args: []string{"agent", "peek", address, "--json"}, Terminal: terminal}); result.Code != 0 {
			t.Fatalf("bare session peek %s with multiple profiles: %+v", address, result)
		}
	}
	var roster []protocol.CrewMember
	s.Run(testworld.Invocation{Args: []string{"crew", "list", "--json"}, Terminal: terminal}).JSON(t, &roster)
	if len(roster) != 1 || !roster[0].Chief {
		t.Fatalf("Side sees another profile's members: %+v", roster)
	}
	for _, args := range [][]string{{"crew", "sleep", "Keel"}, {"crew", "rename", "Keel", "Alfred"}, {"agent", "peek", "Keel"}} {
		result := s.Run(testworld.Invocation{Args: args, Terminal: terminal})
		if result.Code != 1 {
			t.Fatalf("foreign member command %v: %+v", args, result)
		}
		if args[0] == "crew" && !strings.Contains(result.Stderr, `profile "Side"`) {
			t.Fatalf("wrong request scope: %+v", result)
		}
	}
}

func TestCrewLifecycleCommandsAndClearPriming(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	requireRefusals(t, s, []argvRefusal{
		{args: []string{"crew", "create"}, want: "takes one member name"},
		{args: []string{"crew", "create", "Keel", "extra"}, want: "takes one member name"},
		{args: []string{"crew", "create", "Keel", "--desktop-name", "Boats"}, want: "--desktop-name needs --launch-desktop"},
		{args: []string{"crew", "retire"}, want: "takes one member name"},
		{args: []string{"crew", "restore", "Keel", "extra"}, want: "takes one member name"},
		{args: []string{"crew", "prime", "extra"}, want: "takes no arguments"},
	})
	s.Start()
	app := s.App()
	requireStdout(t, s.Attn("crew", "create", "Keel"), "Created Keel in Default (member:m-", "Home:", "attn crew wake Keel")
	var created protocol.CrewCreateResult
	s.Attn("crew", "create", "Trellis", "--agent", "claude", "--json").JSON(t, &created)
	var wake protocol.CrewWakeResult
	s.Attn("crew", "wake", "Trellis", "--json").JSON(t, &wake)
	day := s.Launched(string(wake.SessionID))
	day.Prompted()
	day.Reply("Ready. <!-- attn:state=idle -->")
	terminal := app.Terminal(string(wake.SessionID))
	app.TypeLine(string(wake.SessionID), "/clear")
	if got := day.Prompted(); got != "/clear" {
		t.Fatalf("clear = %q", got)
	}
	next := *testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && protocol.Deref(e.Session.Succeeds) == wake.SessionID
	}).Session
	if next.Label != "Trellis" || protocol.Deref(next.CrewMember) != created.Member.Key {
		t.Fatalf("successor = %+v", next)
	}
	invoke := testworld.Invocation{Args: []string{"crew", "prime"}, Terminal: terminal}
	requireStdout(t, s.Run(invoke), "You are **Trellis**", "this is your first day")
	requireFailure(t, s.Attn("crew", "prime"), "this session is not a crew member's; nothing to prime")
	s.Stop()
	s.Start()
	requireStdout(t, s.Run(invoke), "You are **Trellis**")
	roster := crewRoster(t, s)
	if protocol.Deref(roster[created.Member.Key].BindingSession) != next.ID {
		t.Fatalf("restart binding = %+v", roster)
	}
	requireStdout(t, s.Attn("crew", "retire", "Keel"), "Keel is retired.", "Released seeds: -", "Removed watches: 0", "Unread mail kept: 0")
	requireStdout(t, s.Attn("crew", "list", "--all"), "Keel", "retired")
	requireFailure(t, s.Attn("crew", "wake", "Keel"), "crew wake: ", "Keel is retired")
	requireFailure(t, s.Attn("crew", "create", "Keel"), "crew create: ", "Keel is retired", "restore")
	requireStdout(t, s.Attn("crew", "restore", "Keel"), "Keel is back in service")
	requireStdout(t, s.Attn("crew", "restore", "Keel"), "Keel is already in service")
	var retired protocol.CrewRetireResult
	s.Attn("crew", "retire", "Keel", "--json").JSON(t, &retired)
	if !retired.Member.Retired {
		t.Fatalf("retirement JSON = %+v", retired)
	}
	var restored protocol.CrewRestoreResult
	s.Attn("crew", "restore", "Keel", "--json").JSON(t, &restored)
	if restored.Member.Retired {
		t.Fatalf("restore JSON = %+v", restored)
	}
}

func TestCrewWakeWhileClearingKeepsTheSuccessor(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	writeCharter(t, s, "keel")
	s.Start()
	s.App()
	first, err := s.Client().CrewWake("Keel", "", "")
	if err != nil {
		t.Fatal(err)
	}
	day := s.Launched(string(first.SessionID))
	day.Prompted()
	day.Reply("Ready. <!-- attn:state=idle -->")
	s.Stop()
	committed := s.PauseAt(pausepoint.MemberClearCommit)
	requested := s.PauseAt(pausepoint.MemberWakeRequested)
	s.Start()
	app := s.App()
	app.TypeLine(string(first.SessionID), "/clear")
	committed.Await()
	woke := make(chan *protocol.CrewWakeResult, 1)
	errors := make(chan error, 1)
	go func() { result, err := s.Client().CrewWake("Keel", "", ""); woke <- result; errors <- err }()
	requested.Await()
	requested.Release()
	committed.Release()
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
	result := <-woke
	next := *testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && protocol.Deref(e.Session.Succeeds) == first.SessionID
	}).Session
	if !result.AlreadyAwake || result.SessionID != next.ID || protocol.Deref(next.CrewMember) != "keel" {
		t.Fatalf("wake = %+v, successor = %+v", result, next)
	}
	if got := day.Prompted(); got != "/clear" {
		t.Fatalf("clear = %q", got)
	}
}

func TestCrewLifecycleCommandsRefuseOutpostsBeforeResolvingProfiles(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	const home = "d-0123456789abcdef0123456789abcdef"
	requireStdout(t, s.Attn("enrollment", "enroll", "--home", home))
	for _, verb := range []string{"create", "retire", "restore"} {
		result := s.Attn("crew", verb, "Keel", "--profile", "Missing")
		if result.Code != 1 || !strings.Contains(result.Stderr, "outpost") || !strings.Contains(result.Stderr, "crew") || !strings.Contains(result.Stderr, home) {
			t.Fatalf("crew %s on an outpost: %+v", verb, result)
		}
	}
}
