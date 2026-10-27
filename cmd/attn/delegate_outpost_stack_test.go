package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestDelegationAndTerminalOpenDefaultToTheCallersMachine(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	home.Vars = append(home.Vars, "SHELL=/bin/bash")
	outpost.Vars = append(outpost.Vars, "SHELL=/bin/bash")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	for _, name := range []string{"home", "Home"} {
		pair.App.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: name, Address: pair.Address})
		refused := testworld.Await(pair.App, protocol.EventCommandError, func(e protocol.CommandErrorMessage) bool { return protocol.Deref(e.Cmd) == protocol.CmdAddOutpost })
		if !strings.Contains(refused.Error, "reserved") || !strings.Contains(refused.Error, name) {
			t.Fatalf("reserved outpost name %s: %s", name, refused.Error)
		}
	}
	source := home.Spawn(pair.App, fakeagent.Claude, outpost.Path("source"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	outpost.LaunchedCarrying(pair.App.Terminal(source))
	opened := outpost.Run(testworld.Invocation{Terminal: pair.App.Terminal(source), Args: []string{"terminal", "open", "--json"}})
	if opened.Code != 0 {
		t.Fatalf("terminal open: %+v", opened)
	}
	var terminal protocol.TerminalOpenResult
	opened.JSON(t, &terminal)
	session := testworld.AwaitSession(pair.App, string(terminal.SessionID), func(s protocol.Session) bool { return true })
	if protocol.Deref(session.Host) != pair.Host {
		t.Fatalf("terminal open host = %q; want caller's %q", protocol.Deref(session.Host), pair.Host)
	}
	if session.Directory != outpost.Path("source") {
		t.Fatalf("default shell cwd=%s, want caller cwd=%s", session.Directory, outpost.Path("source"))
	}
	forcedHome := outpost.Run(testworld.Invocation{Terminal: pair.App.Terminal(source), Args: []string{"terminal", "open", "--host", "home", "--json"}})
	if forcedHome.Code != 0 {
		t.Fatal(forcedHome)
	}
	var homeTerminal protocol.TerminalOpenResult
	forcedHome.JSON(t, &homeTerminal)
	local := testworld.AwaitSession(pair.App, string(homeTerminal.SessionID), func(s protocol.Session) bool { return true })
	if protocol.Deref(local.Host) != "" {
		t.Fatalf("--host home shell host=%q", protocol.Deref(local.Host))
	}
	started := outpost.Run(testworld.Invocation{Terminal: pair.App.Terminal(source), Args: []string{"delegate", "--brief", "Work beside the caller", "--cwd", outpost.Path("source"), "--model", "default"}})
	if started.Code != 0 {
		t.Fatalf("delegate: %+v", started)
	}
	var result delegated
	started.JSON(t, &result)
	session = testworld.AwaitSession(pair.App, result.SessionID, func(s protocol.Session) bool { return true })
	if protocol.Deref(session.Host) != pair.Host {
		t.Fatalf("delegate host = %q; want caller's %q", protocol.Deref(session.Host), pair.Host)
	}
	if prompt := outpost.LaunchedCarrying(pair.App.Terminal(result.SessionID)).Prompted(); !strings.Contains(prompt, result.SeedID) {
		t.Fatalf("delegate prompt = %q; want seed %s", prompt, result.SeedID)
	}
	t.Logf("outpost caller=%s: terminal=%s delegate=%s host=%s", source, terminal.TerminalID, result.SessionID, pair.Host)
}

func TestDelegationCheckoutOccupancyIsScopedToItsMachine(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	repo := outpost.Path("shop")
	gitRepo(t, repo)
	local := home.Spawn(pair.App, fakeagent.Claude, repo)
	home.Launched(local)
	request := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "same-path-other-machine", Host: &pair.Host, Cwd: repo, Agent: protocol.Ptr("claude"),
		Checkout:   &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: "main"},
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Share the path, on another machine")},
	}
	remote, err := home.Client().Delegate(request)
	if err != nil {
		t.Fatalf("home occupant must not block the outpost checkout: %v", err)
	}
	outpost.LaunchedCarrying(pair.App.Terminal(string(remote.SessionID))).Prompted()
	request.RequestID = "same-path-same-machine"
	_, err = home.Client().Delegate(request)
	if err == nil || !strings.Contains(err.Error(), string(remote.SessionID)) || strings.Contains(err.Error(), local) {
		t.Fatalf("same-machine occupancy error=%v; want only outpost occupant %s", err, remote.SessionID)
	}
	t.Logf("home occupant=%s ignored on outpost; target occupant=%s refused: %v", local, remote.SessionID, err)
}

func TestDelegationRelativeCwdUsesTheCallerDirectoryWhenItSelectsItsOwnHost(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	for _, row := range []struct {
		stack *testworld.Stack
		host  string
	}{{home, "home"}, {outpost, "outpost"}} {
		repo := row.stack.Path("relative")
		gitRepo(t, repo)
		caller := home.Spawn(pair.App, fakeagent.Claude, row.stack.Path("caller"), func(m *protocol.SpawnSessionMessage) {
			if row.host != "home" {
				m.Host = &pair.Host
			}
		})
		row.stack.LaunchedCarrying(pair.App.Terminal(caller))
		started := row.stack.Run(testworld.Invocation{Terminal: pair.App.Terminal(caller), Dir: repo,
			Args: []string{"delegate", "--brief", "Use the caller's relative cwd", "--host", row.host, "--cwd", ".", "--model", "default", "--new-worktree", "--branch", "feat/relative", "--from", "main"}})
		if started.Code != 0 {
			t.Fatalf("%s relative cwd: %+v", row.host, started)
		}
		var result delegated
		started.JSON(t, &result)
		if want := row.stack.Path("relative--feat-relative"); result.Directory != want {
			t.Fatalf("%s relative cwd=%s, want %s", row.host, result.Directory, want)
		}
		row.stack.LaunchedCarrying(pair.App.Terminal(result.SessionID)).Prompted()
		t.Logf("explicit %s resolves . from caller %s: %s", row.host, repo, result.Directory)
	}
}

func TestDelegationSelectsTheTargetMachineAndCreatesItsWorktreeThere(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	remoteSource := home.Spawn(pair.App, fakeagent.Claude, outpost.Path("source"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	outpost.LaunchedCarrying(pair.App.Terminal(remoteSource))
	homeSource := home.Spawn(pair.App, fakeagent.Claude, home.Path("source"))
	home.Launched(homeSource)
	notes := home.Path("notes")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := outpost.Path("shop")
	gitRepo(t, repo)
	if err := os.MkdirAll(filepath.Join(repo, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "web", "index.html"), []byte("shop"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "web"}, {"-c", "user.name=attn", "-c", "user.email=attn@example.invalid", "commit", "-qm", "web"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	for _, row := range []struct {
		name, source, host, cwd, wantHost, wantCwd string
		caller                                     *testworld.Stack
		checkout                                   []string
	}{
		{name: "remote to home", source: remoteSource, host: "home", cwd: notes, wantCwd: notes, caller: outpost},
		{name: "home to outpost", source: homeSource, host: "outpost", cwd: filepath.Join(repo, "web"), wantHost: pair.Host,
			wantCwd: outpost.Path("shop--feat-target", "web"), caller: home, checkout: []string{"--new-worktree", "--branch", "feat/target", "--from", "main"}},
	} {
		args := append([]string{"delegate", "--brief", row.name, "--host", row.host, "--cwd", row.cwd, "--model", "default"}, row.checkout...)
		started := row.caller.Run(testworld.Invocation{Terminal: pair.App.Terminal(row.source), Args: args})
		if started.Code != 0 {
			t.Fatalf("%s: %+v", row.name, started)
		}
		var result delegated
		started.JSON(t, &result)
		session := testworld.AwaitSession(pair.App, result.SessionID, func(s protocol.Session) bool { return true })
		if protocol.Deref(session.Host) != row.wantHost || result.Directory != row.wantCwd {
			t.Fatalf("%s: host=%q cwd=%q, want host=%q cwd=%q", row.name, protocol.Deref(session.Host), result.Directory, row.wantHost, row.wantCwd)
		}
		row.caller = home
		if row.wantHost != "" {
			row.caller = outpost
		}
		if prompt := row.caller.LaunchedCarrying(pair.App.Terminal(result.SessionID)).Prompted(); !strings.Contains(prompt, result.SeedID) {
			t.Fatalf("%s prompt=%q", row.name, prompt)
		}
		t.Logf("%s: session=%s host=%q cwd=%s checkout=%s", row.name, result.SessionID, protocol.Deref(session.Host), result.Directory, result.Checkout)
	}
	listed, err := outpost.Client().WorktreeList("", 0)
	if err != nil || len(listed.Worktrees) != 1 || listed.Worktrees[0].Path != outpost.Path("shop--feat-target") {
		t.Fatalf("target worktree registry: %+v, %v", listed, err)
	}
	listed, err = home.Client().WorktreeList("", 0)
	if err != nil || len(listed.Worktrees) != 0 {
		t.Fatalf("home worktree registry: %+v, %v", listed, err)
	}
	for _, host := range []string{"missing-host", "outpost"} {
		if host == "outpost" {
			requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"), "false")
		}
		operation, err := home.Client().StartDelegation(protocol.DelegateMessage{
			Cmd: protocol.CmdDelegate, RequestID: "refused-" + host, Host: &host, Cwd: repo, Agent: protocol.Ptr("claude"),
			Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Must not be accepted")},
		})
		if operation != nil || err == nil || !strings.Contains(err.Error(), host) {
			t.Fatalf("host=%s accepted=%+v error=%v", host, operation, err)
		}
		t.Logf("accept refusal: %s", err)
	}
}

func TestAnInterruptedDelegationKeepsItsOutpostAcrossAHomeRestart(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	source := home.Spawn(pair.App, fakeagent.Claude, outpost.Path("source"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	outpost.LaunchedCarrying(pair.App.Terminal(source))
	boot := outpost.HoldNextBoot()
	request := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "restart-on-outpost", SourceSessionID: protocol.Ptr(protocol.SessionID(source)),
		Cwd: outpost.Path("source"), Agent: protocol.Ptr("claude"),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Keep this launch on the caller's machine")},
	}
	accepted, err := home.Client().StartDelegation(request)
	if err != nil {
		t.Fatal(err)
	}
	outpost.AwaitHeldBoot()
	home.Stop()
	home.Start()
	pair.App = home.App()
	outpostStateOnConnect(pair.App, "outpost", protocol.OutpostStateConnected)
	boot()
	result, err := home.Client().Delegate(request)
	if err != nil || result.SessionID != accepted.SessionID {
		t.Fatalf("recovered delegation=%+v, %v; want session %s", result, err, accepted.SessionID)
	}
	session := testworld.AwaitSession(pair.App, string(result.SessionID), func(s protocol.Session) bool { return true })
	if protocol.Deref(session.Host) != pair.Host {
		t.Fatalf("recovered host=%q; want %q", protocol.Deref(session.Host), pair.Host)
	}
	if prompt := outpost.LaunchedCarrying(pair.App.Terminal(string(result.SessionID))).Prompted(); !strings.Contains(prompt, result.SeedID) {
		t.Fatalf("recovered prompt=%q", prompt)
	}
	t.Logf("home restart resumed session=%s on host=%s", result.SessionID, pair.Host)
}

func TestADelegationNamesItsCheckoutIfItDisappearsBeforeTheMachineLaunches(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	starting := outpost.PauseAt(pausepoint.TerminalStarting)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	repo := outpost.Path("shop")
	gitRepo(t, repo)
	request := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "removed-before-launch", Host: &pair.Host, Cwd: repo, Agent: protocol.Ptr("claude"),
		Checkout:   &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feat/removed", From: protocol.Ptr("main")},
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Work in the prepared checkout")},
	}
	if _, err := home.Client().StartDelegation(request); err != nil {
		t.Fatal(err)
	}
	starting.Await()
	path := outpost.Path("shop--feat-removed")
	cmd := exec.Command("git", "worktree", "remove", "--force", path)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("remove checkout before launch: %v: %s", err, out)
	}
	starting.Release()
	_, err := home.Client().Delegate(request)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "sweep") {
		t.Fatalf("launch after checkout removal: %v", err)
	}
	t.Logf("removed checkout launch refused: %s", err)
}
