package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAShellPaneInheritsNeitherTheClaudeSessionThatStartedTheDaemonNorItsWorkersTransport(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.StartWith("CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=inherited-claude-session", "CLAUDE_EFFORT=max")
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	envFile := filepath.Join(s.Dir, "shell.env")
	app.TypeLine(shell, "env > "+envFile+"; echo dumped-$((1+1))")
	app.AwaitScreen(shell, "dumped-2")
	dumped, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range strings.Split(string(dumped), "\n") {
		switch key, _, _ := strings.Cut(pair, "="); key {
		case "CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_EFFORT":
			t.Errorf("a shell pane inherited %s from the Claude session that started the daemon", pair)
		case "ATTN_PTY_WORKER", "ATTN_CACHED_SHELL_ENV", "ATTN_PTY_EXTERNAL_ENV", "ATTN_PTY_DAEMON_ENV":
			t.Errorf("a shell pane inherited its worker's transport variable %s", pair)
		}
	}
}
