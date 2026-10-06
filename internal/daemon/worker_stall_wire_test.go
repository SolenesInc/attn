package daemon_test

import (
	"errors"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestARestartKeepsAWorkingAgentWhoseTerminalDoesNotAnswer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		alive    bool
		probeErr error
		answerAt time.Duration
	}{
		{name: "its worker is alive", alive: true},
		{name: "its liveness probe fails", probeErr: errors.New("worker health timed out")},
		{name: "it answers before recovery gives up", answerAt: 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app := w.App()
				agent := w.bubbleClaude(t, app, "shop")
				app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(agent.self), Data: "migrate the schema\r"})
				testworld.AwaitSession(app, agent.id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

				answer := agent.term.Stall(tc.alive, tc.probeErr)
				w.restart()
				if tc.answerAt > 0 {
					w.advance(tc.answerAt)
					assertStillWorking(t, w, agent.id)
					answer()
				}
				w.advance(2 * time.Minute)
				assertStillWorking(t, w, agent.id)
			})
		})
	}
}

func assertStillWorking(t *testing.T, w *world, id string) {
	t.Helper()
	if got := queriedSession(t, w.Client(), id).State; got != protocol.SessionStateWorking {
		t.Fatalf("after the restart the agent is %s, want it still working", got)
	}
}
