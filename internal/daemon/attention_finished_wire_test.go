package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	finishedAnswer       = `{"verdict":"FINISHED","label":"finished","reason":"the refund flow is planned and nothing is left for anyone"}`
	finishedNoticeHeader = "attn supervisor · finished check"
)

func markedFinished(s protocol.Session) bool {
	return s.Attention != nil && s.Attention.Verdict == "finished"
}

func closedBySupervisor(t *testing.T, entry protocol.SessionLedgerEntry, reason string) {
	t.Helper()
	if by, why := protocol.Deref(entry.ClosedBy), protocol.Deref(entry.CloseReason); by != "supervisor" || why != reason {
		t.Errorf("the ledger reads closed by %q because %q, want closed by supervisor because %q", by, why, reason)
	}
}

func reopensUnmarked(t *testing.T, w *world, app *testworld.Peer, session string) {
	t.Helper()
	if reopened := reopenOverTheWebSocket(app, session); !reopened.Success {
		t.Fatalf("Reopen of the session the supervisor closed: %+v", reopened)
	}
	w.Launched(session)
	if back := queriedSession(t, w.Client(), session); back.Attention != nil {
		t.Errorf("the reopened session still carries %+v", back.Attention)
	}
}

func markedAfterAnsweringItsFinishedCheck(t *testing.T) (*world, *testworld.Peer, protocol.Session, *fakeagent.Run) {
	t.Helper()
	w, app, _ := supervisorActsWorld(t, func(task *fakeagent.HeadlessTask) { task.Answer(finishedAnswer) })
	cli := w.Client()
	session, agent, settled := settledAgent(t, w, app)
	working, _ := notificationRunStarts(t, app, cli, agent, session, settled, "the review is in")
	runEnds(w, app, agent, working, "The review found nothing to fix.")
	w.awaitTimer(daemon.AttentionStopTimer)
	w.advance(attentionStopWait)

	if got := agent.DeliveredPrompt(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("after the finished verdict the agent was prompted with %q, want the inbox doorbell", got)
	}
	answering := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	notices := supervisorNotices(t, cli, session)
	if len(notices) != 1 || !strings.Contains(notices[0], finishedNoticeHeader) || !strings.Contains(notices[0], "nothing is left for anyone") {
		t.Fatalf("the finished agent got the notices %q, want one finished check with the verdict's reason", notices)
	}
	if answering.Attention != nil {
		t.Errorf("the agent carries %+v before it answered the finished check", answering.Attention)
	}
	runEnds(w, app, agent, answering, "Nothing is left here; the plan is with the user.")
	w.awaitTimer(daemon.AttentionStopTimer)
	w.advance(attentionStopWait)

	marked := testworld.AwaitSession(app, session, markedFinished)
	if protocol.Deref(marked.TurnOwed) || marked.Attention.Decider != "headless/claude-haiku-fake" || protocol.Deref(marked.Attention.Label) != "finished" {
		t.Errorf("the second finished verdict left %+v owed=%v, want the finished mark and no turn", marked.Attention, protocol.Deref(marked.TurnOwed))
	}
	if again := supervisorNotices(t, cli, session); len(again) != 0 {
		t.Errorf("the second finished verdict sent the notices %q, want the mark alone", again)
	}
	return w, app, marked, agent
}

func TestTheUsersSettleClosesAnAgentTheSupervisorMarkedFinished(t *testing.T) {
	w, app, marked, _ := markedAfterAnsweringItsFinishedCheck(t)
	session := string(marked.ID)
	w.advance(4 * time.Hour)
	if still := queriedSession(t, w.Client(), session); !markedFinished(still) {
		t.Fatalf("4 hours after the mark the agent carries %+v, want it open and still marked", still.Attention)
	}
	app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: protocol.SessionID(session)})
	closedBySupervisor(t, awaitClosed(app, session), "settled when finished")
	reopensUnmarked(t, w, app, session)
}

func TestAFinishedMarkEndsWhenTheUserPromptsTheAgentAgain(t *testing.T) {
	w, app, marked, agent := markedAfterAnsweringItsFinishedCheck(t)
	session := string(marked.ID)
	app.TypeLine(session, "now add refunds by card")
	agent.Prompted()
	working := testworld.AwaitStateAfter(app, marked, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	if working.Attention != nil {
		t.Errorf("the agent the user prompted works on with %+v", working.Attention)
	}
	owed := runEnds(w, app, agent, working, "Added refunds by card.")
	if owed.Attention != nil || !protocol.Deref(owed.TurnOwed) {
		t.Fatalf("the user's run stopped as %+v owed=%v, want its turn open and no finished mark", owed.Attention, protocol.Deref(owed.TurnOwed))
	}
	settleFromApp(app, owed)
	if closedAt := protocol.Deref(showSession(t, w.Client(), session).ClosedAt); closedAt != "" {
		t.Errorf("settling the agent after the user prompted it closed it at %s", closedAt)
	}
}

func TestSettlingAnAgentTheSupervisorDidNotMarkFinishedOnlySettlesIt(t *testing.T) {
	t.Run("an open verdict", func(t *testing.T) {
		w, app, _ := supervisorActsWorld(t, func(task *fakeagent.HeadlessTask) { task.Answer(openAnswer) })
		session, _ := stopsQuietlyAndWaitsForItsVerdict(t, w, app)
		owed := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) && s.Attention != nil })
		settleFromApp(app, owed)
		if closedAt := protocol.Deref(showSession(t, w.Client(), session).ClosedAt); closedAt != "" {
			t.Errorf("settling the turn an open verdict opened closed the agent at %s", closedAt)
		}
	})
	t.Run("a quiet verdict", func(t *testing.T) {
		w, app, _ := supervisorActsWorld(t, func(task *fakeagent.HeadlessTask) {
			task.Answer(`{"verdict":"QUIET","reason":"the reviewer will report back"}`)
		})
		session, agent, settled := settledAgent(t, w, app)
		answering, _ := notificationRunStarts(t, app, w.Client(), agent, session, settled, "the review is in")
		stopped := runEnds(w, app, agent, answering, "The review found nothing to fix.")
		w.awaitTimer(daemon.AttentionStopTimer)
		w.advance(attentionStopWait)
		if got := explainedVerdict(t, w, session); !strings.HasPrefix(got, "quiet") {
			t.Fatalf("state explain prints the verdict %q, want quiet", got)
		}
		app.TypeLine(session, "now add refunds by card")
		agent.Prompted()
		working := testworld.AwaitStateAfter(app, stopped, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		owed := runEnds(w, app, agent, working, "Added refunds by card.")
		if !protocol.Deref(owed.TurnOwed) {
			t.Fatalf("the user's run stopped without opening a turn: %+v", owed)
		}
		settleFromApp(app, owed)
		if closedAt := protocol.Deref(showSession(t, w.Client(), session).ClosedAt); closedAt != "" {
			t.Errorf("settling an agent the supervisor called quiet closed it at %s", closedAt)
		}
	})
}

func TestTheSupervisorClosesADelegateWhoseStakeIsDoneAndWhoseDispatcherWatchesIt(t *testing.T) {
	for _, verb := range []string{"harvest", "wither"} {
		t.Run(verb, func(t *testing.T) {
			d := delegateStopsQuietly(t, true, "", `{"verdict":"QUIET","reason":"the dispatcher has the report"}`)
			moveDelegationSeed(t, d.cli, d.delegate, d.stake, verb, "migrated the first table")
			d.judged()
			closed := awaitClosed(d.app, d.delegate)
			state := map[string]string{"harvest": "harvested", "wither": "withered"}[verb]
			closedBySupervisor(t, closed, "its stake "+d.stake+" is "+state)
			reopensUnmarked(t, d.w, d.app, d.delegate)
		})
	}
}

func TestWithTheSupervisorOffADoneDelegateIsLoggedAndNotClosed(t *testing.T) {
	d := delegateStopsQuietly(t, false, "", `{"verdict":"QUIET","reason":"the dispatcher has the report"}`)
	moveDelegationSeed(t, d.cli, d.delegate, d.stake, "harvest", "migrated the first table")
	d.judged()
	if closedAt := protocol.Deref(showSession(t, d.cli, d.delegate).ClosedAt); closedAt != "" {
		t.Errorf("with the supervisor off the done delegate closed at %s", closedAt)
	}
	if got := explainedVerdict(t, d.w, d.delegate); !strings.Contains(got, "rule:delegate_finished") {
		t.Errorf("state explain prints the verdict %q, want the delegate_finished rule's", got)
	}
}

func TestAModelsFinishedVerdictOnADelegateMarksItWhenItsDispatcherStaysSilent(t *testing.T) {
	d := delegateStopsQuietly(t, true, "", `{"verdict":"FINISHED","label":"finished","reason":"the migration is reported and nothing is left"}`)
	d.judged()
	if notices := supervisorNotices(t, d.cli, "caller"); len(notices) != 1 || !strings.Contains(notices[0], "is finished, with nothing left for it to do. Its reason: the migration is reported") {
		t.Fatalf("the dispatcher got the notices %q, want one saying its delegate is finished", notices)
	}
	if notices := supervisorNotices(t, d.cli, d.delegate); len(notices) != 0 {
		t.Errorf("the delegate got the notices %q, want its dispatcher asked instead", notices)
	}
	d.w.awaitTimer(daemon.AttentionNoticeTimer)
	d.w.advance(noticeAnswerTime)
	marked := testworld.AwaitSession(d.app, d.delegate, markedFinished)
	if protocol.Deref(marked.TurnOwed) {
		t.Errorf("the silent dispatcher's finished delegate owes a turn: %+v", marked)
	}
	d.w.advance(4 * time.Hour)
	if closedAt := protocol.Deref(showSession(t, d.cli, d.delegate).ClosedAt); closedAt != "" {
		t.Errorf("a model's finished verdict closed the delegate at %s", closedAt)
	}
}

func TestAPendingFinishedCheckAndAFinishedMarkSurviveARestart(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
			switch {
			case strings.Contains(task.Prompt, supervisorQuestion):
				task.Answer(finishedAnswer)
			case strings.Contains(task.Prompt, "Classify how this assistant message ends"):
				task.Answer(`{"verdict":"DONE"}`)
			default:
				task.Answer("Refund flow")
			}
		})
		app, cli := w.TrustedApp(), w.Client()
		setSetting(t, app, "queue_mode_enabled", "true")
		setSetting(t, app, "attention_supervisor", "true")
		registerSessions(t, w, cli, "peer")
		claude := w.bubbleClaude(t, app, "shop")
		if booted := queriedSession(t, cli, claude.id); protocol.Deref(booted.TurnOwed) {
			settleFromApp(app, booted)
		}
		sendAgentMessage(t, cli, "peer", claude.id, "the review is in")
		w.advance(sessionmonitor.QuietWindow)
		claude.take(claude.term.Pasted()[0])
		readInbox(t, cli, claude.id, 0)
		claude.reply("The review found nothing to fix. <!-- attn:state=idle -->")
		claude.term.OnSubmit(func(string) {})
		w.advance(attentionStopWait)
		if notices := supervisorNotices(t, cli, claude.id); len(notices) != 1 || !strings.Contains(notices[0], finishedNoticeHeader) {
			t.Fatalf("the finished agent got the notices %q, want one finished check", notices)
		}

		w.advance(noticeAnswerTime / 2)
		w.restart()
		claude.cli, app = w.Client(), w.TrustedApp()
		if early := queriedSession(t, claude.cli, claude.id); early.Attention != nil {
			t.Fatalf("right after the restart the agent carries %+v, want its finished check still unanswered", early.Attention)
		}
		w.advance(noticeAnswerTime)
		marked := testworld.AwaitSession(app, claude.id, markedFinished)
		if protocol.Deref(marked.TurnOwed) {
			t.Errorf("the unanswered finished check opened a turn: %+v", marked)
		}

		w.restart()
		claude.cli, app = w.Client(), w.TrustedApp()
		testworld.AwaitSessionFromConnect(app, claude.id, markedFinished)
		app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: protocol.SessionID(claude.id)})
		closedBySupervisor(t, awaitClosed(app, claude.id), "settled when finished")
	})
}

func TestTheSupervisorLeavesADelegateThatIsStillWorkingOpenThoughItsStakeIsDone(t *testing.T) {
	inBubbleWithAgentsAndVerdicts(t, func(t *testing.T, w *world) {
		d := delegateStopsQuietlyInBubble(t, w)
		d.delegate.take("one more pass over the orders table")
		d.delegate.term.ProgramStatus(pty.ProgramWorking)
		moveDelegationSeed(t, d.cli, d.delegate.id, d.stake, "harvest", "migrated the first table")
		w.advance(20 * time.Minute)
		logged := elapsedCheckpoints(t, w, d.delegate.id)
		if len(logged) != 1 || logged[0].Decider != "rule:delegate_finished" || !strings.Contains(string(logged[0].Facts), `"state":"working"`) {
			t.Fatalf("20 minutes into its run the decision log holds %+v, want one elapsed checkpoint of the working delegate decided by rule:delegate_finished", logged)
		}
		if closedAt := protocol.Deref(showSession(t, d.cli, d.delegate.id).ClosedAt); closedAt != "" {
			t.Errorf("the supervisor closed the working delegate at %s", closedAt)
		}
	})
}
