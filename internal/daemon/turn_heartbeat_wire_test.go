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

func TestABusyFrameReadBeforeTheVerdictLeavesTheSettledTurnAlone(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "plan the release")
		w.advance(0)

		lastFrameRead := time.Now()
		w.advance(10 * time.Millisecond)
		agent.reply("Should I ship version one? <!-- attn:state=waiting_input -->")
		waiting := queriedSession(t, agent.cli, agent.id)
		if waiting.State != protocol.SessionStateWaitingInput {
			t.Fatalf("after the question the session is %s, want waiting_input", waiting.State)
		}

		agent.term.HeartbeatReadAt("busy", "Claude Code", lastFrameRead)
		w.advance(0)
		if s := queriedSession(t, agent.cli, agent.id); s.State != protocol.SessionStateWaitingInput || s.StateSince != waiting.StateSince {
			t.Fatalf("a frame read before the verdict moved the session to %s since %s, want waiting_input since %s, or the phone hears the question twice", s.State, s.StateSince, waiting.StateSince)
		}

		w.advance(10 * time.Millisecond)
		agent.term.Heartbeat("busy", "Claude Code")
		w.advance(0)
		if s := queriedSession(t, agent.cli, agent.id); s.State != protocol.SessionStateWorking {
			t.Fatalf("a frame read after the verdict left the session %s, want working", s.State)
		}
	})
}

func TestABusyFrameReadAfterAnApprovalAndBeforeTheVerdictStillAnswersTheApproval(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "run the migration")
		w.advance(0)

		w.advance(10 * time.Millisecond)
		agent.term.Heartbeat("approval", "Allow the command to run?")
		w.advance(10 * time.Millisecond)
		answeredFrameRead := time.Now()
		w.advance(10 * time.Millisecond)
		agent.reply("Ran it. <!-- attn:state=idle -->")

		agent.term.HeartbeatReadAt("busy", "Claude Code", answeredFrameRead)
		w.advance(2 * time.Minute)
		if s := queriedSession(t, agent.cli, agent.id); s.State != protocol.SessionStateIdle {
			t.Fatalf("the approval answered before the verdict left the session %s (%s), want idle", s.State, protocol.Deref(s.StateReason))
		}
	})
}

func TestAWorkingStatusReadBeforeTheVerdictLeavesTheSettledTurnAlone(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "plan the release")
		w.advance(0)

		statusRead := time.Now()
		w.advance(10 * time.Millisecond)
		agent.reply("Should I ship version one? <!-- attn:state=waiting_input -->")
		waiting := queriedSession(t, agent.cli, agent.id)

		before := len(sessionUpdatesOf(app, agent.id))
		agent.term.ProgramStatusReadAt("working", statusRead)
		w.advance(0)
		if updates := sessionUpdatesOf(app, agent.id)[before:]; len(updates) != 0 {
			t.Fatalf("a status read before the verdict moved the session: %s", describeUpdates(updates))
		}
		if s := queriedSession(t, agent.cli, agent.id); s.State != protocol.SessionStateWaitingInput || s.StateSince != waiting.StateSince {
			t.Fatalf("a status read before the verdict left the session %s since %s, want waiting_input since %s", s.State, s.StateSince, waiting.StateSince)
		}
	})
}
