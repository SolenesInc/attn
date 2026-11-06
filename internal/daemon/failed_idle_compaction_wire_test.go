package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
)

func (a *bubbleClaude) failCompact() {
	a.t.Helper()
	a.term.ProgramStatus(pty.ProgramWorking)
	if err := a.cli.RecordCompaction(protocol.TerminalID(a.self), true, "auto"); err != nil {
		a.t.Fatalf("%s starts compacting: %v", a.id, err)
	}
	a.term.ProgramStatus(pty.ProgramDone)
}

func TestAFailedIdleCompactionPutsTheAgentBackAsItWas(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply string
		want  protocol.SessionState
	}{
		{"idle", "Done. <!-- attn:state=idle -->", protocol.SessionStateIdle},
		{"waiting", "Which one? <!-- attn:state=waiting_input -->", protocol.SessionStateWaitingInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				agent := w.bubbleClaude(t, app, "shop")
				app.TypeLine(agent.id, "do the thing")
				agent.term.ProgramStatus(pty.ProgramWorking)
				w.advance(time.Second)
				agent.reply(tc.reply)
				agent.term.ProgramStatus(pty.ProgramDone)
				w.advance(time.Minute)
				settleAgentTurn(t, w, app, agent.id)

				agent.failCompact()
				w.advance(0)
				if got := queriedSession(t, cli, agent.id); got.State != tc.want {
					t.Errorf("right after the failed compaction the agent is %s (%s), want %s", got.State, protocol.Deref(got.StateReason), tc.want)
				}

				w.advance(time.Hour)
				got := queriedSession(t, cli, agent.id)
				if got.State != tc.want {
					t.Errorf("an hour after the failed compaction the agent is %s (%s), want %s", got.State, protocol.Deref(got.StateReason), tc.want)
				}
				if protocol.Deref(got.TurnOwed) {
					t.Error("the failed compaction said nothing new, but it opened a turn")
				}
			})
		})
	}
}

func TestMailSentAroundAFailedIdleCompactionRingsWithoutWaitingForStuck(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		agent := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")
		app.TypeLine(agent.id, "do the thing")
		agent.term.ProgramStatus(pty.ProgramWorking)
		w.advance(time.Second)
		agent.reply("Done. <!-- attn:state=idle -->")
		agent.term.ProgramStatus(pty.ProgramDone)
		w.advance(time.Minute)
		settleAgentTurn(t, w, app, agent.id)

		agent.term.ProgramStatus(pty.ProgramWorking)
		if err := cli.RecordCompaction(protocol.TerminalID(agent.self), true, "auto"); err != nil {
			t.Fatalf("start compacting: %v", err)
		}
		w.advance(0)
		sendAgentMessage(t, cli, "sender", agent.id, "sent during the compact")
		w.advance(0)
		if got := agent.promptsContaining(inboxDoorbell); got != 0 {
			t.Fatalf("attn rang an agent that is compacting: %q", agent.prompts())
		}

		agent.term.ProgramStatus(pty.ProgramDone)
		w.advance(0)
		if got := agent.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("mail held during the failed compaction rang %d times once it ended, want 1: %q", got, agent.prompts())
		}
	})
}
