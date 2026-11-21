package main_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
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
	if atPrompt := s.Run(testworld.Invocation{Session: session, Args: []string{"terminal", "wait", string(opened.TerminalID), "--until", "prompt"}}); atPrompt.Code != 0 {
		t.Fatalf("attn terminal wait --until prompt after the opened command: %+v", atPrompt)
	}
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

func TestATerminalRunWhoseTimeoutPassesBeforeItTypesNeverTypes(t *testing.T) {
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
	run("open", "--json", "--", "printf shell-ready").JSON(t, &opened)
	app.AwaitScreen(string(opened.SessionID), "shell-ready")
	terminal := string(opened.TerminalID)
	if got := run("run", terminal, "printf 'busy\\n'; sleep 30"); got.Code != 0 {
		t.Fatalf("start a long command: %+v", got)
	}
	app.AwaitScreen(string(opened.SessionID), "busy\r\n")
	if got := run("run", terminal, "printf 'late-%s\\n' typed", "--timeout", "0.5"); got.Code != 124 {
		t.Fatalf("run while the shell is busy with --timeout 0.5 = %+v, want exit 124", got)
	}
	if got := run("send", terminal, "--keys", "C-c"); got.Code != 0 {
		t.Fatalf("interrupt: %+v", got)
	}
	done := run("run", terminal, "printf 'after-%s\\n' interrupt", "--wait")
	if done.Code != 0 || strings.TrimSpace(done.Stdout) != "after-interrupt" {
		t.Fatalf("run --wait after the interrupt = %+v", done)
	}
	if screen := run("read", terminal, "--scrollback"); strings.Contains(screen.Stdout, "late-") {
		t.Fatalf("the command whose timeout passed was typed after all:\n%s", screen.Stdout)
	}
}

func TestTerminalToolsAnswerWithoutWaitingOnTheShell(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude), testworld.OnPTYBackend(testworld.WorkerPTY))
	s.Vars = append(s.Vars, "SHELL=/bin/bash")
	waiting := s.PauseEvery(pausepoint.PtyWaitBlock)
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
	waiting.Await()
	waiting.Release()
	app.AwaitScreen(string(opened.SessionID), "tool-ready")
	shell := string(opened.TerminalID)
	atPrompt := s.Launch(testworld.Invocation{Session: session, Args: []string{"terminal", "wait", shell, "--until", "prompt"}})
	waiting.Await()
	waiting.Release()
	if result := atPrompt.Wait(); result.Code != 0 {
		t.Fatalf("attn terminal wait --until prompt after the opened command: %+v", result)
	}

	var reading protocol.TerminalRunResult
	tools.structured(t, "terminal_run", map[string]any{"terminal": shell, "command": "IFS= read -r line"}, &reading)
	busy := tools.call(t, "terminal_run", map[string]any{"terminal": shell, "command": "printf never-typed"})
	if !busy.IsError || !strings.Contains(busy.Text, "prompt rule: ") {
		t.Fatalf("terminal_run while the shell runs the tool's earlier command = %+v, want a prompt rule refusal at once", busy)
	}
	tools.structured(t, "terminal_send", map[string]any{"terminal": shell, "keys": []string{"C-c"}}, &protocol.TerminalInputResult{})
}
