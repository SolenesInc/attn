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

func TestSeedStandbyCloseCompactCompactsTheSharedCodexTenderBeforeItCloses(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
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

func TestACompactRefusedByADrainingCodexServerWaitsForTheAskingTurnToEndBeforeClosing(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	session, codex := sharedCodexInATurn(t, w, app)
	w.CodexServer().Drain()

	closeCompacting(t, cli, session)
	stillOpen(t, cli, session, "after the draining app-server refused the compact during the turn that asked to close")

	codex.Reply("Fixed and closing. <!-- attn:state=idle -->")
	closed := awaitClosed(app, session)
	if reason := protocol.Deref(closed.CloseReason); !strings.HasPrefix(reason, compactReason) || protocol.Deref(closed.ClosedBy) != session {
		t.Errorf("ledger entry = %+v, want it closed by itself for %q once its turn ended", closed, compactReason)
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

func TestACompactingCloseAskedAgainAfterTheUserClosedTheSessionDuringItsCompactCompactsAndCloses(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	session, codex := sharedCodexInATurn(t, w, app)
	conversation := codex.ConversationID
	w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("blog")))

	firstCompactArrived, releaseFirstCompact := w.CodexServer().HoldNextCompaction()
	closeCompacting(t, cli, session)
	codex.Reply("Fixed and closing. <!-- attn:state=idle -->")
	firstCompactArrived()
	if closed := closeFromApp(app, session); !closed.Accepted {
		t.Fatalf("the user closes the session during its compact = %+v", closed)
	}
	awaitClosed(app, session)
	w.CodexServer().AwaitArchived(conversation)
	releaseFirstCompact()
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(session)}); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	w.Launched(session)

	secondCompactArrived, releaseSecondCompact := w.CodexServer().HoldNextCompaction()
	closeCompacting(t, cli, session)
	secondCompactArrived()
	stillOpen(t, cli, session, "during the compact of the second close")
	releaseSecondCompact()
	closed := awaitClosed(app, session)
	if protocol.Deref(closed.CloseReason) != compactReason || protocol.Deref(closed.ClosedBy) != session {
		t.Errorf("ledger entry = %+v, want it closed by itself for %q", closed, compactReason)
	}
	if got := w.CodexServer().Compactions(conversation); got != 1 {
		t.Errorf("the conversation compacted %d times by the second close, want once: closing the session archived the conversation and ended the first compact", got)
	}
}
