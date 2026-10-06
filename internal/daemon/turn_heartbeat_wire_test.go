package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestASettledTurnStaysSettledWhileTheAgentRepaintsSlowerThanItsHeartbeatLasts(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "tidy the logs")
		agent.reply("Tidied. <!-- attn:state=idle -->")
		if owed := queriedSession(t, agent.cli, agent.id); !protocol.Deref(owed.TurnOwed) {
			t.Fatalf("a finished turn is %+v, want it owed", owed)
		}
		app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: protocol.SessionID(agent.id)})
		w.advance(0)
		if settled := queriedSession(t, agent.cli, agent.id); protocol.Deref(settled.TurnOwed) {
			t.Fatal("settling did not close the turn")
		}

		const repaint = 1920 * time.Millisecond
		for frame := 1; frame <= 16; frame++ {
			agent.term.Heartbeat("busy", "⠐ compacting")
			w.advance(repaint)
			if s := queriedSession(t, agent.cli, agent.id); protocol.Deref(s.TurnOwed) {
				t.Fatalf("frame %d: the settled turn re-opened while the agent was painting busy frames (%s)", frame, s.State)
			}
		}
		if s := queriedSession(t, agent.cli, agent.id); s.State != protocol.SessionStateWorking {
			t.Fatalf("after busy frames throughout the session is %s, want working", s.State)
		}
	})
}
