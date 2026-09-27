package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
)

func TestAgentsAndShellPanesGetAttnFirstOnPathAndGhosttysTerminalIdentity(t *testing.T) {
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	stale := filepath.Join(w.Dir, "stale-attn")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "attn"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(w.Dir, "bin")
	t.Setenv("PATH", stale+string(os.PathListSeparator)+os.Getenv("PATH"))
	for key, value := range map[string]string{
		"NO_COLOR": "1", "TERM_PROGRAM": "vscode", "TERM_PROGRAM_VERSION": "1.0.0",
		"ATTN_SESSION_ID": "inherited-session", "ATTN_AGENT": "inherited-agent",
		"CLAUDE_CODE_SESSION_ID": "inherited-claude-session",
	} {
		t.Setenv(key, value)
	}
	w.start()
	app := w.App()

	agent := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	shell := w.Spawn(app, workspaceShell, w.Path("shop"))
	envFile := filepath.Join(w.Dir, "shell.env")
	app.TypeLine(shell, "env > "+envFile+"; echo dumped-$((1+1))")
	app.AwaitScreen(shell, "dumped-2")
	dumped, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}

	common := map[string]string{"TERM_PROGRAM": "ghostty", "TERM": "xterm-256color", "NO_COLOR": "", "TERM_PROGRAM_VERSION": ""}
	for name, launched := range map[string]struct {
		env  []string
		want map[string]string
	}{
		"agent": {w.Launched(agent).Env, map[string]string{"ATTN_SESSION_ID": agent, "ATTN_AGENT": "claude", "ATTN_INSIDE_APP": "1", "ATTN_SOCKET_PATH": w.Socket, "ATTN_WRAPPER_PATH": filepath.Join(active, "attn"), "CLAUDE_CODE_SESSION_ID": ""}},
		"shell": {strings.Split(strings.TrimSpace(string(dumped)), "\n"), map[string]string{"ATTN_SESSION_ID": "", "ATTN_AGENT": ""}},
	} {
		env := map[string]string{}
		for _, pair := range launched.env {
			key, value, _ := strings.Cut(pair, "=")
			env[key] = value
		}
		for key, want := range launched.want {
			if env[key] != want {
				t.Errorf("the %s started with %s=%q, want %q", name, key, env[key], want)
			}
		}
		for key, want := range common {
			if env[key] != want {
				t.Errorf("the %s started with %s=%q, want %q", name, key, env[key], want)
			}
		}
		path := filepath.SplitList(env["PATH"])
		if len(path) == 0 || path[0] != active || strings.Count(env["PATH"], active) != 1 {
			t.Errorf("the %s started with PATH %q, want the active attn directory %s first and once", name, env["PATH"], active)
		}
	}
	exitWorkspaceShells(app, shell)
}
