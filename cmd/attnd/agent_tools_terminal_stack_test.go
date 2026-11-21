package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
	claude := s.LaunchedCarrying(app.Terminal(session))
	tools := claudeToolEndpoint(t, claude)

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

	var closing protocol.TerminalOpenResult
	tools.structured(t, "terminal_open", map[string]any{"command": []string{"printf never-typed"}}, &closing)
	waiting.Await()
	tools.structured(t, "terminal_close", map[string]any{"terminal": string(closing.TerminalID)}, &protocol.TerminalCloseResult{})
	waiting.Release()
	if ring := claude.Prompted(); !strings.Contains(ring, "attn agent inbox") {
		t.Fatalf("the opener was rung with %q, want its inbox named", ring)
	}
	inbox := s.Run(testworld.Invocation{Session: session, Args: []string{"agent", "inbox"}})
	if inbox.Code != 0 || !strings.Contains(inbox.Stdout, "The command you opened terminal "+string(closing.TerminalID)+" with was not typed") {
		t.Fatalf("the opener's inbox after its terminal closed before the first prompt = %+v, want the not-typed notice", inbox)
	}
}

func TestACommandTerminalOpenAcceptedBeforeADaemonRestartIsNoticedNotTyped(t *testing.T) {
	for _, backend := range testworld.WorkerAndSharedHost {
		t.Run(string(backend), func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude), testworld.OnPTYBackend(backend))
			bash := filepath.Join(s.Dir, "bash")
			const heldUntilEnter = `#!/bin/sh
if [ -n "${ATTN_SHELL_INIT_DIR-}" ]; then
    printf 'held-before-the-first-prompt\n'
    IFS= read -r release
fi
exec /bin/bash "$@"
`
			if err := os.WriteFile(bash, []byte(heldUntilEnter), 0o755); err != nil {
				t.Fatal(err)
			}
			s.Vars = append(s.Vars, "SHELL="+bash)
			s.Start()
			app := s.App()
			mark := app.EventCount()
			requireStdout(t, s.Attn("settings", "set", "agent_tools_enabled", "true"), "true")
			testworld.AwaitSince(app, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
				return m.Settings["agent_tools_enabled"] == "true"
			})
			session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
			tools := claudeToolEndpoint(t, s.LaunchedCarrying(app.Terminal(session)))
			const accepted = "printf '%s-%s\\n' accepted ran"
			var opened protocol.TerminalOpenResult
			tools.structured(t, "terminal_open", map[string]any{"command": []string{accepted}}, &opened)
			app.AwaitScreen(string(opened.SessionID), "held-before-the-first-prompt")

			s.Signal(syscall.SIGTERM)
			s.AwaitStopped()
			s.Start()
			app = s.App()
			run := func(args ...string) testworld.Result {
				return s.Run(testworld.Invocation{Session: session, Args: args})
			}
			terminal := string(opened.TerminalID)
			inbox := run("agent", "inbox")
			if inbox.Code != 0 || !strings.Contains(inbox.Stdout, "The command you opened terminal "+terminal+" with was not typed: the daemon restarted before attn typed "+accepted) {
				t.Fatalf("the opener's inbox after a restart before the first prompt = %+v, want the not-typed notice naming the terminal and the command", inbox)
			}

			app.AwaitScreen(string(opened.SessionID), "held-before-the-first-prompt")
			if got := run("terminal", "send", terminal, "--keys", "Enter"); got.Code != 0 {
				t.Fatalf("releasing the first prompt: %+v", got)
			}
			if got := run("terminal", "run", terminal, "printf '%s-%s\\n' shell survived", "--wait"); got.Code != 0 || strings.TrimSpace(got.Stdout) != "shell-survived" {
				t.Fatalf("a command in the recovered shell = %+v", got)
			}
			if screen := run("terminal", "read", terminal, "--scrollback"); screen.Code != 0 || strings.Contains(screen.Stdout, "accepted-ran") {
				t.Fatalf("the recovered shell's scrollback = %+v, want the accepted command never typed", screen)
			}
		})
	}
}

func TestACommandTerminalOpenWasTypingWhenTheDaemonRestartedIsNoticedAsMayHaveRun(t *testing.T) {
	for _, backend := range testworld.WorkerAndSharedHost {
		t.Run(string(backend), func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude), testworld.OnPTYBackend(backend))
			s.Vars = append(s.Vars, "SHELL=/bin/bash")
			typed := s.PauseAt(pausepoint.TerminalOpenCommandTyped)
			s.Start()
			app := s.App()
			mark := app.EventCount()
			requireStdout(t, s.Attn("settings", "set", "agent_tools_enabled", "true"), "true")
			testworld.AwaitSince(app, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
				return m.Settings["agent_tools_enabled"] == "true"
			})
			session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
			tools := claudeToolEndpoint(t, s.LaunchedCarrying(app.Terminal(session)))
			appended := filepath.Join(s.Dir, "appended")
			accepted := "printf 'appended\\n' >> " + appended
			var opened protocol.TerminalOpenResult
			tools.structured(t, "terminal_open", map[string]any{"command": []string{accepted}}, &opened)
			typed.Await()

			s.Signal(syscall.SIGTERM)
			s.AwaitStopped()
			s.Start()
			run := func(args ...string) testworld.Result {
				return s.Run(testworld.Invocation{Session: session, Args: args})
			}
			terminal := string(opened.TerminalID)
			inbox := run("agent", "inbox")
			want := "The daemon restarted while attn was typing the command you opened terminal " + terminal + " with, so the command may have run: " + accepted + "."
			if inbox.Code != 0 || !strings.Contains(inbox.Stdout, want) || strings.Contains(inbox.Stdout, "was not typed") {
				t.Fatalf("the opener's inbox after a restart while attn typed the command = %+v, want the notice that it may have run", inbox)
			}
			if got := run("terminal", "wait", terminal, "--until", "prompt"); got.Code != 0 {
				t.Fatalf("waiting for the recovered shell's prompt: %+v", got)
			}
			if got, err := os.ReadFile(appended); err != nil || string(got) != "appended\n" {
				t.Fatalf("the file the command appends to = %q (%v), want one line: the command ran once and was not replayed", got, err)
			}
		})
	}
}
