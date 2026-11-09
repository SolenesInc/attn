package daemon_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type agentRestart struct {
	name      string
	stop      func(t *testing.T, w *world, app *testworld.Peer, agent *bubbleClaude)
	bringBack func(t *testing.T, app *testworld.Peer, session, seed string)
}

var agentRestarts = []agentRestart{
	{
		name: "reopening it from the ledger",
		stop: func(t *testing.T, w *world, app *testworld.Peer, agent *bubbleClaude) {
			closePane(app, sessionPane{session: agent.id})
		},
		bringBack: func(t *testing.T, app *testworld.Peer, session, _ string) {
			if reopened := reopenOverTheWebSocket(app, session); !reopened.Success {
				t.Fatalf("reopen %s: %s", session, protocol.Deref(reopened.Error))
			}
		},
	},
	{
		name: "resuming its seed",
		stop: func(t *testing.T, w *world, app *testworld.Peer, agent *bubbleClaude) {
			closePane(app, sessionPane{session: agent.id})
		},
		bringBack: func(t *testing.T, app *testworld.Peer, _, seed string) {
			if resumed := seedResumeRequest(app, seed); !resumed.Success {
				t.Fatalf("resume %s: %s", seed, protocol.Deref(resumed.Error))
			}
		},
	},
	{
		name: "reloading it after its process exited",
		stop: func(t *testing.T, w *world, app *testworld.Peer, agent *bubbleClaude) {
			agent.term.Exit(143)
			testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == agent.id })
		},
		bringBack: func(t *testing.T, app *testworld.Peer, session, _ string) { reloadRespawned(t, app, session) },
	},
	{
		name:      "reloading it while it runs",
		bringBack: func(t *testing.T, app *testworld.Peer, session, _ string) { reloadRespawned(t, app, session) },
	},
}

func agentTendingAnAsk(t *testing.T, w *world, app *testworld.Peer) (agent *bubbleClaude, seed string, answer func()) {
	t.Helper()
	cli := w.Client()
	setSetting(t, app, "asks_enabled", "true")
	agent = w.bubbleClaude(t, app, "shop")
	agent.reply("Ready. <!-- attn:state=idle -->")
	seed = plantSeedAs(t, cli, agent.id, "pick a store")
	gardenNudgeMove(t, cli, agent.id, seed, "tend")
	asked, err := cli.AskOpen(protocol.SessionID(agent.id), seed, cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatalf("the agent asks: %v", err)
	}
	return agent, seed, func() { answerAsk(t, app, asked.ID) }
}

func wantTypedOnlyOnceTheHarnessStarted(t *testing.T, w *world, session string) {
	t.Helper()
	term := w.terminal(session)
	term.WorkerReportsItsProcessRunning()
	synctest.Wait()
	if inputs := term.Inputs(); len(inputs) != 0 {
		t.Fatalf("attn typed into a harness that had not started: %+v", inputs)
	}
	started := w.bootBubbleClaude(t, session)
	if got := started.promptsContaining(startedAnswer); got != 1 {
		t.Fatalf("the started agent took %q, want %q once", started.prompts(), startedAnswer)
	}
}

func TestAnAnswerDuringTheBootOfARestartedAgentIsTypedOnlyOnceItsHarnessHasStarted(t *testing.T) {
	for _, restart := range agentRestarts {
		t.Run(restart.name, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app := w.App()
				agent, seed, answer := agentTendingAnAsk(t, w, app)
				if restart.stop != nil {
					restart.stop(t, w, app, agent)
					synctest.Wait()
				}
				restart.bringBack(t, app, agent.id, seed)
				synctest.Wait()
				answer()
				wantTypedOnlyOnceTheHarnessStarted(t, w, agent.id)
			})
		})
	}
}

func TestAnAnswerWaitingForAStoppedAgentIsTypedAsSoonAsItsHarnessHasStarted(t *testing.T) {
	for _, restart := range agentRestarts {
		if restart.stop == nil {
			continue
		}
		t.Run(restart.name, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app := w.App()
				agent, seed, answer := agentTendingAnAsk(t, w, app)
				restart.stop(t, w, app, agent)
				synctest.Wait()
				answer()
				synctest.Wait()
				restart.bringBack(t, app, agent.id, seed)
				synctest.Wait()
				wantTypedOnlyOnceTheHarnessStarted(t, w, agent.id)
			})
		})
	}
}

func TestRestartingASettledAgentOpensNoTurnAndKeepsItsState(t *testing.T) {
	for _, restart := range agentRestarts {
		if restart.name == "reloading it after its process exited" {
			continue
		}
		t.Run(restart.name, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				agent := w.bubbleClaude(t, app, "shop")
				agent.reply("Ready. <!-- attn:state=idle -->")
				seed := plantSeedAs(t, cli, agent.id, "keep working")
				gardenNudgeMove(t, cli, agent.id, seed, "tend")
				app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: protocol.SessionID(agent.id)})
				synctest.Wait()
				w.advance(time.Second)
				if restart.stop != nil {
					restart.stop(t, w, app, agent)
					synctest.Wait()
				}
				restart.bringBack(t, app, agent.id, seed)
				synctest.Wait()
				if booting := queriedSession(t, cli, agent.id); booting.State != protocol.SessionStateIdle {
					t.Fatalf("a settled agent shows %s while its harness boots, want idle", booting.State)
				}
				w.advance(time.Second)
				w.bootBubbleClaude(t, agent.id)
				w.advance(time.Second)
				if started := queriedSession(t, cli, agent.id); started.State != protocol.SessionStateIdle || protocol.Deref(started.TurnOwed) {
					t.Fatalf("a settled agent came back %s with turn owed %v, want idle with none", started.State, protocol.Deref(started.TurnOwed))
				}
			})
		})
	}
}

func TestAnAnnotationDuringTheBootOfARestartedAgentIsSkippedInsteadOfTyped(t *testing.T) {
	for _, restart := range agentRestarts {
		t.Run(restart.name, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app := w.App()
				agent, seed, _ := agentTendingAnAsk(t, w, app)
				if restart.stop != nil {
					restart.stop(t, w, app, agent)
					synctest.Wait()
				}
				restart.bringBack(t, app, agent.id, seed)
				synctest.Wait()
				if sent := submitSessionAnnotationFeedback(app, agent.id, "the tax is wrong"); sent.success || sent.status != "skipped_pending_approval" {
					t.Fatalf("an annotation during the boot got %+v, want it skipped", sent)
				}
				if inputs := w.terminal(agent.id).Inputs(); len(inputs) != 0 {
					t.Fatalf("attn typed into a harness that had not started: %+v", inputs)
				}
			})
		})
	}
}
