package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestEachAgentStartsOnItsInitialPromptAndShowsItsReply(t *testing.T) {
	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex, fakeagent.Copilot, fakeagent.Pi} {
		t.Run(string(h), func(t *testing.T) {
			w := newWorld(t, withAgents(h))
			app := w.app()
			if h == fakeagent.Pi {
				awaitAgentAvailable(app, string(h))
			}

			session := w.spawn(app, h, w.path("shop"), func(m *protocol.SpawnSessionMessage) {
				m.InitialPrompt = protocol.Ptr("list the checkout tests")
			})
			run := w.launched(session)
			if got := run.Prompted(); got != "list the checkout tests" {
				t.Fatalf("%s received %q", h, got)
			}
			run.Reply("Found 14 checkout tests. <!-- attn:state=idle -->")
			app.awaitScreen(session, "Found 14 checkout tests.")

			if h == fakeagent.Copilot {
				run.Exit(3)
				exited := await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
				if exited.ExitCode != 3 {
					t.Fatalf("session_exited exit_code = %d, want copilot's 3", exited.ExitCode)
				}
			}
		})
	}
}

func awaitAgentAvailable(p *peer, agent string) {
	p.t.Helper()
	key := agent + "_available"
	if p.initial.Settings[key] == "true" {
		return
	}
	await(p, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings[key] == "true" })
}
