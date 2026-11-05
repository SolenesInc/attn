package daemon_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/testworld"
)

func inBubbleWithAgentsAndVerdicts(t *testing.T, script func(t *testing.T, w *world)) {
	t.Helper()
	prepared := prepareWorld(t, fakeagent.Claude)
	t.Setenv("ATTN_PTY_BACKEND", "embedded")
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	synctest.Test(t, func(t *testing.T) {
		bubbled := *prepared
		bubbled.T = t
		w := &world{World: &bubbled, bubbled: true, terms: testworld.NewTerminals(t)}
		w.start()
		script(t, w)
	})
}

func (w *world) labeledBubbleClaude(t *testing.T, app *testworld.Peer) *bubbleClaude {
	t.Helper()
	id := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("deploy work")
	})
	return w.bootBubbleClaude(t, id)
}

func (a *bubbleClaude) stopWithBackgroundWork(text string) {
	a.t.Helper()
	synctest.Wait()
	a.transcript.Answer(text)
	if err := a.cli.SendStop(protocol.TerminalID(a.self), a.transcript.Path, client.StopFacts{BackgroundTasks: []protocol.StopBackgroundTask{
		{Type: "background_session", Status: "running", Name: protocol.Ptr("./deploy.sh staging")},
	}}); err != nil {
		a.t.Fatalf("%s stops with background work: %v", a.id, err)
	}
}

func (a *bubbleClaude) compact(w *world, trigger string, statusFirst bool) {
	a.t.Helper()
	terminal := protocol.TerminalID(a.self)
	if statusFirst {
		a.term.ProgramStatus(pty.ProgramWorking)
	}
	if err := a.cli.RecordCompaction(terminal, true, trigger); err != nil {
		a.t.Fatalf("%s starts compacting: %v", a.id, err)
	}
	if !statusFirst {
		a.term.ProgramStatus(pty.ProgramWorking)
	}
	w.advance(40 * time.Second)
	if got := queriedSession(a.t, a.cli, a.id); got.State != protocol.SessionStateWorking {
		a.t.Fatalf("%s is %s while compacting, want working", a.id, got.State)
	}
	if err := a.cli.RecordCompaction(terminal, false, trigger); err != nil {
		a.t.Fatalf("%s finishes compacting: %v", a.id, err)
	}
	a.term.ProgramStatus(pty.ProgramDone)
	w.advance(0)
}

func settleAgentTurn(t *testing.T, w *world, app *testworld.Peer, id string) {
	t.Helper()
	app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: protocol.SessionID(id)})
	w.advance(0)
	if got := queriedSession(t, w.Client(), id); protocol.Deref(got.TurnOwed) {
		t.Fatalf("%s still owes a turn after the user settled it", id)
	}
}

func TestAnIdleCompactionPutsTheAgentBackAsItWasAndOpensNoTurn(t *testing.T) {
	for _, tc := range []struct {
		name        string
		reply       string
		want        protocol.SessionState
		statusFirst bool
	}{
		{"idle, hook first", "Done. <!-- attn:state=idle -->", protocol.SessionStateIdle, false},
		{"idle, status first", "Done. <!-- attn:state=idle -->", protocol.SessionStateIdle, true},
		{"waiting, hook first", "Which one? <!-- attn:state=waiting_input -->", protocol.SessionStateWaitingInput, false},
		{"waiting, status first", "Which one? <!-- attn:state=waiting_input -->", protocol.SessionStateWaitingInput, true},
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

				agent.compact(w, "auto", tc.statusFirst)
				w.advance(5 * time.Minute)

				got := queriedSession(t, cli, agent.id)
				if got.State != tc.want {
					t.Errorf("after the compaction the agent is %s (%s), want %s", got.State, protocol.Deref(got.StateReason), tc.want)
				}
				if protocol.Deref(got.TurnOwed) {
					t.Error("the compaction said nothing new, but it opened a turn")
				}
			})
		})
	}
}

func TestAnIdleCompactionLeavesAnOwedTurnOwed(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "do the thing")
		w.advance(time.Second)
		agent.reply("Done. <!-- attn:state=idle -->")
		w.advance(time.Minute)
		owedSince := queriedSession(t, cli, agent.id).TurnOwed
		if !protocol.Deref(owedSince) {
			t.Fatal("the stop opened no turn")
		}

		agent.compact(w, "auto", false)

		if got := queriedSession(t, cli, agent.id); !protocol.Deref(got.TurnOwed) {
			t.Fatalf("the compaction closed the turn the user still owes: %s (%s)", got.State, protocol.Deref(got.StateReason))
		}
	})
}

func TestAnAgentWithBackgroundWorkDoesNotGoStuckAfterAnIdleCompaction(t *testing.T) {
	for _, tc := range []struct {
		name       string
		verdict    string
		wait       time.Duration
		wantState  protocol.SessionState
		wantReason string
	}{
		{"waiting verdict", "WAITING", time.Minute, protocol.SessionStateWaitingInput, "classifier_verdict"},
		{"parked verdict, still inside its window", "PARKED", time.Minute, protocol.SessionStateWorking, "background_parked"},
		{"parked verdict that expired", "PARKED", 31 * time.Minute, protocol.SessionStateIdle, "parked_expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				agent := w.labeledBubbleClaude(t, app)
				app.TypeLine(agent.id, "deploy to staging")
				agent.term.ProgramStatus(pty.ProgramWorking)
				w.advance(time.Second)
				agent.stopWithBackgroundWork("The deploy is running.")
				agent.term.ProgramStatus(pty.ProgramDone)
				answerTurnVerdict(t, w, tc.verdict)
				w.advance(time.Minute)
				if err := cli.RecordNotification(protocol.TerminalID(agent.self), "idle_prompt", "Claude is waiting for your input"); err != nil {
					t.Fatalf("notify idle_prompt: %v", err)
				}
				w.advance(tc.wait)

				agent.compact(w, "auto", false)
				w.advance(2 * time.Minute)

				if got := queriedSession(t, cli, agent.id); got.State != tc.wantState || protocol.Deref(got.StateReason) != tc.wantReason {
					t.Fatalf("after the compaction the agent is %s (%s), want %s (%s)", got.State, protocol.Deref(got.StateReason), tc.wantState, tc.wantReason)
				}
			})
		})
	}
}

func TestACompactionInsideTheParkedWindowLeavesTheExpiryToOpenTheTurn(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		agent := w.labeledBubbleClaude(t, app)
		app.TypeLine(agent.id, "deploy to staging")
		agent.term.ProgramStatus(pty.ProgramWorking)
		w.advance(time.Second)
		agent.stopWithBackgroundWork("The deploy is running.")
		agent.term.ProgramStatus(pty.ProgramDone)
		answerTurnVerdict(t, w, "PARKED")
		w.advance(time.Minute)
		if err := cli.RecordNotification(protocol.TerminalID(agent.self), "idle_prompt", "Claude is waiting for your input"); err != nil {
			t.Fatalf("notify idle_prompt: %v", err)
		}
		w.advance(time.Minute)

		agent.compact(w, "auto", false)
		w.advance(30 * time.Minute)

		got := queriedSession(t, cli, agent.id)
		if got.State != protocol.SessionStateIdle || protocol.Deref(got.StateReason) != "parked_expired" {
			t.Fatalf("past the parked window the agent is %s (%s), want idle (parked_expired)", got.State, protocol.Deref(got.StateReason))
		}
		if !protocol.Deref(got.TurnOwed) {
			t.Fatal("the stop never reached the user: the expiry after the compaction opened no turn")
		}
	})
}

func TestACompactionThatIsNotIdleStillEndsInATurn(t *testing.T) {
	t.Run("a compaction in the middle of a run", func(t *testing.T) {
		inBubbleWithAgents(t, func(t *testing.T, w *world) {
			app, cli := w.App(), w.Client()
			agent := w.bubbleClaude(t, app, "shop")
			app.TypeLine(agent.id, "do the thing")
			w.advance(time.Second)

			agent.compact(w, "auto", false)
			agent.reply("Done. <!-- attn:state=idle -->")
			w.advance(time.Minute)

			if got := queriedSession(t, cli, agent.id); !protocol.Deref(got.TurnOwed) {
				t.Fatalf("the run's stop opened no turn: %s (%s)", got.State, protocol.Deref(got.StateReason))
			}
		})
	})
	t.Run("a compaction the user asked for", func(t *testing.T) {
		inBubbleWithAgents(t, func(t *testing.T, w *world) {
			app, cli := w.App(), w.Client()
			agent := w.bubbleClaude(t, app, "shop")
			app.TypeLine(agent.id, "do the thing")
			w.advance(time.Second)
			agent.reply("Done. <!-- attn:state=idle -->")
			agent.term.ProgramStatus(pty.ProgramDone)
			w.advance(time.Minute)
			settleAgentTurn(t, w, app, agent.id)

			agent.compact(w, "manual", false)
			w.advance(time.Minute)

			if got := queriedSession(t, cli, agent.id); !protocol.Deref(got.TurnOwed) {
				t.Fatalf("the /compact the user asked for opened no turn: %s (%s)", got.State, protocol.Deref(got.StateReason))
			}
		})
	})
	t.Run("a turn the agent starts after an idle compaction", func(t *testing.T) {
		inBubbleWithAgents(t, func(t *testing.T, w *world) {
			app, cli := w.App(), w.Client()
			agent := w.bubbleClaude(t, app, "shop")
			app.TypeLine(agent.id, "do the thing")
			w.advance(time.Second)
			agent.reply("Done. <!-- attn:state=idle -->")
			agent.term.ProgramStatus(pty.ProgramDone)
			w.advance(time.Minute)
			settleAgentTurn(t, w, app, agent.id)
			agent.compact(w, "auto", false)
			w.advance(time.Minute)

			app.TypeLine(agent.id, "one more thing")
			w.advance(time.Second)
			agent.reply("Done again. <!-- attn:state=idle -->")
			w.advance(time.Minute)

			if got := queriedSession(t, cli, agent.id); !protocol.Deref(got.TurnOwed) {
				t.Fatalf("the next run's stop opened no turn: %s (%s)", got.State, protocol.Deref(got.StateReason))
			}
		})
	})
}

func TestAParkedAgentGetsRingsWithoutWaitingForItsWindowToExpire(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		agent := w.labeledBubbleClaude(t, app)
		registerSessions(t, w, cli, "sender")
		app.TypeLine(agent.id, "deploy to staging")
		agent.term.ProgramStatus(pty.ProgramWorking)
		w.advance(time.Minute)
		sendAgentMessage(t, cli, "sender", agent.id, "sent while it works")
		w.advance(time.Minute)
		if got := agent.promptsContaining(inboxDoorbell); got != 0 {
			t.Fatalf("attn rang an agent that is working: %q", agent.prompts())
		}

		agent.stopWithBackgroundWork("The deploy is running.")
		agent.term.ProgramStatus(pty.ProgramDone)
		answerTurnVerdict(t, w, "PARKED")
		w.advance(0)

		if got := agent.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("mail sent before the agent parked rang %d times once it parked, want 1: %q", got, agent.prompts())
		}
		readInbox(t, cli, agent.id, 0)

		agent.stopWithBackgroundWork("Read it. The deploy is still running.")
		agent.term.ProgramStatus(pty.ProgramDone)
		answerTurnVerdict(t, w, "PARKED")
		w.advance(0)
		parked := queriedSession(t, cli, agent.id)
		if parked.State != protocol.SessionStateWorking || protocol.Deref(parked.StateReason) != "background_parked" {
			t.Fatalf("the agent is %s (%s), want it parked", parked.State, protocol.Deref(parked.StateReason))
		}
		sendAgentMessage(t, cli, "sender", agent.id, "sent while it is parked")
		w.advance(0)
		if got := agent.promptsContaining(inboxDoorbell); got != 2 {
			t.Fatalf("mail sent to a parked agent rang %d times in all, want 2: %q", got, agent.prompts())
		}
	})
}

func TestAnAgentAttnCannotReadGetsRings(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		agent := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")
		app.TypeLine(agent.id, "do the thing")
		w.advance(time.Minute)
		sendAgentMessage(t, cli, "sender", agent.id, "sent while it works")
		if got := agent.promptsContaining(inboxDoorbell); got != 0 {
			t.Fatalf("attn rang an agent that is working: %q", agent.prompts())
		}

		stuck := testworld.AwaitSession(app, agent.id, func(s protocol.Session) bool { return s.State == protocol.SessionStateUnknown })
		if reason := protocol.Deref(stuck.StateReason); reason != "stuck" {
			t.Fatalf("the silent agent is unknown (%s), want stuck", reason)
		}
		w.advance(0)
		if got := agent.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("an agent in unknown got %d rings, want 1: %q", got, agent.prompts())
		}
	})
}
