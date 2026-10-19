package daemon_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func quietNotificationsWorld(t *testing.T, quiet bool) (*world, *testworld.Peer, *client.Client) {
	t.Helper()
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "queue_mode_enabled", "true")
	if quiet {
		setSetting(t, app, "queue_quiet_notifications", "true")
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
	waiting := userRunStops(app, agent, settleFromApp(app, owed), "open the PR", "PR is open, CI is running. <!-- attn:state=waiting_input -->")
	if !protocol.Deref(waiting.TurnOwed) {
		t.Fatal("the user's run stopped without opening a turn")
	}

	working, _ := notificationRunStarts(t, app, cli, agent, session, waiting, "CI passed")
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

func TestWithTheQueueFlowOffAnAgentsTurnCommandsChangeNothing(t *testing.T) {
	w, app, cli := quietNotificationsWorld(t, true)
	setSetting(t, app, "queue_mode_enabled", "false")
	session, _, owed := owingIdleAgent(t, w, app, cli)
	settleFromApp(app, owed)

	for _, action := range []protocol.AgentTurnAction{protocol.AgentTurnActionOpen, protocol.AgentTurnActionSettle} {
		result, err := cli.AgentTurn(protocol.SessionID(session), action)
		if err != nil || result.Outcome != protocol.AgentTurnOutcomeQueueOff {
			t.Fatalf("attn turn %s with the queue flow off = %+v, %v; want queue_off", action, result, err)
		}
	}
	if s := queriedSession(t, cli, session); protocol.Deref(s.TurnOwed) || protocol.Deref(s.TurnByAgent) {
		t.Fatalf("with the queue flow off attn turn open left owed=%v by_agent=%v", protocol.Deref(s.TurnOwed), protocol.Deref(s.TurnByAgent))
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
