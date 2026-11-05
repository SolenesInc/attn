package daemon_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const compactReason = "the checkout fix landed"

func sharedCodexInATurn(t *testing.T, w *world, app *testworld.Peer) (string, *fakeagent.Run) {
	t.Helper()
	setSetting(t, app, "codex_shared_enabled", "true")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	app.TypeLine(session, "fix the flaky checkout test")
	if got := codex.Prompted(); got != "fix the flaky checkout test" {
		t.Fatalf("codex received %q", got)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	return session, codex
}

func closeCompacting(t *testing.T, cli *client.Client, session string) {
	t.Helper()
	pending, err := cli.AgentCloseCompacting(protocol.SessionID(session), compactReason)
	if err != nil {
		t.Fatalf("agent close --self --compact: %v", err)
	}
	if !protocol.Deref(pending.PendingCompact) || string(pending.TargetSessionID) != session {
		t.Fatalf("agent close --self --compact = %+v, want %s pending its compact", pending, session)
	}
}

func stillOpen(t *testing.T, cli *client.Client, session, what string) {
	t.Helper()
	if !slices.Contains(queriedIDs(t, cli, ""), session) {
		t.Fatalf("%s: session %s closed, want it open", what, session)
	}
}

func TestASharedCodexSessionThatClosesCompactingCompactsOnceItsTurnEndsAndThenCloses(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	session, codex := sharedCodexInATurn(t, w, app)
	conversation := codex.ConversationID

	closeCompacting(t, cli, session)
	if got := w.CodexServer().Compactions(conversation); got != 0 {
		t.Fatalf("the conversation compacted %d times during the turn that ran the close", got)
	}
	stillOpen(t, cli, session, "during the turn that ran the close")

	codex.Reply("Fixed and closing. <!-- attn:state=idle -->")
	closed := awaitClosed(app, session)
	if protocol.Deref(closed.CloseReason) != compactReason || protocol.Deref(closed.ClosedBy) != session {
		t.Errorf("ledger entry = %+v, want it closed by itself for %q", closed, compactReason)
	}
	if got := w.CodexServer().Compactions(conversation); got != 1 {
		t.Errorf("the conversation compacted %d times before the close, want once", got)
	}
}

func TestUserInputAfterACompactingCloseKeepsTheSharedCodexSessionOpenUncompacted(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	session, codex := sharedCodexInATurn(t, w, app)
	conversation := codex.ConversationID

	closeCompacting(t, cli, session)
	if sent := submitSessionAnnotationFeedback(app, session, "also cover the tax lookup"); !sent.success {
		t.Fatalf("the user's notes to the working session = %+v", sent)
	}
	if got := w.CodexServer().Prompted(conversation); got != "also cover the tax lookup" {
		t.Fatalf("the running turn received %q", got)
	}
	codex.Reply("Covered. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	nothingElsePrompted(t, app, codex, session)
	codex.Reply("Done. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	stillOpen(t, cli, session, "after the user typed past the compacting close")
	if got := w.CodexServer().Compactions(conversation); got != 0 {
		t.Errorf("the conversation compacted %d times after the user typed, want none", got)
	}
}

func TestSeedStandbyCloseCompactCompactsTheSharedCodexTenderBeforeItCloses(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "codex_shared_enabled", "true")
	if err := os.MkdirAll(w.Path("api"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.SetCodexFolderTrust(w.Path("api"), "trusted")
	delegated := seedResumeDelegate(t, w, fakeagent.Codex, "api")
	session := string(delegated.SessionID)
	tender := w.Launched(session)
	tender.Prompted()
	conversation := tender.ConversationID

	set, err := cli.WithGardenProfile(protocol.Deref(delegated.ProfileID), delegated.SessionID).
		SeedStandby(delegated.SeedID, client.SeedStandbyOptions{Note: standbyNote, Close: true, Compact: true})
	if err != nil {
		t.Fatalf("seed standby --close --compact: %v", err)
	}
	if set.Seed.Standby == nil || set.Close == nil || !protocol.Deref(set.Close.PendingCompact) {
		t.Fatalf("seed standby --close --compact = %+v, want the seed on standby and the close pending its compact", set)
	}
	if got := w.CodexServer().Compactions(conversation); got != 0 {
		t.Fatalf("the conversation compacted %d times during the turn that set the standby", got)
	}
	stillOpen(t, cli, session, "during the turn that set the standby")

	tender.Reply("On standby. <!-- attn:state=idle -->")
	closed := awaitClosed(app, session)
	if reason := protocol.Deref(closed.CloseReason); !strings.Contains(reason, "On standby for "+delegated.SeedID) {
		t.Errorf("the tender closed for %q, want the standby reason", reason)
	}
	if got := w.CodexServer().Compactions(conversation); got != 1 {
		t.Errorf("the conversation compacted %d times before the close, want once", got)
	}
}

func TestACompactingCloseOfAPlainCodexSessionIsRefusedNamingTheSharedSetting(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	w.Launched(session)

	_, err := cli.AgentCloseCompacting(protocol.SessionID(session), compactReason)
	agentCloseRefused(t, err, "compact_unavailable", "codex_shared_enabled")
	stillOpen(t, cli, session, "after the refused compacting close")
}

func TestACompactThatFailsStillClosesTheSharedCodexSessionSayingSo(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	session, codex := sharedCodexInATurn(t, w, app)
	w.CodexServer().RefuseNextCompaction("context window unavailable")

	closeCompacting(t, cli, session)
	codex.Reply("Fixed and closing. <!-- attn:state=idle -->")
	closed := awaitClosed(app, session)
	reason := protocol.Deref(closed.CloseReason)
	if !strings.HasPrefix(reason, compactReason) || !strings.Contains(reason, "the compact did not run") || !strings.Contains(reason, "context window unavailable") {
		t.Errorf("the session closed for %q, want %q noting the compact did not run and why", reason, compactReason)
	}
	if got := w.CodexServer().Compactions(codex.ConversationID); got != 0 {
		t.Errorf("a refused compact counted %d compactions", got)
	}
}

func TestMailThatArrivesWhileTheCompactRunsWaitsUntilTheSessionHasClosed(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")
	session, codex := sharedCodexInATurn(t, w, app)
	arrived, release := w.CodexServer().HoldNextCompaction()

	closeCompacting(t, cli, session)
	codex.Reply("Fixed and closing. <!-- attn:state=idle -->")
	arrived()
	sent := sendAgentMessage(t, cli, "sender", session, "the review is in")
	if sent.Status != protocol.AgentMsgStatusQueued || !strings.Contains(sent.Detail, "compacting") {
		t.Fatalf("mail during the compact = %+v, want it queued behind the close", sent)
	}
	release()
	awaitClosed(app, session)
	if got := w.CodexServer().Compactions(codex.ConversationID); got != 1 {
		t.Errorf("the conversation compacted %d times before the close, want once", got)
	}
	status, err := cli.AgentMsgStatus(sent.MessageID, "sender")
	if err != nil || status.State != protocol.AgentMessageStateQueued || status.NotifiedAt != nil {
		t.Errorf("mail sent during the compact is %+v, %v after the close, want it still queued and never rung", status, err)
	}
}

func TestARingThatWinsTheTurnsEndIsReadBeforeTheCompactingCloseCompactsAtTheNextIdle(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")
	session, codex := sharedCodexInATurn(t, w, app)
	conversation := codex.ConversationID

	closeCompacting(t, cli, session)
	placing := w.PauseAt(pausepoint.CompactingClosePlacing)
	sent := sendAgentMessage(t, cli, "sender", session, "the review is in")
	codex.Reply("Fixed and closing. <!-- attn:state=idle -->")
	placing.Await()
	if rang := w.CodexServer().Prompted(conversation); rang == "" {
		t.Fatal("the ring at the turn's end reached no turn")
	}
	placing.Release()
	stillOpen(t, cli, session, "while the ring's turn runs")
	if got := w.CodexServer().Compactions(conversation); got != 0 {
		t.Fatalf("the conversation compacted %d times during the ring's turn", got)
	}

	codex.Reply("Read the review. <!-- attn:state=idle -->")
	closed := awaitClosed(app, session)
	if protocol.Deref(closed.CloseReason) != compactReason {
		t.Errorf("the session closed for %q, want %q", protocol.Deref(closed.CloseReason), compactReason)
	}
	if got := w.CodexServer().Compactions(conversation); got != 1 {
		t.Errorf("the conversation compacted %d times before the close, want once", got)
	}
	status, err := cli.AgentMsgStatus(sent.MessageID, "sender")
	if err != nil || status.NotifiedAt == nil {
		t.Errorf("the ring's mail is %+v, %v, want it rung in the turn before the close", status, err)
	}
}

func TestUserInputDuringTheCompactKeepsTheSharedCodexSessionOpen(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	session, codex := sharedCodexInATurn(t, w, app)
	arrived, release := w.CodexServer().HoldNextCompaction()

	closeCompacting(t, cli, session)
	codex.Reply("Fixed and closing. <!-- attn:state=idle -->")
	arrived()
	app.TypeLine(session, "wait, one more thing")
	release()
	if got := codex.Prompted(); got != "wait, one more thing" {
		t.Fatalf("after the compact the conversation received %q, want what the user typed", got)
	}
	codex.Reply("Sure. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if got := w.CodexServer().Compactions(codex.ConversationID); got != 1 {
		t.Errorf("the conversation compacted %d times, want once", got)
	}
	stillOpen(t, cli, session, "after the user typed during the compact")
}

func TestACompactingCloseAskedAgainAfterTheUserClosedTheSessionDuringItsCompactCompactsAndCloses(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	session, codex := sharedCodexInATurn(t, w, app)
	conversation := codex.ConversationID
	w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("blog")))

	arrived, release := w.CodexServer().HoldNextCompaction()
	closeCompacting(t, cli, session)
	codex.Reply("Fixed and closing. <!-- attn:state=idle -->")
	arrived()
	if closed := closeFromApp(app, session); !closed.Accepted {
		t.Fatalf("the user closes the session during its compact = %+v", closed)
	}
	awaitClosed(app, session)
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(session)}); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	reopened := w.Launched(session)
	app.TypeLine(session, "finish the checkout fix")
	if got := reopened.Prompted(); got != "finish the checkout fix" {
		t.Fatalf("the reopened session received %q", got)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	closeCompacting(t, cli, session)
	release()
	reopened.Reply("Finished and closing. <!-- attn:state=idle -->")
	closed := awaitClosed(app, session)
	if protocol.Deref(closed.CloseReason) != compactReason || protocol.Deref(closed.ClosedBy) != session {
		t.Errorf("ledger entry = %+v, want it closed by itself for %q", closed, compactReason)
	}
	if got := w.CodexServer().Compactions(conversation); got != 2 {
		t.Errorf("the conversation compacted %d times by the second close, want twice", got)
	}
}

func TestInputTheUserSendsThroughAttnDuringTheCompactIsRefusedAndKeepsTheSessionOpen(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")
	session, codex := sharedCodexInATurn(t, w, app)
	arrived, release := w.CodexServer().HoldNextCompaction()

	closeCompacting(t, cli, session)
	codex.Reply("Fixed and closing. <!-- attn:state=idle -->")
	arrived()
	sent := submitSessionAnnotationFeedback(app, session, "wait, one more thing")
	if sent.success || !strings.Contains(sent.err, "send it again") {
		t.Fatalf("the user's notes during the compact = %+v, want them refused, asking to send them again", sent)
	}
	sendAgentMessage(t, cli, "sender", session, "the review is in")
	release()
	if rang := w.CodexServer().Prompted(codex.ConversationID); rang == "" {
		t.Fatal("mail held during the compact reached no turn after the close was cancelled")
	}
	stillOpen(t, cli, session, "after the user sent notes during the compact")
}
