package daemon_test

import (
	"context"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAnAgentOpensTypesIntoReadsMovesAndClosesAShellWithTheTerminalTools(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	tools := listOfferedTools(t, agent)
	shapes := newToolShapes(t, agent, tools)
	if _, offered := tools["terminal_wait"]; offered {
		t.Error("terminal_wait is offered as a tool, but it holds the call with no limit of its own")
	}

	open := shapes.call("terminal_open", map[string]any{"cwd": w.Path("shop")})
	if open.IsError {
		t.Fatalf("terminal_open refused: %s", resultText(open))
	}
	opened := narrowed[protocol.TerminalOpenResult](t, open.StructuredContent)
	shell := string(opened.TerminalID)
	listed := narrowed[protocol.TerminalListResult](t, shapes.call("terminal_ls", map[string]any{}).StructuredContent)
	if !strings.Contains(entryIDs(listed.Terminals), shell) {
		t.Errorf("terminal_ls = %+v, want the opened shell", listed)
	}
	typed := narrowed[protocol.TerminalInputResult](t, shapes.call("terminal_send", map[string]any{"terminal": shell, "text": "typed-by-tool"}).StructuredContent)
	if typed.TerminalID != opened.TerminalID {
		t.Errorf("terminal_send = %+v, want the shell", typed)
	}
	app.AwaitScreen(string(opened.SessionID), "typed-by-tool")
	screen := narrowed[protocol.TerminalReadResult](t, shapes.call("terminal_read", map[string]any{"terminal": shell}).StructuredContent)
	if !strings.Contains(screen.Text, "typed-by-tool") {
		t.Errorf("terminal_read = %+v, want the typed text on the screen", screen)
	}
	ran := narrowed[protocol.TerminalRunResult](t, shapes.call("terminal_run", map[string]any{"terminal": shell, "command": "printf ran-by-tool"}).StructuredContent)
	if protocol.Deref(ran.TerminalID) != opened.TerminalID || ran.Block != nil {
		t.Errorf("terminal_run = %+v, want the shell, without waiting for the command", ran)
	}
	app.AwaitScreen(string(opened.SessionID), "ran-by-tool")

	sent := narrowed[protocol.TerminalInputResult](t, shapes.call("terminal_send", map[string]any{"terminal": shell, "keys": []any{"C-c"}}).StructuredContent)
	if sent.TerminalID != opened.TerminalID {
		t.Errorf("terminal_send = %+v, want the shell", sent)
	}
	moved := narrowed[protocol.TerminalMoveResult](t, shapes.call("terminal_move", map[string]any{"terminal": shell, "desktop": "7"}).StructuredContent)
	if moved.TerminalID != opened.TerminalID || !protocol.Deref(moved.DesktopCreated) {
		t.Errorf("terminal_move = %+v, want the shell on a new desktop 7", moved)
	}
	terminalDo[protocol.TerminalMoveResult](t, w.Client(), "terminal move", protocol.SessionID(session), map[string]any{"terminal": shell, "desktop": "1"})
	closed := narrowed[protocol.TerminalCloseResult](t, shapes.call("terminal_close", map[string]any{"terminal": shell}).StructuredContent)
	if closed.TerminalID != opened.TerminalID {
		t.Errorf("terminal_close = %+v, want the shell", closed)
	}

	refused := agent.mustCall(t, "terminal_send", map[string]any{"terminal": session, "keys": []any{"Bogus"}})
	if text := resultText(refused); !refused.IsError || !strings.HasPrefix(text, "unknown_key: ") || !strings.Contains(text, "\nFix: ") {
		t.Errorf("terminal_send of an unknown key = %q, want the unknown_key refusal with a fix", text)
	}
	shapes.finish("terminal_")
}

func TestTerminalCommandsReachNothingInAnotherProfile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	home := w.Spawn(app, fakeagent.Claude, w.Path("home"))
	work := w.AppOn(createProfile(app, "Work").ID)
	stranger := w.Spawn(work, fakeagent.Claude, w.Path("work"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(home))}
	foreign := work.Terminal(stranger)
	for _, tc := range []struct {
		tool, command string
		args          map[string]any
	}{
		{"terminal_read", "terminal read", map[string]any{"terminal": foreign}},
		{"terminal_send", "terminal send", map[string]any{"terminal": foreign, "text": "x"}},
		{"terminal_move", "terminal move", map[string]any{"terminal": foreign, "desktop": "8"}},
		{"terminal_close", "terminal close", map[string]any{"terminal": foreign}},
	} {
		result := agent.mustCall(t, tc.tool, tc.args)
		if text := resultText(result); !result.IsError || !strings.Contains(text, "not found in the caller's profile") {
			t.Errorf("/mcp %s on another profile's terminal = %q, want it not found", tc.tool, text)
		}
		_, err := client.TerminalCommand[map[string]any](context.Background(), w.Client(), tc.command, protocol.SessionID(home), tc.args)
		if err == nil || !strings.Contains(err.Error(), "not found in the caller's profile") {
			t.Errorf("run_command %s on another profile's terminal = %v, want it not found", tc.command, err)
		}
	}
	listed := narrowed[protocol.TerminalListResult](t, agent.mustCall(t, "terminal_ls", map[string]any{"all": true}).StructuredContent)
	if strings.Contains(entryIDs(listed.Terminals), foreign) {
		t.Errorf("terminal_ls --all listed another profile's terminal %s: %+v", foreign, listed)
	}
}

func entryIDs(entries []protocol.TerminalEntry) string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = string(entry.TerminalID)
	}
	return strings.Join(ids, " ")
}
