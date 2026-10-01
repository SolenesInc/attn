package daemon_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEachAgentStartsOnItsInitialPromptAndShowsItsReply(t *testing.T) {
	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex, fakeagent.Copilot, fakeagent.Pi} {
		t.Run(string(h), func(t *testing.T) {
			w := newWorld(t, h)
			app := w.App()
			if h == fakeagent.Pi {
				awaitAgentAvailable(app, string(h))
			}

			session := w.Spawn(app, h, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
				m.InitialPrompt = protocol.Ptr("list the checkout tests")
			})
			run := w.Launched(session)
			if got := run.Prompted(); got != "list the checkout tests" {
				t.Fatalf("%s received %q", h, got)
			}
			run.Reply("Found 14 checkout tests. <!-- attn:state=idle -->")
			app.AwaitScreen(session, "Found 14 checkout tests.")

			if h == fakeagent.Copilot {
				run.Exit(3)
				exited := testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
				if exited.ExitCode != 3 {
					t.Fatalf("session_exited exit_code = %d, want copilot's 3", exited.ExitCode)
				}
			}
		})
	}
}

func awaitAgentAvailable(p *testworld.Peer, agent string) {
	p.T.Helper()
	key := agent + "_available"
	if p.Initial.Settings[key] == "true" {
		return
	}
	testworld.Await(p, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings[key] == "true" })
}

func TestARestartRemovesInitialPromptFilesTheLastDaemonLeftUnread(t *testing.T) {
	w := newWorld(t)
	prompts := filepath.Join(filepath.Dir(w.Socket), "runtime", "prompts")
	if err := os.MkdirAll(prompts, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(prompts, "left-by-a-stopped-daemon.md")
	fresh := filepath.Join(prompts, "a-wrapper-is-about-to-read.md")
	for _, path := range []string{stale, fresh} {
		if err := os.WriteFile(path, []byte("a plaintext prompt"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	anHourAgo := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, anHourAgo, anHourAgo); err != nil {
		t.Fatal(err)
	}

	w.restart()

	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the prompt file from an hour ago survived the restart (stat err %v)", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("the restart removed a prompt file a launching wrapper may still read: %v", err)
	}
}
