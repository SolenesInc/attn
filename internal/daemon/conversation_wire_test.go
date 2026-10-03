package daemon_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
	"github.com/victorarias/attn/internal/transcript"
)

func respawn(w *world, app *testworld.Peer, agent fakeagent.Harness, session, cwd string) *fakeagent.Run {
	w.T.Helper()
	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: session})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.SessionID == session })
	return revive(w, app, agent, session, cwd)
}

func revive(w *world, app *testworld.Peer, agent fakeagent.Harness, session, cwd string) *fakeagent.Run {
	w.T.Helper()
	w.Spawn(app, agent, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = session
		m.ResumeSessionID = protocol.Ptr(session)
	})
	return w.Launched(session)
}

func awaitSuccessor(app *testworld.Peer, predecessor string) protocol.Session {
	app.T.Helper()
	return *testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && protocol.Deref(e.Session.Succeeds) == predecessor
	}).Session
}

func clearClaude(app *testworld.Peer, claude *fakeagent.Run, session string) protocol.Session {
	app.T.Helper()
	app.TypeLine(session, "/clear")
	if got := claude.Prompted(); got != "/clear" {
		app.T.Fatalf("claude received %q, want /clear", got)
	}
	return awaitSuccessor(app, session)
}

func TestClearOpensANewSessionInTheSameTerminalAndClosesTheOldOneWhole(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	first := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("checkout")
	})
	terminal := app.Terminal(first)
	claude := w.Launched(first)
	app.TypeLine(first, "add a discount field")
	claude.Prompted()
	added := "Added. <!-- attn:state=idle -->"
	claude.Reply(added)
	awaitUsageTokens(app, first, claudeTokens(added))
	cleared := claude.ConversationID

	next := clearClaude(app, claude, first)
	if next.ID == first || next.Label != "shop" {
		t.Errorf("after /clear the terminal shows %s named %q, want a new session named like a new one in shop", next.ID, next.Label)
	}
	if got := app.Terminal(next.ID); got != terminal {
		t.Errorf("the new session runs in terminal %s, want %s, the one /clear ran in", got, terminal)
	}
	closed := awaitClosed(app, first)
	if !strings.Contains(protocol.Deref(closed.CloseReason), next.ID) || closed.Label != "checkout" {
		t.Errorf("the cleared session's ledger row = %+v, want it closed under its own name naming %s", closed, next.ID)
	}
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && e.Session.ID == first
	})
	if got, want := handoverEvents(app.Received(), first, next.ID), []string{
		protocol.EventSessionRegistered, protocol.EventWorkspaceLayoutUpdated, protocol.EventSessionClosed, protocol.EventSessionUnregistered,
	}; !slices.Equal(got, want) {
		t.Errorf("the app heard the handover as %v, want %v so it never shows the terminal without a session", got, want)
	}

	app.TypeLine(next.ID, "now the tests")
	if got := claude.Prompted(); got != "now the tests" {
		t.Fatalf("typing after /clear reached claude as %q", got)
	}
	testworld.AwaitSession(app, next.ID, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	tested := "Which framework? <!-- attn:state=waiting_input -->"
	claude.Reply(tested)
	testworld.AwaitSession(app, next.ID, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && s.Usage != nil && s.Usage.TotalTokens == claudeTokens(tested)
	})

	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: first}); err != nil {
		t.Fatalf("reopen %s: %v", first, err)
	}
	reopened := w.Launched(first)
	if !reopened.Resumed || reopened.ConversationID != cleared {
		t.Errorf("reopening the cleared session ran claude %q, want it resuming %s", reopened.Argv, cleared)
	}
	if got := app.Terminal(first); got == terminal {
		t.Errorf("the reopened session took terminal %s back from its successor", terminal)
	}
	if usage := queriedSession(t, cli, first).Usage; usage == nil || usage.TotalTokens != claudeTokens(added) {
		t.Errorf("the reopened session's usage = %+v, want the %d tokens it spent before /clear", usage, claudeTokens(added))
	}

	claude.Exit(1)
	exited := testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == terminal })
	if exited.SessionID != next.ID {
		t.Errorf("the exit of terminal %s ended session %s, want %s", terminal, exited.SessionID, next.ID)
	}
	testworld.AwaitSession(app, next.ID, func(s protocol.Session) bool { return protocol.Deref(s.StateReason) == "process_exited" })
	if reason := protocol.Deref(queriedSession(t, cli, first).StateReason); reason == "process_exited" {
		t.Errorf("the exit of %s's terminal ended the reopened %s too", next.ID, first)
	}
}

// handoverEvents lists, in order, the first event of each step of from's terminal moving on to to.
func handoverEvents(events []protocol.WebSocketEvent, from, to string) []string {
	var heard []string
	for _, e := range events {
		var step bool
		switch e.Event {
		case protocol.EventSessionRegistered:
			step = e.Session != nil && e.Session.ID == to
		case protocol.EventWorkspaceLayoutUpdated:
			step = e.WorkspaceLayout != nil && slices.ContainsFunc(e.WorkspaceLayout.Panes, func(p protocol.WorkspaceLayoutPane) bool {
				return protocol.Deref(p.SessionID) == to
			})
		case protocol.EventSessionClosed:
			step = e.SessionLedgerEntry != nil && e.SessionLedgerEntry.ID == from
		case protocol.EventSessionUnregistered:
			step = e.Session != nil && e.Session.ID == from
		}
		if step && !slices.Contains(heard, e.Event) {
			heard = append(heard, e.Event)
		}
	}
	return heard
}

func resumeClaude(app *testworld.Peer, claude *fakeagent.Run, session, conversation string) {
	app.T.Helper()
	app.TypeLine(session, "/resume "+conversation)
	if got := claude.Prompted(); got != "/resume "+conversation {
		app.T.Fatalf("claude received %q, want /resume %s", got, conversation)
	}
}

func TestResumeToAClosedSessionsConversationReopensItInThePane(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	first := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("checkout")
	})
	terminal := app.Terminal(first)
	claude := w.Launched(first)
	app.TypeLine(first, "add a discount field")
	claude.Prompted()
	added := "Added. <!-- attn:state=idle -->"
	claude.Reply(added)
	awaitUsageTokens(app, first, claudeTokens(added))
	checkout := claude.ConversationID
	cleared := clearClaude(app, claude, first)
	awaitClosed(app, first)

	heard := len(app.Received())
	resumeClaude(app, claude, cleared.ID, checkout)
	back := awaitSuccessor(app, cleared.ID)
	if back.ID != first || back.Label != "checkout" {
		t.Fatalf("/resume %s brought back %s named %q, want %s, the closed session that holds it", checkout, back.ID, back.Label, first)
	}
	if got := app.Terminal(first); got != terminal {
		t.Errorf("%s came back in terminal %s, want %s, the one /resume ran in", first, got, terminal)
	}
	awaitClosed(app, cleared.ID)
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && e.Session.ID == cleared.ID
	})
	if got, want := handoverEvents(app.Received()[heard:], cleared.ID, first), []string{
		protocol.EventSessionRegistered, protocol.EventWorkspaceLayoutUpdated, protocol.EventSessionClosed, protocol.EventSessionUnregistered,
	}; !slices.Equal(got, want) {
		t.Errorf("the app heard the handover as %v, want %v so it follows the terminal", got, want)
	}
	if ids := queriedIDs(t, cli, ""); !slices.Equal(ids, []string{first}) {
		t.Errorf("after /clear and /resume back the sessions are %v, want only %s", ids, first)
	}

	app.TypeLine(first, "now the tests")
	if got := claude.Prompted(); got != "now the tests" || claude.ConversationID != checkout {
		t.Fatalf("typing after /resume reached claude as %q in conversation %s, want it in %s", got, claude.ConversationID, checkout)
	}
	testworld.AwaitSession(app, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	tested := "Which framework? <!-- attn:state=waiting_input -->"
	claude.Reply(tested)
	testworld.AwaitSession(app, first, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && s.Usage != nil &&
			s.Usage.TotalTokens == claudeTokens(added)+claudeTokens(tested)
	})
}

func TestResumeToARecoverableSessionsConversationMovesItIntoThePane(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	earlier := w.Spawn(app, fakeagent.Claude, cwd)
	dead := app.Terminal(earlier)
	before := w.Launched(earlier)
	app.TypeLine(earlier, "find the flaky test")
	before.Prompted()
	before.Reply("It races the tax lookup. <!-- attn:state=idle -->")
	flaky := before.ConversationID
	before.Exit(1)
	testworld.AwaitSession(app, earlier, func(s protocol.Session) bool { return protocol.Deref(s.StateReason) == "process_exited" })

	current := w.Spawn(app, fakeagent.Claude, cwd)
	terminal := app.Terminal(current)
	claude := w.Launched(current)
	resumeClaude(app, claude, current, flaky)
	if moved := awaitSuccessor(app, current); moved.ID != earlier {
		t.Fatalf("/resume %s showed session %s, want %s, the recoverable session that holds it", flaky, moved.ID, earlier)
	}
	awaitClosed(app, current)
	layout := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WebSocketEvent) bool {
		return e.WorkspaceLayout != nil && slices.ContainsFunc(e.WorkspaceLayout.Panes, func(p protocol.WorkspaceLayoutPane) bool {
			return protocol.Deref(p.RuntimeID) == terminal && protocol.Deref(p.SessionID) == earlier
		})
	}).WorkspaceLayout
	for _, pane := range layout.Panes {
		if protocol.Deref(pane.RuntimeID) == dead {
			t.Errorf("after %s moved into terminal %s its dead pane %s stayed: %+v", earlier, terminal, pane.PaneID, layout.Panes)
		}
	}

	app.TypeLine(earlier, "fix it")
	if got := claude.Prompted(); got != "fix it" {
		t.Fatalf("typing after /resume reached claude as %q", got)
	}
	testworld.AwaitSession(app, earlier, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	claude.Reply("Fixed with a lock. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, earlier, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

func TestACrewMembersClearEndsItsDay(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	woken := wakeCrew(t, cli, "trellis", "")
	day := w.Launched(woken.SessionID)
	day.Prompted()
	day.Reply("Ready. <!-- attn:state=idle -->")

	next := clearClaude(app, day, woken.SessionID)
	awaitClosed(app, woken.SessionID)
	if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
		t.Errorf("after the day's /clear trellis is bound to %s, want its day ended", *binding)
	}
	if member := protocol.Deref(queriedSession(t, cli, next.ID).CrewMember); member != "" {
		t.Errorf("the session after /clear works as crew member %q, want it unbound", member)
	}
}

func TestADelegateThatClearsLeavesItsSeedAndMailWithItsClosedSession(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	delegated := seedResumeDelegate(t, w, fakeagent.Claude, "api")
	delegate, seed := delegated.SessionID, delegated.SeedID
	claude := w.Launched(delegate)
	claude.Prompted()
	claude.Reply("Looking into it. <!-- attn:state=idle -->")

	next := clearClaude(app, claude, delegate)
	awaitClosed(app, delegate)
	if tender := lifeShow(t, cli, seed).Seed.TenderSession; tender != delegate {
		t.Errorf("after /clear seed %s is tended by %q, want the cleared %s", seed, tender, delegate)
	}
	if sent, err := cli.AgentMsg(delegate, next.ID, "are you still on it?"); err == nil {
		t.Errorf("a message to the cleared %s = %+v, want it refused as for any closed session", delegate, sent)
	}
	if sent := sendAgentMessage(t, cli, next.ID, seed, "the deployment is ready"); sent.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("mail for the cleared tender's seed = %+v, want it queued", sent)
	}

	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: delegate}); err != nil {
		t.Fatalf("reopen %s: %v", delegate, err)
	}
	back := w.Launched(delegate)
	app.TypeLine(delegate, "where were we?")
	back.Prompted()
	back.Reply("On the tracked task. <!-- attn:state=idle -->")
	if got := back.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("the reopened tender was prompted with %q, want the inbox doorbell", got)
	}
	if mail := readInbox(t, cli, delegate, 0).Items; len(mail) != 1 || mail[0].Content != "the deployment is ready" {
		t.Errorf("the reopened tender's inbox = %+v, want the mail that queued while it was closed", mail)
	}
}

func TestAClearedTerminalComesBackShowingItsNewSessionInItsNewConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	first := w.Spawn(app, fakeagent.Claude, cwd)
	terminal := app.Terminal(first)
	claude := w.Launched(first)
	app.TypeLine(first, "add a discount field")
	claude.Prompted()
	claude.Reply("Added. <!-- attn:state=idle -->")
	next := clearClaude(app, claude, first)
	started := claude.ConversationID

	w.restart()
	app = w.App()
	if slices.ContainsFunc(app.Initial.Sessions, func(s protocol.Session) bool { return s.ID == first }) {
		t.Errorf("the session /clear replaced came back after a restart")
	}
	if got := app.Terminal(next.ID); got != terminal {
		t.Errorf("after a restart %s runs in terminal %s, want %s", next.ID, got, terminal)
	}
	resumed := revive(w, app, fakeagent.Claude, next.ID, cwd)
	if !resumed.Resumed || resumed.ConversationID != started {
		t.Fatalf("reviving %s ran claude %q, want it resuming %s, the conversation /clear started", next.ID, resumed.Argv, started)
	}
	app.TypeLine(next.ID, "still there?")
	resumed.Prompted()
	resumed.Reply("Still here. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, next.ID, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

func TestCodexNewOpensANewSessionOnTheNewChatsFirstPrompt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := w.Path("shop")
	first := w.Spawn(app, fakeagent.Codex, cwd)
	codex := w.Launched(first)
	app.TypeLine(first, "find the flaky test")
	codex.Prompted()
	codex.Reply("It races the tax lookup. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	launched := codex.ConversationID

	app.TypeLine(first, "/new")
	if got := codex.Prompted(); got != "/new" {
		t.Fatalf("codex received %q, want /new", got)
	}
	if ids := queriedIDs(t, cli, ""); !slices.Equal(ids, []string{first}) {
		t.Fatalf("before the new chat's first prompt the sessions are %v, want only %s", ids, first)
	}
	app.TypeLine(first, "now fix it")
	codex.Prompted()
	next := awaitSuccessor(app, first)
	started := codex.ConversationID
	if started == launched {
		t.Fatalf("/new kept codex in conversation %s", launched)
	}
	awaitClosed(app, first)
	testworld.AwaitSession(app, next.ID, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	codex.Reply("Fixed with a lock. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, next.ID, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	resumed := respawn(w, app, fakeagent.Codex, next.ID, cwd)
	if !resumed.Resumed || resumed.ConversationID != started {
		t.Fatalf("respawn ran codex %q; want it to resume %s, the conversation /new started", resumed.Argv, started)
	}
}

func TestCodexResumeReopensTheClosedSessionOnTheNextPrompt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	first := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	terminal := app.Terminal(first)
	codex := w.Launched(first)
	app.TypeLine(first, "find the flaky test")
	codex.Prompted()
	codex.Reply("It races the tax lookup. <!-- attn:state=idle -->")
	flaky := codex.ConversationID
	app.TypeLine(first, "/new")
	codex.Prompted()
	app.TypeLine(first, "write the changelog")
	codex.Prompted()
	next := awaitSuccessor(app, first)
	awaitClosed(app, first)
	codex.Reply("Written. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, next.ID, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	app.TypeLine(next.ID, "/resume "+flaky)
	codex.Prompted()
	app.TypeLine(next.ID, "now fix it")
	if got := codex.Prompted(); got != "now fix it" || codex.ConversationID != flaky {
		t.Fatalf("codex took %q in conversation %s, want it in %s", got, codex.ConversationID, flaky)
	}
	if back := awaitSuccessor(app, next.ID); back.ID != first {
		t.Fatalf("/resume %s showed session %s, want %s, the closed session that holds it", flaky, back.ID, first)
	}
	if got := app.Terminal(first); got != terminal {
		t.Errorf("%s came back in terminal %s, want %s", first, got, terminal)
	}
	awaitClosed(app, next.ID)
	testworld.AwaitSession(app, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	codex.Reply("Fixed with a lock. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestCodexResumeFromAFreshPaneToASessionRunningInAnotherPaneShowsItInBoth(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	shared := runInTwoTerminals(w, app)
	shared.secondRun.Reply("Which lock? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, shared.id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

func TestASessionLaunchedToResumeAConversationKeepsResumingIt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	cwd := w.Path("api")
	earlier := w.Spawn(app, fakeagent.Codex, cwd)
	conversation := w.Launched(earlier).ConversationID
	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: earlier})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.SessionID == earlier })

	session := w.Spawn(app, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr(conversation)
	})
	if launched := w.Launched(session); !launched.Resumed || launched.ConversationID != conversation {
		t.Fatalf("a launch resuming %s ran codex %q", conversation, launched.Argv)
	}
	if resumed := respawn(w, app, fakeagent.Codex, session, cwd); !resumed.Resumed || resumed.ConversationID != conversation {
		t.Fatalf("respawn ran codex %q, want it to resume %s", resumed.Argv, conversation)
	}
}

func TestARelaunchThatCannotResumeKeepsTheSessionAndAdoptsItsNewConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	app.TypeLine(session, "find the flaky test")
	codex.Prompted()
	codex.Reply("It races the tax lookup. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	lost := transcript.FindCodexTranscriptForResume(codex.ConversationID)
	if lost == "" {
		t.Fatalf("no rollout for codex conversation %s", codex.ConversationID)
	}
	if err := os.Remove(lost); err != nil {
		t.Fatal(err)
	}

	reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: session, Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return r.ID == session })
	if !reloaded.Success {
		t.Fatalf("reload failed: %s", protocol.Deref(reloaded.Error))
	}
	fresh := w.Launched(session)
	if fresh.Resumed {
		t.Fatalf("the reload resumed %q although its rollout is gone", fresh.Argv)
	}
	app.TypeLine(session, "run the tests")
	fresh.Prompted()
	fresh.Reply("Green. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	for _, e := range app.Received() {
		if e.Event == protocol.EventSessionRegistered && e.Session != nil && protocol.Deref(e.Session.Succeeds) == session {
			t.Fatalf("a relaunch of %s opened session %s after it", session, e.Session.ID)
		}
	}
	resumed := respawn(w, app, fakeagent.Codex, session, w.Path("shop"))
	if !resumed.Resumed || resumed.ConversationID != fresh.ConversationID {
		t.Fatalf("respawn ran codex %q; want it to resume %s, the conversation the relaunch started", resumed.Argv, fresh.ConversationID)
	}
}
