package daemon_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func codexAutomationWorld(t *testing.T, shared bool) (*world, *testworld.Peer) {
	t.Helper()
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}
	if shared {
		setSetting(t, app, "codex_shared_enabled", "true")
		w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("shop")))
	}
	applyAutomation(t, w.Client(), manualAutomation(w, "Check the code."))
	awaitAutomationChanged(app, 1)
	return w, app
}

func runAutomationFromApp(app *testworld.Peer, requestID string) protocol.AutomationRunResultMessage {
	app.T.Helper()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: requestID})
	return awaitAutomationRunResult(app, requestID)
}

func awaitAutomationRunResult(app *testworld.Peer, requestID string) protocol.AutomationRunResultMessage {
	app.T.Helper()
	return testworld.Await(app, protocol.EventAutomationRunResult, automationAnswer[protocol.AutomationRunResultMessage](requestID))
}

func describeRunAnswer(m protocol.AutomationRunResultMessage) string {
	if m.Run == nil {
		return fmt.Sprintf("success=%t error=%q and no run", m.Success, protocol.Deref(m.Error))
	}
	return fmt.Sprintf("success=%t error=%q run state=%s last_error=%q", m.Success, protocol.Deref(m.Error), m.Run.State, protocol.Deref(m.Run.LastError))
}

func awaitRunState(t *testing.T, w *world, app *testworld.Peer, runID string, done func(protocol.AutomationRunSummary) bool) protocol.AutomationRunSummary {
	t.Helper()
	for {
		for _, run := range automationRuns(t, w.Client(), 1) {
			if run.ID == runID && done(run) {
				return run
			}
		}
		awaitAutomationChanged(app, 1)
	}
}

func TestACodexAutomationWhoseConversationIsStillBeingWrittenIsNeverDeliveredAndFailsWithItsStartupScreen(t *testing.T) {
	w, app := codexAutomationWorld(t, true)
	arrived, release := w.CodexServer().HoldNextInjectReply()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "held"})
	arrived()
	answered := awaitAutomationRunResult(app, "held")
	runs := automationRuns(t, w.Client(), 1)
	if len(runs) != 1 || runs[0].State == "delivered" {
		t.Fatalf("while its conversation is still being written the run answered %s with runs %+v, want it not delivered", describeRunAnswer(answered), runs)
	}

	release("disk I/O error")
	failed := awaitRunState(t, w, app, runs[0].ID, func(r protocol.AutomationRunSummary) bool { return r.State != "pending" })
	failure := protocol.Deref(failed.LastError)
	if failed.State != "failed" || !strings.Contains(failure, "exited with code 1 before its first turn") || !strings.Contains(failure, "thread/start failed during TUI bootstrap") {
		t.Fatalf("after Codex exited at bootstrap the run is %s with %q, want failed with the startup screen", failed.State, failure)
	}
	seed, err := w.Client().SeedShow("", protocol.Deref(failed.SeedID))
	if err != nil || seed.Seed.Status != "withered" {
		t.Fatalf("the failed run's seed = %+v (%v), want it withered", seed, err)
	}
}

func TestACodexAutomationRefusedAtBootstrapFailsWithoutEverBeingDelivered(t *testing.T) {
	w, app := codexAutomationWorld(t, true)
	arrived, release := w.CodexServer().HoldNextInjectReply()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "refused"})
	arrived()
	release("disk I/O error")
	answered := awaitAutomationRunResult(app, "refused")
	if answered.Success || !strings.Contains(protocol.Deref(answered.Error), "thread/start failed during TUI bootstrap") {
		t.Fatalf("a run whose conversation was refused answered %s, want a failure with the startup screen", describeRunAnswer(answered))
	}
	runs := automationRuns(t, w.Client(), 1)
	if len(runs) != 1 || runs[0].State != "failed" || !strings.Contains(protocol.Deref(runs[0].LastError), "disk I/O") {
		t.Fatalf("runs = %+v, want the one run failed naming the refusal and never delivered", runs)
	}
}

func TestAPlainCodexAutomationThatExitsBeforeItsFirstTurnFailsWithItsScreen(t *testing.T) {
	w, app := codexAutomationWorld(t, false)
	w.ExitAtNextBoot(1, "Error: the model provider refused the login")
	answered := runAutomationFromApp(app, "exits")
	if answered.Success || !strings.Contains(protocol.Deref(answered.Error), "exited with code 1 before its first turn") {
		t.Fatalf("a run whose Codex exited at boot answered %s, want a failure naming the exit", describeRunAnswer(answered))
	}
	runs := automationRuns(t, w.Client(), 1)
	if len(runs) != 1 || runs[0].State != "failed" || !strings.Contains(protocol.Deref(runs[0].LastError), "the model provider refused the login") {
		t.Fatalf("runs = %+v, want the one run failed with the screen Codex left", runs)
	}
}

func TestACodexAutomationIsDeliveredOnceItsOpeningPromptStartsATurn(t *testing.T) {
	for _, shared := range []bool{true, false} {
		name := map[bool]string{true: "shared", false: "plain"}[shared]
		t.Run(name, func(t *testing.T) {
			w, app := codexAutomationWorld(t, shared)
			boot := w.HoldNextBoot()
			app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "ok"})
			runs := automationRuns(t, w.Client(), 1)
			for len(runs) == 0 {
				awaitAutomationChanged(app, 1)
				runs = automationRuns(t, w.Client(), 1)
			}
			if runs[0].State != "pending" {
				t.Fatalf("before Codex boots the run is %+v, want pending", runs[0])
			}
			boot()
			answered := awaitAutomationRunResult(app, "ok")
			if !answered.Success {
				t.Fatalf("run answered %s, want success", describeRunAnswer(answered))
			}
			session := string(protocol.Deref(answered.Run.SessionID))
			agent := w.Launched(session)
			if prompt := agent.Prompted(); !strings.Contains(prompt, "Check the code.") {
				t.Fatalf("the agent was prompted with %q, want the configured task", prompt)
			}
			delivered := awaitRunState(t, w, app, answered.Run.ID, func(r protocol.AutomationRunSummary) bool { return r.State != "pending" })
			if delivered.State != "delivered" {
				t.Fatalf("after its first turn started the run is %+v, want delivered", delivered)
			}
		})
	}
}

func TestASharedCodexSingletonContinuesItsLiveSessionAndResumesItsConversationAfterStopping(t *testing.T) {
	t.Setenv("ATTN_AUTOMATION_SCHEDULE_INTERVAL", "100ms")
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	if err := os.MkdirAll(w.Path(""), 0o755); err != nil {
		t.Fatal(err)
	}
	setSetting(t, app, "codex_shared_enabled", "true")
	spec := strings.NewReplacer("*/2 * * * *", "@every 1s", "launch: {driver: claude}", "launch: {driver: codex}").Replace(automationSingletonSpec(w, "Sweep", "latest", ""))
	id := applyAutomation(t, cli, spec).ID
	awaitDelivered := func(earlier ...string) protocol.AutomationRunSummary {
		t.Helper()
		for {
			if runs := automationRuns(t, cli, id); len(runs) > 0 && runs[0].State == "delivered" && !slices.Contains(earlier, runs[0].ID) {
				setAutomationEnabled(t, cli, id, false)
				return runs[0]
			}
			awaitAutomationChanged(app, id)
		}
	}

	first := awaitDelivered()
	session := protocol.Deref(first.SessionID)
	agent := w.Launched(string(session))
	if opening := agent.Prompted(); !strings.Contains(opening, "Sweep.") {
		t.Fatalf("the singleton's first prompt = %q, want its opening prompt", opening)
	}
	conversation := agent.ConversationID
	agent.Reply("Swept. <!-- attn:state=idle -->")
	server := w.CodexServer()

	setAutomationEnabled(t, cli, id, true)
	live := awaitDelivered(first.ID)
	if protocol.Deref(live.SessionID) != session || protocol.Deref(live.SeedID) != protocol.Deref(first.SeedID) {
		t.Fatalf("the live continuation ran on %s/%s, want %s/%s", protocol.Deref(live.SessionID), protocol.Deref(live.SeedID), session, protocol.Deref(first.SeedID))
	}
	if notice := server.Prompted(conversation); strings.Contains(notice, "Sweep.") {
		t.Fatalf("the live continuation prompted %q, want its occurrence notice and not the opening prompt again", notice)
	}
	server.Reply(conversation, "Swept again. <!-- attn:state=idle -->")

	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: session})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.SessionID) == session })
	setAutomationEnabled(t, cli, id, true)
	resumedRun := awaitDelivered(first.ID, live.ID)
	resumed := w.Launched(string(session))
	if protocol.Deref(resumedRun.SessionID) != session || protocol.Deref(resumedRun.SeedID) != protocol.Deref(first.SeedID) || !resumed.Resumed || resumed.ConversationID != conversation {
		t.Fatalf("after stopping, the singleton ran on %s/%s as %+v, want session %s resumed on conversation %s", protocol.Deref(resumedRun.SessionID), protocol.Deref(resumedRun.SeedID), resumed, session, conversation)
	}
	if strings.Contains(strings.Join(resumed.Argv, " "), "Sweep.") {
		t.Fatalf("the resumed singleton launched with %q, want no opening prompt again", resumed.Argv)
	}
	t.Logf("singleton %s: opening prompt once on conversation %s, live continuation on the same session, stopped and resumed on the same conversation and seed %s", session, conversation, protocol.Deref(first.SeedID))
}

func TestACodexAutomationWhoseAgentExitsRightAfterItsFirstTurnStartsIsDelivered(t *testing.T) {
	w, app := codexAutomationWorld(t, false)
	boot := w.HoldNextBoot()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "brief"})
	w.AwaitHeldBoot()
	runs := automationRuns(t, w.Client(), 1)
	boot()
	agent := w.Launched(string(protocol.Deref(runs[0].SessionID)))
	agent.Prompted()
	agent.Exit(1)
	settled := awaitRunState(t, w, app, runs[0].ID, func(r protocol.AutomationRunSummary) bool { return r.State != "pending" })
	if settled.State != "delivered" {
		t.Fatalf("a run whose agent started its turn and then exited is %+v, want delivered once", settled)
	}
	if answered := awaitAutomationRunResult(app, "brief"); !answered.Success {
		t.Fatalf("run answered %s, want success", describeRunAnswer(answered))
	}
}

func TestACodexSingletonWhoseFirstLaunchExitsBeforeItsFirstTurnStartsFreshAtTheNextOccurrence(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		cli := w.Client()
		if err := os.MkdirAll(w.Path(""), 0o755); err != nil {
			t.Fatal(err)
		}
		w.terms.OnNextSpawn(func(term *testworld.Terminal) {
			term.PaintScreen("Error: the model provider refused the login")
			term.Exit(1)
		})
		id := applyAutomation(t, cli, strings.Replace(automationSingletonSpec(w, "Sweep", "latest", ""), "launch: {driver: claude}", "launch: {driver: codex}", 1)).ID
		w.advance(2*time.Minute + 30*time.Second)
		runs := automationRuns(t, cli, id)
		if len(runs) != 1 || runs[0].State != "failed" {
			t.Fatalf("after Codex exited before its first turn the runs are %+v, want one failed run", runs)
		}
		failed := runs[0]

		launched := make(chan *testworld.Terminal, 1)
		w.terms.OnNextSpawn(func(term *testworld.Terminal) { launched <- term })
		w.advance(2 * time.Minute)
		var replacement *testworld.Terminal
		select {
		case replacement = <-launched:
		default:
			latest := automationRuns(t, cli, id)[0]
			t.Fatalf("after the first launch exited, the next occurrence launched nothing and is %s with %q", latest.State, protocol.Deref(latest.LastError))
		}
		if err := cli.UpdateStateFromHook(replacement.Options.ID, "working", ""); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()

		runs = automationRuns(t, cli, id)
		fresh := runs[0]
		if len(runs) != 2 || fresh.State != "delivered" || protocol.Deref(fresh.SessionID) == protocol.Deref(failed.SessionID) || protocol.Deref(fresh.SeedID) == protocol.Deref(failed.SeedID) {
			t.Fatalf("after the first launch exited, the next occurrence is %s on %s/%s with %q, want delivered on a session and seed of its own, not %s/%s", fresh.State, protocol.Deref(fresh.SessionID), protocol.Deref(fresh.SeedID), protocol.Deref(fresh.LastError), protocol.Deref(failed.SessionID), protocol.Deref(failed.SeedID))
		}
		if argv := strings.Join(replacement.Options.AgentArgv, " "); !strings.Contains(argv, "Sweep.") {
			t.Fatalf("the replacement launched with %q, want its opening prompt", argv)
		}
		if first := runs[1]; first.ID != failed.ID || first.State != "failed" || !strings.Contains(protocol.Deref(first.LastError), "the model provider refused the login") {
			t.Fatalf("the first run now reads %+v, want it still failed with the screen Codex left", first)
		}
	})
}
