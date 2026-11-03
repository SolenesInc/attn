package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPullRequestRecordedWhileTheSessionsConversationIsAppliedIsListedOnceAcknowledged(t *testing.T) {
	s := testworld.NewStack(t)
	applying := s.PauseAt(pausepoint.ConversationReportReady)
	awaited := s.PauseAt(pausepoint.ConversationApplyAwaited)
	s.Start()
	app, cli := s.App(), s.Client()
	driver := dialDriverAt(t, s.DialUnix, "snipe-plugin")
	driver.register("snipe", map[string]bool{"resume": true, "state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	session := s.Spawn(app, scriptedAgent, s.Path("shop"))
	run := driver.launched()
	reported := make(chan error, 1)
	go func() {
		reported <- driver.report("session.report_metadata", map[string]any{
			"session_id": run.SessionID, "run_id": run.RunID, "seq": 1,
			"metadata": map[string]string{"native_id": "first"}, "resume_session_id": "first",
		})
	}()
	applying.Await()
	release, err := recordDuringConversationApply(cli.RecordPullRequestCreated, session, applying, awaited)
	if err != nil {
		t.Fatalf("record while the session's conversation was applied: %v", err)
	}
	if got := pullNumbers(queriedSession(t, cli, session)); !slices.Equal(got, []int{4}) {
		t.Fatalf("pull requests right after the acknowledgement = %v, want [4]", got)
	}
	release()
	if err := <-reported; err != nil {
		t.Fatalf("conversation report: %v", err)
	}
}

func TestAPullRequestReportedFromTheTerminalWhileAClearIsAppliedStaysOnTheClearedSessionThroughItsResume(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	deciding := s.PauseAt(pausepoint.ConversationDecision)
	awaited := s.PauseAt(pausepoint.ConversationApplyAwaited)
	s.Start()
	app, cli := s.App(), s.Client()
	first := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	agent := s.Launched(first)
	terminal := protocol.TerminalID(app.Terminal(first))
	app.TypeLine(first, "add a discount field")
	agent.Prompted()
	conversation := agent.ConversationID
	agent.Reply("Added. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, first, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	app.TypeLine(first, "/clear")
	agent.Prompted()
	deciding.Await()
	fromTerminal := func(_ protocol.SessionID, url string) error {
		return cli.RecordPullRequestCreatedFromTerminal(terminal, url)
	}
	release, err := recordDuringConversationApply(fromTerminal, first, deciding, awaited)
	release()
	if err != nil {
		t.Fatalf("record from the terminal while /clear was applied: %v", err)
	}
	successor := awaitSuccessor(app, first)
	resumeClaude(app, agent, string(successor.ID), conversation)
	if back := awaitSuccessor(app, string(successor.ID)); string(back.ID) != first {
		t.Fatalf("/resume %s opened %s, want the cleared session %s", conversation, back.ID, first)
	}
	mark := len(app.Log())
	app.TypeLine(first, "keep going")
	agent.Prompted()
	agent.Reply("Going. <!-- attn:state=waiting_input -->")
	testworld.AwaitSessionSince(app, mark, first, func(x protocol.Session) bool {
		return x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.TurnOwed)
	})
	if got := pullNumbers(queriedSession(t, cli, first)); !slices.Equal(got, []int{4}) {
		t.Fatalf("pull requests of the resumed session after another turn = %v, want [4]", got)
	}
	s.Stop()
	s.Start()
	if got := pullNumbers(restartedSession(s.App(), first)); !slices.Equal(got, []int{4}) {
		t.Fatalf("pull requests of the resumed session after a restart = %v, want [4]", got)
	}
}

func recordDuringConversationApply(record func(protocol.SessionID, string) error, session string, applying, awaited *testworld.Pause) (releaseApply func(), err error) {
	recorded := make(chan error, 1)
	returned := make(chan struct{})
	go func() {
		recorded <- record(protocol.SessionID(session), shopPull(4))
		close(returned)
	}()
	if !awaited.AwaitBefore(returned) {
		return applying.Release, <-recorded
	}
	applying.Release()
	awaited.Release()
	return func() {}, <-recorded
}

func TestAPullRequestReportedOnAnOutpostWhileAClearMovesItsTerminalIsSavedOnTheClearedSessionAtTheHome(t *testing.T) {
	home := testworld.NewStack(t)
	deciding := home.PauseAt(pausepoint.ConversationDecision)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	clearApplied := outpost.PauseAt(pausepoint.OutpostConversationApplied)
	awaited := outpost.PauseAt(pausepoint.ConversationApplyAwaited)
	bound := outpost.PauseAt(pausepoint.ConversationCandidateBound)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	app := pair.App
	testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, row := range m.Outposts {
			if row.ID == pair.Host && row.Notebook != nil && row.Notebook.State == protocol.OutpostNotebookStateInSync {
				return true
			}
		}
		return false
	})
	first := home.Spawn(app, fakeagent.Claude, outpost.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	terminal := protocol.TerminalID(app.Terminal(first))
	agent := outpost.LaunchedCarrying(string(terminal))
	app.TypeLine(first, "add a discount field")
	agent.Prompted()
	conversation := agent.ConversationID
	agent.Reply("Added. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, first, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })

	app.TypeLine(first, "/clear")
	agent.Prompted()
	clearApplied.Await()
	deciding.Await()
	recorded := make(chan error, 1)
	go func() { recorded <- outpost.Client().RecordPullRequestCreatedFromTerminal(terminal, shopPull(4)) }()
	awaited.Await()
	deciding.Release()
	bound.Await()
	clearApplied.Release()
	awaited.Release()
	err := <-recorded
	bound.Release()
	if err != nil {
		t.Fatalf("record from the outpost terminal while /clear moved it: %v", err)
	}

	successor := awaitSuccessor(app, first)
	mark := len(app.Log())
	pair.Disconnect()
	awaitOutpostState(app, mark, pair.Host, protocol.OutpostStateLost)
	mark = len(app.Log())
	pair.Reconnect()
	stopRetries := home.FireTimers(bridge.RetryTimer)
	awaitOutpostState(app, mark, pair.Host, protocol.OutpostStateConnected)
	stopRetries()
	resumeClaude(app, agent, string(successor.ID), conversation)
	if back := awaitSuccessor(app, string(successor.ID)); string(back.ID) != first {
		t.Fatalf("/resume %s opened %s, want the cleared session %s", conversation, back.ID, first)
	}
	mark = len(app.Log())
	app.TypeLine(first, "keep going")
	agent.Prompted()
	agent.Reply("Going. <!-- attn:state=waiting_input -->")
	testworld.AwaitSessionSince(app, mark, first, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.TurnOwed)
	})
	if got := pullNumbers(queriedSession(t, home.Client(), first)); !slices.Equal(got, []int{4}) {
		t.Fatalf("pull requests of the resumed session at the home after a reconnect and another turn = %v, want [4]", got)
	}
}

func awaitOutpostState(app *testworld.Peer, mark int, host string, state protocol.OutpostState) {
	app.T.Helper()
	testworld.AwaitSince(app, mark, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, row := range m.Outposts {
			if row.ID == host && row.State == state {
				return true
			}
		}
		return false
	})
}
