package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAWaitingClaudeKeepsItsStateAcrossADaemonRestart(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "pick a colour")
		agent.reply("Which colour? <!-- attn:state=waiting_input -->")
		waiting := testworld.AwaitSession(app, agent.id, func(s protocol.Session) bool {
			return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.TurnOwed)
		})

		w.advance(20 * time.Second)
		agent.term.Heartbeat("not_busy", "Claude Code")
		w.advance(0)
		w.restart()
		app = w.App()
		for _, elapsed := range []time.Duration{0, time.Minute} {
			w.advance(elapsed)
			got := queriedSession(t, w.Client(), agent.id)
			if got.State != protocol.SessionStateWaitingInput || got.StateSince != waiting.StateSince ||
				!protocol.Deref(got.TurnOwed) || !openedAt(got).Equal(openedAt(waiting)) {
				t.Fatalf("%s after restart: state=%s since=%s owed=%v opened=%s; want waiting_input since=%s owed at=%s",
					elapsed, got.State, got.StateSince, protocol.Deref(got.TurnOwed), openedAt(got), waiting.StateSince, openedAt(waiting))
			}
		}
	})
}
