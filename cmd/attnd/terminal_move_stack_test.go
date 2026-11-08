package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTerminalMoveKeepsTheShellAndMovesTheCaller(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "SHELL=/bin/bash")
	s.Start()
	app := s.App()
	caller := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	s.Launched(caller)
	run := func(args ...string) testworld.Result {
		return s.Run(testworld.Invocation{Session: caller, Args: append([]string{"terminal"}, args...)})
	}
	var shell protocol.TerminalOpenResult
	opened := run("open", "--json", "--", "printf before-move")
	opened.JSON(t, &shell)
	app.AwaitScreen(string(shell.SessionID), "before-move")
	if got := run("wait", string(shell.TerminalID), "--until", "prompt"); got.Code != 0 {
		t.Fatal(got)
	}
	for _, args := range [][]string{{"move", string(shell.TerminalID), "8"}, {"move", "8"}} {
		if got := run(args...); got.Code != 0 {
			t.Fatalf("%v: %+v", args, got)
		}
	}
	var entries []protocol.TerminalEntry
	run("ls", "--all", "--json").JSON(t, &entries)
	if len(entries) != 2 {
		t.Fatalf("terminals after moving: %+v", entries)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.DesktopID, "/desktop_8") {
			t.Fatalf("terminal %s stayed on %s", entry.TerminalID, entry.DesktopID)
		}
		if entry.SessionID == shell.SessionID && entry.TerminalID != shell.TerminalID {
			t.Fatalf("shell terminal changed from %s to %s", shell.TerminalID, entry.TerminalID)
		}
	}
	if got := run("read", string(shell.TerminalID)); got.Code != 0 || !strings.Contains(got.Stdout, "before-move") {
		t.Fatalf("earlier output after moving: %+v", got)
	}
	if got := run("run", string(shell.TerminalID), "printf after-move", "--wait"); got.Code != 0 || !strings.Contains(got.Stdout, "after-move") {
		t.Fatalf("same shell after moving: %+v", got)
	}
	if got := s.Run(testworld.Invocation{Session: caller, Args: []string{"session", "move", "3"}}); got.Code != 2 || !strings.Contains(got.Stderr, `unknown command "move"`) {
		t.Fatalf("removed session move: %+v", got)
	}
	t.Logf("shell %s and caller %s on desktop 8; output and process preserved", shell.TerminalID, caller)
}

func TestTerminalOpenCreatesOnlyTheExplicitNumberedDesktop(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "SHELL=/bin/bash")
	s.Start()
	app := s.App()
	caller := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	s.Launched(caller)
	run := func(args ...string) testworld.Result {
		return s.Run(testworld.Invocation{Session: caller, Args: append([]string{"terminal"}, args...)})
	}
	var shell protocol.TerminalOpenResult
	run("open", "--desktop", "8", "--json").JSON(t, &shell)
	if !strings.HasSuffix(shell.DesktopID, "/desktop_8") {
		t.Fatalf("opened on %s, want numbered desktop 8", shell.DesktopID)
	}
	for _, args := range [][]string{{"ls", "--desktop", "9"}, {"send", string(shell.TerminalID), "--text", "never typed", "--desktop", "9"}} {
		got := run(args...)
		if got.Code != 1 || !strings.Contains(got.Stderr, "no desktop holds shortcut 9") || !strings.Contains(got.Stderr, "8 ") {
			t.Fatalf("missing desktop must be refused with a desktop list: %v: %+v", args, got)
		}
	}
	t.Logf("open created desktop 8 for terminal %s; ls and send refused missing desktop 9", shell.TerminalID)
}
