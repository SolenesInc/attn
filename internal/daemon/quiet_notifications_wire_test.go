package daemon_test

import (
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/testworld"
)

func sessionMailbox(session string) protocol.QuickCaptureMailbox {
	return protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID(session))}
}

func quietNotificationsWorld(t *testing.T, quiet bool) (*world, *testworld.Peer, *client.Client) {
	t.Helper()
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "queue_mode_enabled", "true")
	if !quiet {
		setSetting(t, app, "queue_quiet_notifications", "false")
	}
	registerSessions(t, w, cli, "reviewer")
	return w, app, cli
}

func userRunStops(app *testworld.Peer, agent *fakeagent.Run, before protocol.Session, prompt, reply string) protocol.Session {
	app.T.Helper()
	session := string(before.ID)
	app.TypeLine(session, prompt)
	agent.Prompted()
	working := testworld.AwaitStateAfter(app, before, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	agent.Reply(reply)
	return testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
}

func notificationRunStarts(t *testing.T, app *testworld.Peer, cli *client.Client, agent *fakeagent.Run, session string, before protocol.Session, body string) (protocol.Session, *protocol.AgentInboxBatchResult) {
	t.Helper()
	sendAgentMessage(t, cli, "reviewer", session, body)
	if got := agent.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("the agent was prompted with %q, want the inbox doorbell", got)
	}
	working := testworld.AwaitStateAfter(app, before, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	return working, readInbox(t, cli, session, 0)
}

func settleFromApp(app *testworld.Peer, owed protocol.Session) protocol.Session {
	app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: owed.ID})
	return testworld.AwaitSession(app, string(owed.ID), func(s protocol.Session) bool {
		return s.StateSince == owed.StateSince && !protocol.Deref(s.TurnOwed)
	})
}

func owingIdleAgent(t *testing.T, w *world, app *testworld.Peer, cli *client.Client) (string, *fakeagent.Run, protocol.Session) {
	t.Helper()
	session, agent := mailIdleAgent(w, app, "shop")
	owed := queriedSession(t, cli, session)
	if !protocol.Deref(owed.TurnOwed) {
		t.Fatalf("the user's first run stopped without opening a turn: %+v", owed)
	}
	return session, agent, owed
}

func TestAnAgentANotificationWokeStaysOutOfTheQueueWhenItStops(t *testing.T) {
	w, app, cli := quietNotificationsWorld(t, true)
	session, agent, owed := owingIdleAgent(t, w, app, cli)
	settled := settleFromApp(app, owed)

	for i, reply := range []string{
		"Nothing for this plot. <!-- attn:state=idle -->",
		"The reviewer is done. Should I merge? <!-- attn:state=waiting_input -->",
	} {
		working, batch := notificationRunStarts(t, app, cli, agent, session, settled, fmt.Sprintf("review round %d is in", i+1))
		if !protocol.Deref(batch.QuietNotifications) {
			t.Errorf("reading its inbox in a notification run, the agent was not told it stays out of the queue: %+v", batch)
		}
		agent.Reply(reply)
		settled = testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
		if protocol.Deref(settled.TurnOwed) {
			t.Fatalf("after %q ended a notification run the agent owes the user a turn", reply)
		}
	}

	stopped := userRunStops(app, agent, settled, "what did the reviewer say?", "They approved. <!-- attn:state=idle -->")
	if !protocol.Deref(stopped.TurnOwed) || protocol.Deref(stopped.TurnByAgent) {
		t.Fatalf("a run the user started stopped with owed=%v by_agent=%v, want the turn open as always",
			protocol.Deref(stopped.TurnOwed), protocol.Deref(stopped.TurnByAgent))
	}
}

func TestANotificationThatSetsAWaitingAgentWorkingSettlesItsTurn(t *testing.T) {
	w, app, cli := quietNotificationsWorld(t, true)
	session, agent, owed := owingIdleAgent(t, w, app, cli)
	settled := settleFromApp(app, owed)
	if got := submitSessionAnnotationFeedback(app, session, "open the PR"); got.status != "delivered" {
		t.Fatalf("the user's annotation = %+v, want delivered", got)
	}
	agent.Prompted()
	working := testworld.AwaitStateAfter(app, settled, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	agent.Reply("PR is open, CI is running. <!-- attn:state=waiting_input -->")
	waiting := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
	if !protocol.Deref(waiting.TurnOwed) {
		t.Fatal("the user's run stopped without opening a turn")
	}

	working, _ = notificationRunStarts(t, app, cli, agent, session, waiting, "CI passed")
	if protocol.Deref(working.TurnOwed) || !protocol.Deref(working.TurnByAgent) {
		t.Fatalf("once the notification set the agent working its turn shows owed=%v by_agent=%v, want settled by the agent",
			protocol.Deref(working.TurnOwed), protocol.Deref(working.TurnByAgent))
	}
	agent.Reply("CI is green. <!-- attn:state=idle -->")
	if stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle }); protocol.Deref(stopped.TurnOwed) {
		t.Fatal("the notification run's stop reopened the turn")
	}
}

func TestAnAgentOpensItsOwnTurnAndOnlyItOrTheUserSettlesIt(t *testing.T) {
	w, app, cli := quietNotificationsWorld(t, true)
	session, agent, owed := owingIdleAgent(t, w, app, cli)
	settled := settleFromApp(app, owed)

	working, _ := notificationRunStarts(t, app, cli, agent, session, settled, "the migration failed")
	opened, err := cli.AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen)
	if err != nil || opened.Outcome != protocol.AgentTurnOutcomeOpensAtStop {
		t.Fatalf("attn turn open while working = %+v, %v; want it to open at the stop", opened, err)
	}
	agent.Reply("The migration needs your call. <!-- attn:state=idle -->")
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(stopped.TurnOwed) || !protocol.Deref(stopped.TurnByAgent) {
		t.Fatalf("after attn turn open the stop shows owed=%v by_agent=%v, want the agent's turn open",
			protocol.Deref(stopped.TurnOwed), protocol.Deref(stopped.TurnByAgent))
	}

	working, _ = notificationRunStarts(t, app, cli, agent, session, stopped, "never mind the reviewer")
	if !protocol.Deref(working.TurnOwed) {
		t.Fatal("a notification settled the turn the agent opened")
	}
	agent.Reply("Noted. <!-- attn:state=idle -->")
	stopped = testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(stopped.TurnOwed) {
		t.Fatal("the agent's turn closed when its next notification run stopped")
	}

	closed, err := cli.AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionSettle)
	if err != nil || closed.Outcome != protocol.AgentTurnOutcomeSettled {
		t.Fatalf("attn turn settle = %+v, %v; want settled", closed, err)
	}
	stopped = testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.StateSince == stopped.StateSince && !protocol.Deref(s.TurnOwed)
	})
	if !protocol.Deref(stopped.TurnByAgent) {
		t.Error("the agent's own settle is not marked as the agent's")
	}

	asked := userRunStops(app, agent, stopped, "anything else?", "Should I also bump the version? <!-- attn:state=waiting_input -->")
	if _, err := cli.AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionSettle); err == nil || !strings.Contains(err.Error(), "only the user settles it") {
		t.Fatalf("attn turn settle on a turn the user's run opened = %v, want a refusal naming why", err)
	}
	if !protocol.Deref(asked.TurnOwed) || !protocol.Deref(queriedSession(t, cli, session).TurnOwed) {
		t.Fatal("a refused settle closed the turn")
	}
}

func TestANotificationRunThatNeedsApprovalStillOpensATurn(t *testing.T) {
	w, app, cli := quietNotificationsWorld(t, true)
	setSetting(t, app, "auto_approve_enabled", "false")
	session, agent, owed := owingIdleAgent(t, w, app, cli)
	settled := settleFromApp(app, owed)

	working, _ := notificationRunStarts(t, app, cli, agent, session, settled, "rebase onto main")
	agent.AskApproval()
	if asking := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval }); !protocol.Deref(asking.TurnOwed) {
		t.Fatal("a notification run waiting on an approval does not owe the user a turn")
	}
}

func TestANotificationRunThatDiesOnAnErrorOpensATurn(t *testing.T) {
	w, app, cli := quietNotificationsWorld(t, true)
	session, agent, owed := owingIdleAgent(t, w, app, cli)

	waking := owed
	for i := range 2 {
		working, _ := notificationRunStarts(t, app, cli, agent, session, waking, fmt.Sprintf("review round %d is in", i+1))
		if protocol.Deref(working.TurnOwed) {
			t.Fatal("the ring's run did not settle the agent's open turn")
		}
		agent.ProgramStatus("state=error:app=claude-code")
		died := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
		if !protocol.Deref(died.TurnOwed) {
			t.Fatalf("round %d: the agent died before it could open its turn, and the user was not told: %+v", i+1, died)
		}
		if protocol.Deref(died.TurnByAgent) {
			t.Fatalf("round %d: a failed run's turn counts as the agent's, so only the agent could settle it: %+v", i+1, died)
		}
		waking = settleFromApp(app, died)
	}
}

func TestWithoutQuietNotificationsOrTheQueueANotificationRunOpensATurnAsBefore(t *testing.T) {
	for _, tc := range []struct {
		name  string
		quiet bool
		queue string
	}{
		{"quiet notifications off", false, "true"},
		{"queue flow off", true, "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, app, cli := quietNotificationsWorld(t, tc.quiet)
			setSetting(t, app, "queue_mode_enabled", tc.queue)
			session, agent, owed := owingIdleAgent(t, w, app, cli)
			settled := settleFromApp(app, owed)

			working, batch := notificationRunStarts(t, app, cli, agent, session, settled, "the review is in")
			if protocol.Deref(batch.QuietNotifications) {
				t.Error("the agent was told it stays out of the queue")
			}
			agent.Reply("Nothing for this plot. <!-- attn:state=idle -->")
			if stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle }); !protocol.Deref(stopped.TurnOwed) {
				t.Fatal("the notification run's stop did not open a turn")
			}
		})
	}
}

func TestAPairedSendersQuestionPutsTheAgentsReplyInTheQueue(t *testing.T) {
	w, _, cli := quietNotificationsWorld(t, true)
	app := w.TrustedApp()
	session, agent, owed := owingIdleAgent(t, w, app, cli)
	settled := settleFromApp(app, owed)
	paired := pairSenderInApp(t, cli, app, "Capture")
	profileID := app.SelectedProfile()

	if _, err := cli.QuickCapture(protocol.QuickCaptureSendMessage{
		Cmd: protocol.CmdQuickCaptureSend, ProfileID: &profileID, SenderToken: &paired.Token, CaptureID: uuid.NewString(), Content: "What is the review status?", AttachmentIds: []string{},
		Mailbox: sessionMailbox(session),
	}); err != nil {
		t.Fatal(err)
	}
	if got := agent.Prompted(); !strings.Contains(got, "What is the review status?") {
		t.Fatalf("the agent was prompted with %q, want the user's question", got)
	}
	working := testworld.AwaitStateAfter(app, settled, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	agent.Reply("The review is green. <!-- attn:state=idle -->")
	answered := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(answered.TurnOwed) || protocol.Deref(answered.TurnByAgent) {
		t.Fatalf("the reply to the user's question stopped with owed=%v by_agent=%v, want the user's turn open",
			protocol.Deref(answered.TurnOwed), protocol.Deref(answered.TurnByAgent))
	}

	settled = settleFromApp(app, answered)
	working, _ = notificationRunStarts(t, app, cli, agent, session, settled, "the review is in")
	agent.Reply("Nothing for this plot. <!-- attn:state=idle -->")
	if stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle }); protocol.Deref(stopped.TurnOwed) {
		t.Fatal("the notification run after the user's question opened a turn")
	}
}

func TestAQuickCaptureThatSteersANotificationRunOpensItsTurnAtTheStop(t *testing.T) {
	w, _, cli := quietNotificationsWorld(t, true)
	app := w.TrustedApp()
	session, agent, owed := owingIdleAgent(t, w, app, cli)
	settled := settleFromApp(app, owed)

	working, _ := notificationRunStarts(t, app, cli, agent, session, settled, "the review is in")
	sendQuickCapture(t, app, sessionMailbox(session), "what did the reviewer say?")
	if got := agent.Prompted(); !strings.Contains(got, "what did the reviewer say?") {
		t.Fatalf("the agent was prompted with %q, want the user's question", got)
	}
	agent.Reply("They approved. <!-- attn:state=idle -->")
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(stopped.TurnOwed) || protocol.Deref(stopped.TurnByAgent) {
		t.Fatalf("the steered run stopped with owed=%v by_agent=%v, want the user's turn open",
			protocol.Deref(stopped.TurnOwed), protocol.Deref(stopped.TurnByAgent))
	}
}

func TestAQuickCaptureRungWithOtherItemsOpensItsTurn(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.TrustedApp(), w.Client()
		setSetting(t, app, "queue_mode_enabled", "true")
		registerSessions(t, w, cli, "peer")
		agent := w.bubbleClaude(t, app, "shop")

		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(agent.self), Data: "half a thought"})
		synctest.Wait()
		sendAgentMessage(t, cli, "peer", agent.id, "the review is in")
		sendQuickCapture(t, app, sessionMailbox(agent.id), "what did the reviewer say?")
		w.advance(sessionmonitor.QuietWindow)

		pasted := agent.term.Pasted()
		if len(pasted) != 1 || !strings.Contains(pasted[0], "what did the reviewer say?") || !strings.Contains(pasted[0], inboxDoorbell) {
			t.Fatalf("once the user was quiet attn pasted %q, want one ring with the capture and the doorbell", pasted)
		}
		agent.reply("They approved. <!-- attn:state=idle -->")
		if stopped := queriedSession(t, cli, agent.id); !protocol.Deref(stopped.TurnOwed) {
			t.Fatalf("the ring's run stopped %s without opening the user's turn", stopped.State)
		}
	})
}

func TestWithTheQueueFlowOffAnAgentOpensAndSettlesItsOwnTurn(t *testing.T) {
	w, app, cli := quietNotificationsWorld(t, true)
	setSetting(t, app, "queue_mode_enabled", "false")
	session, _, owed := owingIdleAgent(t, w, app, cli)
	settleFromApp(app, owed)

	if opened, err := cli.AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen); err != nil || opened.Outcome != protocol.AgentTurnOutcomeOpened {
		t.Fatalf("attn turn open with the queue flow off = %+v, %v; want opened", opened, err)
	}
	if s := queriedSession(t, cli, session); !protocol.Deref(s.TurnOwed) || !protocol.Deref(s.TurnByAgent) {
		t.Fatalf("attn turn open left owed=%v by_agent=%v", protocol.Deref(s.TurnOwed), protocol.Deref(s.TurnByAgent))
	}
	if closed, err := cli.AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionSettle); err != nil || closed.Outcome != protocol.AgentTurnOutcomeSettled {
		t.Fatalf("attn turn settle with the queue flow off = %+v, %v; want settled", closed, err)
	}
	if s := queriedSession(t, cli, session); protocol.Deref(s.TurnOwed) {
		t.Fatal("attn turn settle left the turn open")
	}
}

func TestAnAgentsOwnTurnOutlivesARestart(t *testing.T) {
	w, app, cli := quietNotificationsWorld(t, true)
	session, _, owed := owingIdleAgent(t, w, app, cli)
	settleFromApp(app, owed)
	if opened, err := cli.AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen); err != nil || opened.Outcome != protocol.AgentTurnOutcomeOpened {
		t.Fatalf("attn turn open while stopped = %+v, %v; want opened", opened, err)
	}
	if s := queriedSession(t, cli, session); !protocol.Deref(s.TurnOwed) || !protocol.Deref(s.TurnByAgent) {
		t.Fatalf("attn turn open left owed=%v by_agent=%v", protocol.Deref(s.TurnOwed), protocol.Deref(s.TurnByAgent))
	}

	w.restart()
	app = w.App()
	w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ID = protocol.SessionID(session)
		m.ResumeSessionID = protocol.Ptr(session)
	})
	resumed := w.Launched(session)
	restored := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(restored.TurnOwed) || !protocol.Deref(restored.TurnByAgent) {
		t.Fatalf("after a restart the agent's turn shows owed=%v by_agent=%v, want it still the agent's",
			protocol.Deref(restored.TurnOwed), protocol.Deref(restored.TurnByAgent))
	}
	registerSessions(t, w, w.Client(), "reviewer")
	if working, _ := notificationRunStarts(t, app, w.Client(), resumed, session, restored, "the review is in"); !protocol.Deref(working.TurnOwed) {
		t.Fatal("after a restart a notification settled the turn the agent opened")
	}
}

func TestAnAgentsRequestForItsTurnWaitsOutTheUsersSnooze(t *testing.T) {
	w, app, cli := quietNotificationsWorld(t, true)
	session, agent, owed := owingIdleAgent(t, w, app, cli)
	snoozeUntil(app, session, time.Now().Add(time.Hour))
	snoozed := testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.StateSince == owed.StateSince && s.TurnSnoozedUntil != nil
	})

	working, _ := notificationRunStarts(t, app, cli, agent, session, snoozed, "the migration failed")
	if asked, err := cli.AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen); err != nil || asked.Outcome != protocol.AgentTurnOutcomeSnoozed {
		t.Fatalf("attn turn open while snoozed = %+v, %v; want snoozed", asked, err)
	}
	app.Send(protocol.WakeTurnMessage{Cmd: protocol.CmdWakeTurn, SessionID: protocol.SessionID(session)})
	awake := testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.StateSince == working.StateSince && s.TurnSnoozedUntil == nil
	})
	if protocol.Deref(awake.TurnOwed) {
		t.Fatal("the snooze ended into an owed turn while the agent still worked")
	}
	agent.Reply("The migration needs your call. <!-- attn:state=idle -->")
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(stopped.TurnOwed) || !protocol.Deref(stopped.TurnByAgent) {
		t.Fatalf("the agent asked for its turn during the snooze and stopped after it with owed=%v by_agent=%v, want its turn open",
			protocol.Deref(stopped.TurnOwed), protocol.Deref(stopped.TurnByAgent))
	}
}

func TestAnAgentsRequestForItsTurnOutlivesARestartBeforeItStops(t *testing.T) {
	w, app, cli := quietNotificationsWorld(t, true)
	session, agent, owed := owingIdleAgent(t, w, app, cli)
	settled := settleFromApp(app, owed)
	notificationRunStarts(t, app, cli, agent, session, settled, "the migration failed")
	if asked, err := cli.AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen); err != nil || asked.Outcome != protocol.AgentTurnOutcomeOpensAtStop {
		t.Fatalf("attn turn open while working = %+v, %v; want it to open at the stop", asked, err)
	}

	w.restart()
	app = w.App()
	w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ID = protocol.SessionID(session)
		m.ResumeSessionID = protocol.Ptr(session)
	})
	w.Launched(session)
	stopped := testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateIdle && protocol.Deref(s.TurnOwed)
	})
	if !protocol.Deref(stopped.TurnByAgent) {
		t.Fatal("after a restart the agent's stop opened a turn that is not the agent's")
	}
}
