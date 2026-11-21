package main_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAToolCallOpensABashShellRunsACommandWithoutWaitingAndReadsTheLastBlock(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "SHELL=/bin/bash")
	s.Start()
	app := s.App()
	mark := app.EventCount()
	requireStdout(t, s.Attn("settings", "set", "agent_tools_enabled", "true"), "true")
	testworld.AwaitSince(app, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["agent_tools_enabled"] == "true"
	})
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	tools := claudeToolEndpoint(t, s.LaunchedCarrying(app.Terminal(session)))

	var opened protocol.TerminalOpenResult
	tools.structured(t, "terminal_open", map[string]any{"command": []string{"printf tool-ready"}}, &opened)
	app.AwaitScreen(string(opened.SessionID), "tool-ready")
	var ran protocol.TerminalRunResult
	tools.structured(t, "terminal_run", map[string]any{"terminal": string(opened.TerminalID), "command": "printf 'ran-by-tool\\n'"}, &ran)
	if protocol.Deref(ran.TerminalID) != opened.TerminalID || ran.LastBlockID == nil || ran.Block != nil {
		t.Fatalf("terminal_run = %+v, want the shell and the block before it, with no wait", ran)
	}
	after := s.Run(testworld.Invocation{Session: session, Args: []string{"terminal", "run", string(opened.TerminalID), "printf 'after-tool\\n'", "--wait"}})
	if after.Code != 0 || strings.TrimSpace(after.Stdout) != "after-tool" {
		t.Fatalf("attn terminal run --wait after the tool's run = %+v", after)
	}
	var last protocol.TerminalReadResult
	tools.structured(t, "terminal_read", map[string]any{"terminal": string(opened.TerminalID), "mode": "last"}, &last)
	if last.Block == nil || strings.TrimSpace(last.Block.Output) != "after-tool" || protocol.Deref(last.Block.ExitCode) != 0 {
		encoded, _ := json.Marshal(last)
		t.Fatalf("terminal_read last = %s, want the finished block of the CLI's run", encoded)
	}
}

func TestTerminalSendTypesAFlagShapedTextLiterally(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "SHELL=/bin/bash")
	s.Start()
	app := s.App()
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	s.Launched(session)
	run := func(args ...string) testworld.Result {
		return s.Run(testworld.Invocation{Session: session, Args: append([]string{"terminal"}, args...)})
	}
	var opened protocol.TerminalOpenResult
	run("open", "--json").JSON(t, &opened)
	for _, text := range []string{"--keys", "--", "--keys Enter --"} {
		if got := run("send", string(opened.TerminalID), "--text", text); got.Code != 0 {
			t.Fatalf("terminal send --text %q = %+v, want the text typed as it is", text, got)
		}
		app.AwaitScreen(string(opened.SessionID), text)
		if got := run("send", string(opened.TerminalID), "--keys", "C-u"); got.Code != 0 {
			t.Fatalf("clearing the line after %q: %+v", text, got)
		}
	}
}
