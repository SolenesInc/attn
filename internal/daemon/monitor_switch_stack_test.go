package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSwitchesBeforeAnOutcomeEachGetASessionAndTheirOwnCost(t *testing.T) {
	for _, point := range []string{pausepoint.ConversationSwitchDecision, pausepoint.ConversationSwitchOutcome} {
		t.Run(point, func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			outcome := s.PauseAt(point)
			s.Start()
			app := s.App()
			first := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
			agent := s.Launched(first)
			terminal := app.Terminal(first)
			app.TypeLine(first, "first prompt")
			agent.Prompted()
			firstReply := "First done. <!-- attn:state=waiting_input -->"
			agent.Reply(firstReply)
			awaitUsageTokens(app, first, claudeTokens(firstReply))
			app.TypeLine(first, "/clear")
			agent.Prompted()
			outcome.Await()
			var middle protocol.Session
			if point == pausepoint.ConversationSwitchOutcome {
				middle = awaitSuccessor(app, first)
			}
			typeInto(app, terminal, "middle prompt\r")
			agent.Prompted()
			middleReply := "Middle done. <!-- attn:state=waiting_input -->"
			agent.Reply(middleReply)
			typeInto(app, terminal, "/clear\r")
			agent.Prompted()
			typeInto(app, terminal, "last prompt\r")
			agent.Prompted()
			lastReply := "Last done. <!-- attn:state=waiting_input -->"
			agent.Reply(lastReply)
			outcome.Release()
			if middle.ID == "" {
				middle = awaitSuccessor(app, first)
			}
			last := awaitSuccessor(app, string(middle.ID))
			closed := awaitClosed(app, string(middle.ID))
			if closed.Usage == nil || closed.Usage.TotalTokens != claudeTokens(middleReply) {
				t.Fatalf("middle conversation cost = %+v, want %d tokens", closed.Usage, claudeTokens(middleReply))
			}
			lastUsage := awaitUsageTokens(app, string(last.ID), claudeTokens(lastReply))
			t.Logf("daemon terminal=%s chain=%s -> %s -> %s middle_tokens=%d last_tokens=%d", terminal, first, middle.ID, last.ID, closed.Usage.TotalTokens, lastUsage.TotalTokens)
			if got := app.Terminal(string(last.ID)); got != terminal {
				t.Fatalf("last session uses terminal %s, want %s", got, terminal)
			}
		})
	}
}

func TestASwitchCommittedBeforeACrashReceivesItsOutcomeAfterRestart(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	initial := s.App()
	first := s.Spawn(initial, fakeagent.Claude, s.Path("shop"))
	agent := s.Launched(first)
	initial.TypeLine(first, "before the crash")
	agent.Prompted()
	agent.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(initial, first, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	s.Stop()
	s.StartCrashingAt("monitor-switch-applied")
	app := s.App()
	app.TypeLine(first, "/clear")
	agent.Prompted()
	s.AwaitCrash()
	s.Start()
	recovered := s.App()
	var next protocol.Session
	for _, session := range recovered.Initial.Sessions {
		if protocol.Deref(session.Succeeds) == protocol.SessionID(first) {
			next = session
		}
	}
	if next.ID == "" {
		next = awaitSuccessor(recovered, first)
	}
	recovered.TypeLine(string(next.ID), "continue after the crash")
	agent.Prompted()
	reply := "Recovered. <!-- attn:state=waiting_input -->"
	agent.Reply(reply)
	awaitUsageTokens(recovered, string(next.ID), claudeTokens(reply))
}

func TestAReloadDropsTheSwitchOfTheRuntimeItReplaced(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	deciding := s.PauseAt(pausepoint.ConversationSwitchDecision)
	s.Start()
	app := s.App()
	first := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	agent := s.Launched(first)
	app.TypeLine(first, "before reload")
	agent.Prompted()
	conversation := agent.ConversationID
	agent.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, first, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	app.TypeLine(first, "/clear")
	agent.Prompted()
	deciding.Await()
	reloadRespawned(t, app, first)
	relaunched := s.Launched(first)
	if relaunched.ConversationID != conversation {
		t.Fatalf("reload resumed %s, want %s", relaunched.ConversationID, conversation)
	}
	deciding.Release()
	s.Stop()
	s.Start()
	recovered := s.App()
	for _, session := range recovered.Initial.Sessions {
		if protocol.Deref(session.Succeeds) == protocol.SessionID(first) {
			t.Fatalf("reloaded runtime's switch replayed as %+v", session)
		}
	}
	recovered.TypeLine(first, "after reload and restart")
	relaunched.Prompted()
	if relaunched.ConversationID != conversation {
		t.Fatalf("restart changed the conversation to %s", relaunched.ConversationID)
	}
}

func TestARefusedSwitchLeavesTheTerminalOnItsSession(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	deciding := s.PauseAt(pausepoint.ConversationSwitchDecision)
	exited := s.PauseAt(pausepoint.PtyExitHandled)
	s.Start()
	app := s.App()
	first := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	agent := s.Launched(first)
	app.TypeLine(first, "before exit")
	agent.Prompted()
	agent.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, first, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	terminal := app.Terminal(first)
	app.TypeLine(first, "/clear")
	agent.Prompted()
	deciding.Await()
	agent.Exit(1)
	exited.Await()
	exited.Release()
	deciding.Release()
	testworld.Await(app, protocol.EventSessionExited, func(x protocol.SessionExitedMessage) bool { return string(x.SessionID) == first })
	s.Stop()
	s.Start()
	recovered := s.App()
	for _, session := range recovered.Initial.Sessions {
		if protocol.Deref(session.Succeeds) == protocol.SessionID(first) {
			t.Fatalf("refused switch replayed as %+v", session)
		}
	}
	if got := recovered.Terminal(first); got != terminal {
		t.Fatalf("refused switch moved the terminal to %s, want %s", got, terminal)
	}
}

func TestAnAppliedObservationIsNotReplayedOverALaterHomeBinding(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	first := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	agent := s.Launched(first)
	app.TypeLine(first, "before clear")
	agent.Prompted()
	original := agent.ConversationID
	agent.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, first, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	next := clearClaude(app, agent, first)
	resumeClaude(app, agent, string(next.ID), original)
	back := awaitSuccessor(app, string(next.ID))
	if string(back.ID) != first {
		t.Fatalf("resume reopened %s, want %s", back.ID, first)
	}
	s.Stop()
	s.Start()
	recovered := s.App()
	restartedSession(recovered, first)
	reloadRespawned(t, recovered, first)
	agent = s.Launched(first)
	recovered.TypeLine(first, "still the original conversation")
	agent.Prompted()
	if agent.ConversationID != original {
		t.Fatalf("agent runs %s, want %s", agent.ConversationID, original)
	}
}

func TestACandidatesTitleAndTurnSurviveItsOutcome(t *testing.T) {
	for _, agentKind := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(agentKind), func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(agentKind))
			s.Vars = append(s.Vars, "ATTN_HEADLESS_TASKS=on")
			outcome := s.PauseAt(pausepoint.ConversationSwitchOutcome)
			owed := s.PauseAt(pausepoint.ConversationCandidateTurn)
			s.Start()
			app := s.App()
			if agentKind == fakeagent.Codex {
				setSetting(t, app, "codex_shared_enabled", "true")
			}
			first := s.Spawn(app, agentKind, s.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.Label = protocol.Ptr("Original name") })
			agent := s.Launched(first)
			app.TypeLine(first, "first prompt")
			agent.Prompted()
			agent.Reply("Ready. <!-- attn:state=waiting_input -->")
			s.HeadlessTask().Answer(`{"verdict":"WAITING"}`)
			testworld.AwaitSession(app, first, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
			command := "/clear"
			if agentKind == fakeagent.Codex {
				command = "/new"
			}
			app.TypeLine(first, command)
			agent.Prompted()
			outcome.Await()
			next := awaitSuccessor(app, first)
			app.TypeLine(string(next.ID), "fix the cart total")
			agent.Prompted()
			title := s.HeadlessTask()
			var subject string
			for _, task := range listTasks(app) {
				if task.Kind == "session_title" {
					subject = task.Subject
				}
			}
			if subject == "" {
				t.Fatal("the candidate has no title task")
			}
			title.Answer("Fix cart total")
			awaitTitleTask(app, subject, func(x protocol.Task) bool { return x.State == "done" })
			agent.Reply("Which discount? <!-- attn:state=waiting_input -->")
			s.HeadlessTask().Answer(`{"verdict":"WAITING"}`)
			owed.Await()
			beforeOutcome := time.Now()
			owed.Release()
			outcome.Release()
			shown := testworld.AwaitSession(app, string(next.ID), func(x protocol.Session) bool { return x.Label == "Fix cart total" && protocol.Deref(x.TurnOwed) })
			opened := protocol.Timestamp(protocol.Deref(shown.TurnOpenedAt)).Time()
			if !opened.Before(beforeOutcome) {
				t.Fatalf("turn opened at %s, want before the outcome at %s", opened, beforeOutcome)
			}
			t.Logf("daemon candidate=%s label=%q state=%s turn_opened=%s outcome_after=%s", shown.ID, shown.Label, shown.State, opened, beforeOutcome)
			if shown.State != protocol.SessionStateWaitingInput {
				t.Fatalf("candidate after the outcome is %s, want waiting", shown.State)
			}
		})
	}
}

func TestACandidatesFailedTitleCanBeRetriedAfterRestart(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "ATTN_HEADLESS_TASKS=on")
	outcome := s.PauseAt(pausepoint.ConversationSwitchOutcome)
	s.Start()
	app := s.App()
	first := s.Spawn(app, fakeagent.Claude, s.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.Label = protocol.Ptr("Original name") })
	agent := s.Launched(first)
	app.TypeLine(first, "first prompt")
	agent.Prompted()
	app.TypeLine(first, "/clear")
	agent.Prompted()
	outcome.Await()
	next := awaitSuccessor(app, first)
	app.TypeLine(string(next.ID), "fix cart discounts")
	agent.Prompted()
	title := s.HeadlessTask()
	var subject string
	for _, task := range listTasks(app) {
		if task.Kind == "session_title" {
			subject = task.Subject
		}
	}
	if subject == "" {
		t.Fatal("the candidate has no title task")
	}
	title.Fail("provider unavailable")
	failed := awaitTitleTask(app, subject, func(x protocol.Task) bool { return x.State == "failed" })
	outcome.Release()
	bound := awaitTitleTask(app, string(next.ID), func(x protocol.Task) bool { return x.ID == failed.ID && x.State == "failed" })
	s.Stop()
	s.Start()
	recovered := s.App()
	requestID := "retry-candidate-title"
	result := testworld.Request(recovered, protocol.TaskRetryMessage{Cmd: protocol.CmdTaskRetry, TaskID: bound.ID, RequestID: &requestID}, protocol.EventTaskRetryResult, func(x protocol.TaskRetryResultMessage) bool { return x.RequestID == requestID })
	if !result.Success {
		t.Fatalf("retry failed: %+v", result)
	}
	s.HeadlessTask().Answer("Fix cart discounts")
	awaitLabel(recovered, string(next.ID), "Fix cart discounts")
}

func TestAResumedSharedConversationsRunSurvivesItsOutcome(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	outcome := s.PauseEvery(pausepoint.ConversationSwitchOutcome)
	s.Start()
	app := s.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	first := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	agent := s.Launched(first)
	app.TypeLine(first, "first conversation")
	agent.Prompted()
	original := agent.ConversationID
	agent.Reply("First question? <!-- attn:state=waiting_input -->")
	initial := testworld.AwaitSession(app, first, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	app.TypeLine(first, "/new")
	agent.Prompted()
	outcome.Await()
	next := awaitSuccessor(app, first)
	outcome.Release()
	resuming := len(app.Log())
	app.TypeLine(string(next.ID), "/resume "+original)
	agent.Prompted()
	outcome.Await()
	back := *testworld.AwaitSince(app, resuming, protocol.EventSessionRegistered, func(x protocol.WebSocketEvent) bool { return x.Session != nil && string(x.Session.ID) == first }).Session
	if string(back.ID) != first {
		t.Fatalf("resume shows %s, want %s", back.ID, first)
	}
	mark := len(app.Log())
	app.TypeLine(first, "continue the original conversation")
	agent.Prompted()
	agent.Reply("Second question? <!-- attn:state=waiting_input -->")
	current := testworld.AwaitSessionSince(app, mark, first, func(x protocol.Session) bool {
		return x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.LastModelRequestAt) != protocol.Deref(initial.LastModelRequestAt)
	})
	if !protocol.Deref(current.TurnOwed) {
		t.Fatal("the resumed conversation lost its turn before the outcome")
	}
	outcome.Release()
	s.Stop()
	s.Start()
	app = s.App()
	retained := queriedSession(t, s.Client(), first)
	if retained.State != protocol.SessionStateWaitingInput || !protocol.Deref(retained.TurnOwed) || protocol.Deref(retained.TurnOpenedAt) != protocol.Deref(current.TurnOpenedAt) {
		t.Fatalf("outcome changed the resumed turn: before=%+v after=%+v", current, retained)
	}
	if sent := submitSessionAnnotationFeedback(app, first, "answer the second question"); sent.status != "delivered" {
		t.Fatalf("input after resume: %+v", sent)
	}
	if got := s.CodexServer().Prompted(original); got != "answer the second question" {
		t.Fatalf("link delivered %q to %s", got, original)
	}
	t.Logf("daemon resumed=%s conversation=%s state=%s turn_owed=%v", current.ID, original, current.State, protocol.Deref(current.TurnOwed))
}

func TestReloadingOneSharedTerminalKeepsTheOthersPendingConversation(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	deciding := s.PauseEvery(pausepoint.ConversationSwitchDecision)
	s.Start()
	app := s.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	first := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	firstAgent := s.Launched(first)
	app.TypeLine(first, "first conversation")
	firstAgent.Prompted()
	firstAgent.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, first, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	app.TypeLine(first, "/new")
	firstAgent.Prompted()
	resumeFirst := deciding.Take()
	pending := firstAgent.ConversationID
	second := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	secondAgent := s.Launched(second)
	app.TypeLine(second, "second conversation")
	secondAgent.Prompted()
	secondAgent.Reply("Ready too. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, second, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	app.TypeLine(second, "/resume "+pending)
	secondAgent.Prompted()
	resumeSecond := deciding.Take()
	reloadRespawned(t, app, first)
	s.Launched(first)
	app.TypeLine(second, "continue in the second terminal")
	if got := secondAgent.Prompted(); got != "continue in the second terminal" {
		t.Fatalf("second terminal took %q", got)
	}
	secondAgent.Reply("Second terminal works. <!-- attn:state=waiting_input -->")
	resumeSecond()
	successor := awaitSuccessor(app, second)
	testworld.AwaitSession(app, string(successor.ID), func(x protocol.Session) bool {
		return x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.TurnOwed)
	})
	resumeFirst()
	t.Logf("daemon second_terminal_session=%s bound=%s conversation=%s", second, successor.ID, pending)
}

func TestAnEndedRuntimeCannotReplayItsPendingSwitchOverItsReplacement(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	first := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	agent := s.Launched(first)
	app.TypeLine(first, "before the crash")
	agent.Prompted()
	agent.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, first, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	s.Stop()
	s.StartCrashingAt("monitor-switch-applied")
	app = s.App()
	app.TypeLine(first, "/clear")
	agent.Prompted()
	s.AwaitCrash()
	agent.Exit(1)
	s.Start()
	app = s.App()
	second, ok := successorIn(app.Initial.Sessions, first)
	if !ok {
		second = awaitSuccessor(app, first)
	}
	reloadRespawned(t, app, string(second.ID))
	replacement := s.Launched(string(second.ID))
	last := clearClaude(app, replacement, string(second.ID))
	awaitClosed(app, string(second.ID))
	s.Stop()
	s.Start()
	restartedSession(s.App(), string(last.ID))
}

func TestInputFindsItsConversationWhenBindingFinishesDuringLookup(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "ATTN_HEADLESS_TASKS=on")
	outcome := s.PauseAt(pausepoint.ConversationSwitchOutcome)
	lookup := s.PauseAt(pausepoint.ConversationInputLookup)
	bound := s.PauseAt(pausepoint.ConversationCandidateBound)
	s.Start()
	app := s.App()
	first := s.Spawn(app, fakeagent.Claude, s.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("Original")
	})
	agent := s.Launched(first)
	app.TypeLine(first, "/clear")
	agent.Prompted()
	outcome.Await()
	next := awaitSuccessor(app, first)
	app.TypeLine(string(next.ID), "fix cart discounts")
	lookup.Await()
	outcome.Release()
	bound.Await()
	bound.Release()
	lookup.Release()
	if got := agent.Prompted(); got != "fix cart discounts" {
		t.Fatalf("input after binding = %q", got)
	}
	title := s.HeadlessTask()
	title.Answer("Fix cart discounts")
	awaitLabel(app, string(next.ID), "Fix cart discounts")
}
