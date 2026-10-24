package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const literalInitialPrompt = "list \"quoted\" checkout tests, 'single', \\\\path\n$HOME $(id) `ticks` and λ"

func TestEachAgentStartsOnItsInitialPromptAndShowsItsReply(t *testing.T) {
	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex, fakeagent.Pi} {
		t.Run(string(h), func(t *testing.T) {
			w := newWorld(t, h)
			app := w.App()
			if h == fakeagent.Pi {
				awaitAgentAvailable(app, string(h))
			}

			session := w.Spawn(app, h, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
				m.InitialPrompt = protocol.Ptr(literalInitialPrompt)
			})
			run := w.Launched(session)
			if got := run.Prompted(); got != literalInitialPrompt {
				t.Fatalf("%s received %q", h, got)
			}
			run.Reply("Found 14 checkout tests. <!-- attn:state=idle -->")
			app.AwaitScreen(session, "Found 14 checkout tests.")

			if h != fakeagent.Pi {
				run.Exit(3)
				exited := testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
				if exited.ExitCode != 3 {
					t.Fatalf("session_exited exit_code = %d, want %s's 3", exited.ExitCode, h)
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

func TestARefusedLaunchPreparationReportsTheErrorAndCanBeRetried(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "direct_agent_launch_enabled", "true")
	notebook := filepath.Join(w.Dir, "notebook")
	if err := os.RemoveAll(notebook); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notebook, []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	chief := func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) }
	result, _, _ := w.RequestSpawn(app, fakeagent.Claude, w.Path("shop"), chief)
	if result.Success || !strings.Contains(protocol.Deref(result.Error), "prepare chief notebook") {
		t.Fatalf("preparation failure returned %+v", result)
	}
	if err := os.Remove(notebook); err != nil {
		t.Fatal(err)
	}
	w.Launched(w.Spawn(app, fakeagent.Claude, w.Path("shop"), chief))
}
