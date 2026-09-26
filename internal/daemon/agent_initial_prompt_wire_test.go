package daemon_test

import (
	"testing"

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
