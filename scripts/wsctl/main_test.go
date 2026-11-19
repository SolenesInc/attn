package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestMain(m *testing.M) {
	os.Exit(testworld.Main(m))
}

func TestResolveWSURLNeverImplicitProd(t *testing.T) {
	prod := "ws://localhost:" + config.WSPortForInstance("") + "/ws"
	for _, instance := range []string{"", "default", "DEFAULT", " ", "dev", "mdclick1"} {
		if got := resolveWSURL("", instance); got == prod {
			t.Fatalf("resolveWSURL(\"\", %q) resolved to prod %q; prod must require an explicit ATTN_WS_URL", instance, got)
		}
	}
}

func TestInputAndScreenReachTheTerminalOfTheSessionNamed(t *testing.T) {
	s := testworld.NewStack(t)
	s.Start()
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	token, err := os.ReadFile(filepath.Join(s.Dir, config.ClientTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATTN_WS_URL", "ws://"+s.WSAddr+"/ws")
	t.Setenv("ATTN_CLIENT_TOKEN", strings.TrimSpace(string(token)))

	if err := input([]string{"--id", shell, "--text", "echo by-session-$((2+3))", "--enter"}); err != nil {
		t.Fatal(err)
	}
	app.AwaitScreen(shell, "by-session-5")
	screen, err := screenSnapshot(shell)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(screen), "by-session-5") {
		t.Fatalf("screen of session %s lacks the echoed line:\n%s", shell, screen)
	}

	terminal := app.Terminal(shell)
	if err := input([]string{"--id", terminal, "--text", "echo by-terminal-$((3+4))", "--enter"}); err != nil {
		t.Fatal(err)
	}
	app.AwaitScreen(shell, "by-terminal-7")

	if _, err := screenSnapshot("no-such-session"); err == nil || !strings.Contains(err.Error(), "neither a session nor a terminal") {
		t.Fatalf("screen of an unknown id: got %v, want it named as neither a session nor a terminal", err)
	}
}
