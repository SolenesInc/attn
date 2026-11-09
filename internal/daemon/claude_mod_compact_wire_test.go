package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const compactingClaudeReply = "Fixed and closing. <!-- attn:state=idle -->"

func moddedClaudeInATurn(t *testing.T, w *world, app *testworld.Peer) (string, *fakeagent.Run) {
	t.Helper()
	session, run := moddedClaude(t, w, app)
	workingOn(t, app, run, session, "fix the flaky checkout test")
	return session, run
}

func awaitCompactReport(t *testing.T, run *fakeagent.Run, outcome string) fakeagent.ModCompactReport {
	t.Helper()
	report := run.ModNextCompactReport()
	for report.Outcome == fakeagent.ModCompactBusy && outcome != fakeagent.ModCompactBusy {
		report = run.ModNextCompactReport()
	}
	if report.Outcome != outcome {
		t.Fatalf("the mod reported the compact as %+v, want %s", report, outcome)
	}
	return report
}

func closeCompactingDuringTheTurn(t *testing.T, cli *client.Client, run *fakeagent.Run, session string) {
	t.Helper()
	closeCompacting(t, cli, session)
	awaitCompactReport(t, run, fakeagent.ModCompactBusy)
}

func awaitNewerTurnRefusal(t *testing.T, run *fakeagent.Run) {
	t.Helper()
	for {
		report := run.ModNextCompactReport()
		if report.Outcome != fakeagent.ModCompactBusy {
			t.Fatalf("the mod reported the compact as %+v, want it refused for a newer turn", report)
		}
		if report.Reason == fakeagent.ModNewerTurnBegan {
			return
		}
	}
}

func mailBehindTheCompact(t *testing.T, cli *client.Client, session string) {
	t.Helper()
	sent := sendAgentMessage(t, cli, "sender", session, "the review is in")
	if sent.Status != protocol.AgentMsgStatusQueued || !strings.Contains(sent.Detail, "compacting") {
		t.Fatalf("mail during the compact = %+v, want it queued behind the close", sent)
	}
}

func ringOfTheMailBehindTheCompact(t *testing.T, run *fakeagent.Run) {
	t.Helper()
	if got := run.Prompted(); !strings.Contains(got, inboxDoorbell) || !run.ThroughMod {
		t.Fatalf("the agent took %q (through the mod: %t), want the ring of the mail that waited behind the compact", got, run.ThroughMod)
	}
}

func compactDidNotRun(t *testing.T, closed protocol.SessionLedgerEntry, why string) {
	t.Helper()
	reason := protocol.Deref(closed.CloseReason)
	if !strings.HasPrefix(reason, compactReason) || !strings.Contains(reason, "the compact did not run") || !strings.Contains(reason, why) {
		t.Errorf("the session closed for %q, want %q noting the compact did not run and why (%q)", reason, compactReason, why)
	}
}

func TestAModdedClaudeSessionThatClosesCompactingCompactsOnceItsTurnEndsAndThenCloses(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaudeInATurn(t, w, app)

	closeCompactingDuringTheTurn(t, cli, run, session)
	stillOpen(t, cli, session, "during the turn that ran the close")

	run.Reply(compactingClaudeReply)
	awaitCompactReport(t, run, fakeagent.ModCompactCompacted)
	closed := awaitClosed(app, session)
	if protocol.Deref(closed.CloseReason) != compactReason || protocol.Deref(closed.ClosedBy) != session {
		t.Errorf("ledger entry = %+v, want it closed by itself for %q", closed, compactReason)
	}
}

func TestAModdedClaudeSessionThatClosesCompactingWhileBackgroundWorkRunsCompactsWhenItsTurnEnds(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaudeInATurn(t, w, app)

	closeCompactingDuringTheTurn(t, cli, run, session)
	run.ReplyLeavingBackgroundWork(compactingClaudeReply, "./deploy.sh staging")

	awaitCompactReport(t, run, fakeagent.ModCompactCompacted)
	closed := awaitClosed(app, session)
	if protocol.Deref(closed.CloseReason) != compactReason {
		t.Errorf("the session closed for %q, want %q", protocol.Deref(closed.CloseReason), compactReason)
	}
}

func TestACompactingCloseWhoseModWasLostMidTurnClosesOnceTheTurnParksOnBackgroundWork(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaudeWithVerdicts(t, w, app)
	workingOn(t, app, run, session, "deploy to staging")

	closeCompactingDuringTheTurn(t, cli, run, session)
	run.ModStopPolling()
	w.awaitTimer(daemon.ClaudeModReachTimer)
	w.advance(claudeModReachWindow)
	stillOpen(t, cli, session, "after the mod was lost during the turn that asked to close")

	parkOnBackgroundWork(t, w, cli, run, session)

	compactDidNotRun(t, awaitClosed(app, session), "no link that compacts")
}

func TestACompactThatClaudeSkipsStillClosesTheModdedSessionSayingSo(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaudeInATurn(t, w, app)
	run.ModSkipNextCompact("nothing to compact yet")

	closeCompactingDuringTheTurn(t, cli, run, session)
	run.Reply(compactingClaudeReply)
	awaitCompactReport(t, run, fakeagent.ModCompactSkipped)
	compactDidNotRun(t, awaitClosed(app, session), "nothing to compact yet")
}

func TestACompactThatFailsStillClosesTheModdedClaudeSessionSayingSo(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaudeInATurn(t, w, app)
	run.ModFailNextCompact("context window unavailable")

	closeCompactingDuringTheTurn(t, cli, run, session)
	run.Reply(compactingClaudeReply)
	awaitCompactReport(t, run, fakeagent.ModCompactFailed)
	compactDidNotRun(t, awaitClosed(app, session), "context window unavailable")
}

func TestACompactThatMeetsARunningTurnCompactsAtTheNextIdleAndThenCloses(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")
	session, run := moddedClaudeInATurn(t, w, app)
	run.ModBusyNextCompact(fakeagent.ModNewerTurnBegan)
	arrived, release := run.ModHoldNextCompact()

	closeCompactingDuringTheTurn(t, cli, run, session)
	run.Reply(compactingClaudeReply)
	arrived()
	mailBehindTheCompact(t, cli, session)
	release()
	awaitNewerTurnRefusal(t, run)

	ringOfTheMailBehindTheCompact(t, run)
	stillOpen(t, cli, session, "while the turn the compact met runs")
	run.Reply("Read the review. <!-- attn:state=idle -->")
	closed := awaitClosed(app, session)
	if protocol.Deref(closed.CloseReason) != compactReason {
		t.Errorf("the session closed for %q, want %q", protocol.Deref(closed.CloseReason), compactReason)
	}
	if got := run.ModCompactions(); got != 1 {
		t.Errorf("the mod compacted the conversation %d times, want once, at the next idle", got)
	}
}

func TestAPromptTheUserSendsAfterABusyCompactDoesNotStopTheClose(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaudeInATurn(t, w, app)
	run.ModBusyNextCompact(fakeagent.ModNewerTurnBegan)

	closeCompactingDuringTheTurn(t, cli, run, session)
	run.Reply(compactingClaudeReply)
	awaitNewerTurnRefusal(t, run)
	app.TypeLine(session, "wait, one more thing")
	if got := run.Prompted(); got != "wait, one more thing" {
		t.Fatalf("the agent took %q, want what the user typed", got)
	}
	run.Reply("Sure. <!-- attn:state=idle -->")

	if closed := awaitClosed(app, session); protocol.Deref(closed.CloseReason) != compactReason {
		t.Errorf("the session closed for %q, want %q", protocol.Deref(closed.CloseReason), compactReason)
	}
	if got := run.ModCompactions(); got != 1 {
		t.Errorf("the mod compacted the conversation %d times, want once", got)
	}
}

func TestAModReloadDuringTheCompactClosesTheClaudeSessionSayingTheCompactDidNotRun(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaudeInATurn(t, w, app)
	arrived, _ := run.ModHoldNextCompact()

	closeCompactingDuringTheTurn(t, cli, run, session)
	run.Reply(compactingClaudeReply)
	arrived()
	run.ModReload()
	compactDidNotRun(t, awaitClosed(app, session), "reloaded")
	run.AwaitGone()
}

func TestACompactingCloseOfAClaudeSessionWithoutTheModIsRefusedAskingForAReload(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaude(t, w, app)
	run.ModStopPolling()
	w.awaitTimer(daemon.ClaudeModReachTimer)
	w.advance(claudeModReachWindow)

	_, err := cli.AgentCloseCompacting(protocol.SessionID(session), compactReason)
	agentCloseRefused(t, err, "compact_unavailable", "attn agent reload")
	stillOpen(t, cli, session, "after the refused compacting close")
}
