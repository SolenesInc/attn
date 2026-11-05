package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
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
	if report.Outcome != outcome {
		t.Fatalf("the mod reported the compact as %+v, want %s", report, outcome)
	}
	return report
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

	closeCompacting(t, cli, session)
	if got := run.ModCompactRequests(); got != 0 {
		t.Fatalf("the mod received %d compact requests during the turn that ran the close", got)
	}
	stillOpen(t, cli, session, "during the turn that ran the close")

	run.Reply(compactingClaudeReply)
	awaitCompactReport(t, run, fakeagent.ModCompactCompacted)
	closed := awaitClosed(app, session)
	if protocol.Deref(closed.CloseReason) != compactReason || protocol.Deref(closed.ClosedBy) != session {
		t.Errorf("ledger entry = %+v, want it closed by itself for %q", closed, compactReason)
	}
	if got := run.ModCompactRequests(); got != 1 {
		t.Errorf("the mod received %d compact requests before the close, want one", got)
	}
}

func TestACompactThatClaudeSkipsStillClosesTheModdedSessionSayingSo(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaudeInATurn(t, w, app)
	run.ModSkipNextCompact("nothing to compact yet")

	closeCompacting(t, cli, session)
	run.Reply(compactingClaudeReply)
	awaitCompactReport(t, run, fakeagent.ModCompactSkipped)
	compactDidNotRun(t, awaitClosed(app, session), "nothing to compact yet")
}

func TestACompactThatFailsStillClosesTheModdedClaudeSessionSayingSo(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaudeInATurn(t, w, app)
	run.ModFailNextCompact("context window unavailable")

	closeCompacting(t, cli, session)
	run.Reply(compactingClaudeReply)
	awaitCompactReport(t, run, fakeagent.ModCompactFailed)
	compactDidNotRun(t, awaitClosed(app, session), "context window unavailable")
}

func TestACompactThatMeetsARunningTurnCompactsAtTheNextIdleAndThenCloses(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")
	session, run := moddedClaudeInATurn(t, w, app)
	run.ModBusyNextCompact("a turn is running")
	arrived, release := run.ModHoldNextCompact()

	closeCompacting(t, cli, session)
	run.Reply(compactingClaudeReply)
	arrived()
	mailBehindTheCompact(t, cli, session)
	release()
	awaitCompactReport(t, run, fakeagent.ModCompactBusy)

	ringOfTheMailBehindTheCompact(t, run)
	stillOpen(t, cli, session, "while the turn the compact met runs")
	run.Reply("Read the review. <!-- attn:state=idle -->")
	awaitCompactReport(t, run, fakeagent.ModCompactCompacted)
	closed := awaitClosed(app, session)
	if protocol.Deref(closed.CloseReason) != compactReason {
		t.Errorf("the session closed for %q, want %q", protocol.Deref(closed.CloseReason), compactReason)
	}
	if got := run.ModCompactRequests(); got != 2 {
		t.Errorf("the mod received %d compact requests, want the busy one and the one at the next idle", got)
	}
}

func TestUserInputAfterABusyCompactKeepsTheModdedClaudeSessionOpen(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaudeInATurn(t, w, app)
	run.ModBusyNextCompact("a turn is running")

	closeCompacting(t, cli, session)
	run.Reply(compactingClaudeReply)
	awaitCompactReport(t, run, fakeagent.ModCompactBusy)
	app.TypeLine(session, "wait, one more thing")
	if got := run.Prompted(); got != "wait, one more thing" {
		t.Fatalf("the agent took %q, want what the user typed", got)
	}
	run.Reply("Sure. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	nothingElsePrompted(t, app, run, session)
	run.Reply("Done. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	stillOpen(t, cli, session, "after the user typed past the busy compact")
	if got := run.ModCompactRequests(); got != 1 {
		t.Errorf("the mod received %d compact requests, want only the busy one", got)
	}
}

func TestAModReloadDuringTheCompactClosesTheClaudeSessionSayingTheCompactDidNotRun(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaudeInATurn(t, w, app)
	arrived, release := run.ModHoldNextCompact()

	closeCompacting(t, cli, session)
	run.Reply(compactingClaudeReply)
	arrived()
	run.ModReload()
	compactDidNotRun(t, awaitClosed(app, session), "reloaded")
	release()
}

func TestUserInputDuringTheCompactKeepsTheModdedClaudeSessionOpen(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")
	session, run := moddedClaudeInATurn(t, w, app)
	arrived, release := run.ModHoldNextCompact()

	closeCompacting(t, cli, session)
	run.Reply(compactingClaudeReply)
	arrived()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWorking && protocol.Deref(s.StateReason) == "compacting"
	})
	app.TypeLine(session, "wait, one more thing")
	if got := run.Prompted(); got != "wait, one more thing" || run.ThroughMod {
		t.Fatalf("the agent took %q (through the mod: %t), want what the user typed", got, run.ThroughMod)
	}
	mailBehindTheCompact(t, cli, session)
	release()
	awaitCompactReport(t, run, fakeagent.ModCompactCompacted)

	run.Reply("Sure. <!-- attn:state=idle -->")
	ringOfTheMailBehindTheCompact(t, run)
	stillOpen(t, cli, session, "after the user typed during the compact")
}

func TestACompactingCloseOfAClaudeSessionWithoutTheModIsRefusedNamingTheModSetting(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(session)

	_, err := cli.AgentCloseCompacting(protocol.SessionID(session), compactReason)
	agentCloseRefused(t, err, "compact_unavailable", claudeModSetting)
	stillOpen(t, cli, session, "after the refused compacting close")
}
