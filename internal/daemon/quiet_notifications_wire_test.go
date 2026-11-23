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
	"github.com/victorarias/attn/internal/pausepoint"
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
	if got := agent.DeliveredPrompt(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("the agent was prompted with %q, want the inbox doorbell", got)
	}
	working := testworld.AwaitStateAfter(app, before, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	return working, readInbox(t, cli, session, 0)
}

func notificationRunSettled(app *testworld.Peer, working protocol.Session) protocol.Session {
	app.T.Helper()
	return testworld.AwaitSession(app, string(working.ID), func(s protocol.Session) bool {
		return s.StateSince == working.StateSince && !protocol.Deref(s.TurnOwed)
	})
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

func TestAnAgentANotificationStartedStaysOutOfTheQueueWhenItStops(t *testing.T) {
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
	working = notificationRunSettled(app, working)
	if !protocol.Deref(working.TurnByAgent) {
		t.Fatalf("once the notification set the agent working its turn shows by_agent=%v, want settled by the agent", protocol.Deref(working.TurnByAgent))
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
		working = notificationRunSettled(app, working)
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

func TestAQuestionWhoseRunEndsBeforeAttnHearsItWasTakenLeavesTheNextNotificationRunQuiet(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	handed := w.PauseAt(pausepoint.SessionInputHanded)
	app, cli := w.TrustedApp(), w.Client()
	setSetting(t, app, "queue_mode_enabled", "true")
	registerSessions(t, w, cli, "reviewer")
	session, agent, owed := owingIdleAgent(t, w, app, cli)
	settled := settleFromApp(app, owed)

	sendQuickCapture(t, app, sessionMailbox(session), "What is the review status?")
	agent.Prompted()
	handed.Await()
	working := testworld.AwaitStateAfter(app, settled, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	agent.Reply("The review is green. <!-- attn:state=idle -->")
	answered := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	handed.Release()
	if !protocol.Deref(answered.TurnOwed) {
		t.Fatal("the reply to the user's question stopped without opening the user's turn")
	}

	settled = settleFromApp(app, answered)
	working, _ = notificationRunStarts(t, app, cli, agent, session, settled, "the review is in")
	agent.Reply("Nothing for this plot. <!-- attn:state=idle -->")
	if stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle }); protocol.Deref(stopped.TurnOwed) {
		t.Fatal("the user's question, taken by a run that had already ended, opened a turn for the next notification run")
	}
}

func TestAQuickCaptureThatSteersANotificationRunOpensItsTurnAtTheStop(t *testing.T) {
	w, _, cli := quietNotificationsWorld(t, true)
	app := w.TrustedApp()
	session, agent, owed := owingIdleAgent(t, w, app, cli)
	settled := settleFromApp(app, owed)

	working, _ := notificationRunStarts(t, app, cli, agent, session, settled, "the review is in")
	sendQuickCapture(t, app, sessionMailbox(session), "what did the reviewer say?")
	modEvent(t, agent, fakeagent.ModEventHeld, "what did the reviewer say?")
	submittedAtTurnEnd(t, agent, "what did the reviewer say?", true)
	agent.Reply("They approved. <!-- attn:state=idle -->")
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(stopped.TurnOwed) || protocol.Deref(stopped.TurnByAgent) {
		t.Fatalf("the steered run stopped with owed=%v by_agent=%v, want the user's turn open",
			protocol.Deref(stopped.TurnOwed), protocol.Deref(stopped.TurnByAgent))
	}
}

func TestAQuickCaptureRungWithOtherItemsOpensItsTurn(t *testing.T) {
	for _, quiet := range []string{"true", "false"} {
		t.Run("quiet_notifications="+quiet, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.TrustedApp(), w.Client()
				setSetting(t, app, "queue_mode_enabled", "true")
				setSetting(t, app, "queue_quiet_notifications", quiet)
				setSetting(t, app, "auto_settle_enabled", "true")
				registerSessions(t, w, cli, "peer")
				agent := w.bubbleClaude(t, app, "shop")
				if booted := queriedSession(t, cli, agent.id); protocol.Deref(booted.TurnOwed) {
					settleFromApp(app, booted)
				}

				app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(agent.self), Data: "half a thought"})
				synctest.Wait()
				sendAgentMessage(t, cli, "peer", agent.id, "the review is in")
				sendQuickCapture(t, app, sessionMailbox(agent.id), "what did the reviewer say?")
				w.advance(sessionmonitor.QuietWindow)
				pasted := agent.term.Pasted()
				if len(pasted) != 1 || !strings.Contains(pasted[0], "what did the reviewer say?") || strings.Contains(pasted[0], inboxDoorbell) {
					t.Fatalf("once the user was quiet attn pasted %q, want the capture in a ring of its own", pasted)
				}
				agent.reply("They approved. <!-- attn:state=idle -->")
				replied := queriedSession(t, cli, agent.id)
				if !protocol.Deref(replied.TurnOwed) || replied.TurnOpenedAt == nil {
					t.Fatalf("the capture's reply left no turn owed: %+v", replied)
				}

				pasted = agent.term.Pasted()
				if len(pasted) != 2 || !strings.Contains(pasted[1], inboxDoorbell) || strings.Contains(pasted[1], "what did the reviewer say?") {
					t.Fatalf("attn pasted %q, want the capture, then the doorbell alone", pasted)
				}
				for doorbell := 1; doorbell <= 2; doorbell++ {
					if doorbell == 2 {
						sendAgentMessage(t, cli, "peer", agent.id, "a second review is in")
						synctest.Wait()
						pasted = agent.term.Pasted()
						if len(pasted) != 3 || !strings.Contains(pasted[2], inboxDoorbell) {
							t.Fatalf("attn pasted %q, want a second doorbell", pasted)
						}
					}
					agent.take(pasted[doorbell])
					if batch := readInbox(t, cli, agent.id, 0); protocol.Deref(batch.QuietNotifications) {
						t.Errorf("doorbell %d: the agent was told its run is quiet and it left the queue, while the user still owes the capture's reply", doorbell)
					}
					w.advance(autoSettleDefaultArm)
					w.advance(autoSettleDefaultCountdown)
					working := queriedSession(t, cli, agent.id)
					if working.State != protocol.SessionStateWorking || working.AutoSettleFiresAt != nil || !protocol.Deref(working.TurnOwed) || protocol.Deref(working.TurnOpenedAt) != *replied.TurnOpenedAt {
						t.Fatalf("past the auto-settle countdown doorbell %d's run shows state=%s owed=%t opened=%q countdown=%q, want the reply's turn owed from %q with no countdown",
							doorbell, working.State, protocol.Deref(working.TurnOwed), protocol.Deref(working.TurnOpenedAt), protocol.Deref(working.AutoSettleFiresAt), *replied.TurnOpenedAt)
					}

					mark := len(app.Log())
					agent.reply("Read the review. <!-- attn:state=idle -->")
					stopped := testworld.AwaitSince(app, mark, protocol.EventSessionStateChanged, func(update protocol.SessionStateChangedMessage) bool {
						return string(update.Session.ID) == agent.id && update.Session.State == protocol.SessionStateIdle
					}).Session
					if !protocol.Deref(stopped.TurnOwed) || protocol.Deref(stopped.TurnByAgent) || protocol.Deref(stopped.TurnOpenedAt) != *replied.TurnOpenedAt {
						t.Fatalf("after doorbell %d's run the session shows owed=%t by_agent=%t opened=%q, want the reply's turn still owed from %q",
							doorbell, protocol.Deref(stopped.TurnOwed), protocol.Deref(stopped.TurnByAgent), protocol.Deref(stopped.TurnOpenedAt), *replied.TurnOpenedAt)
					}
				}
			})
		})
	}
}

func TestTheReplyToACaptureThatAskedForApprovalStaysOwedThroughTheDoorbellsRun(t *testing.T) {
	w, _, cli := quietNotificationsWorld(t, true)
	app := w.TrustedApp()
	session, agent, owed := owingIdleAgent(t, w, app, cli)
	settled := settleFromApp(app, owed)

	sendQuickCapture(t, app, sessionMailbox(session), "what did the reviewer say?")
	if got := agent.DeliveredPrompt(); !strings.Contains(got, "what did the reviewer say?") {
		t.Fatalf("the agent was prompted with %q, want the capture", got)
	}
	working := testworld.AwaitStateAfter(app, settled, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	agent.AskApproval()
	asking := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	answered := settleFromApp(app, asking)
	agent.Dismiss()
	working = testworld.AwaitStateAfter(app, answered, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	agent.Reply("They approved. <!-- attn:state=idle -->")
	replied := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(replied.TurnOwed) {
		t.Fatal("the reply to the user's capture stopped without opening the user's turn")
	}

	working, _ = notificationRunStarts(t, app, cli, agent, session, replied, "the review is in")
	if shown := queriedSession(t, cli, session); !protocol.Deref(shown.TurnOwed) || protocol.Deref(shown.TurnOpenedAt) != protocol.Deref(replied.TurnOpenedAt) {
		t.Fatalf("the doorbell's run shows owed=%t opened=%q, want the reply's turn owed from %q",
			protocol.Deref(shown.TurnOwed), protocol.Deref(shown.TurnOpenedAt), protocol.Deref(replied.TurnOpenedAt))
	}
	agent.Reply("Read the review. <!-- attn:state=idle -->")
	stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(stopped.TurnOwed) || protocol.Deref(stopped.TurnOpenedAt) != protocol.Deref(replied.TurnOpenedAt) {
		t.Fatalf("after the doorbell's run the session shows owed=%t opened=%q, want the reply's turn still owed from %q",
			protocol.Deref(stopped.TurnOwed), protocol.Deref(stopped.TurnOpenedAt), protocol.Deref(replied.TurnOpenedAt))
	}
}

func TestTheReplyToACaptureClaudeHeldThroughItsTurnStaysOwedAfterTheDoorbellsRun(t *testing.T) {
	w, _, cli := quietNotificationsWorld(t, true)
	app := w.TrustedApp()
	setAnalyticsEnabled(app, "true")
	session, run := moddedClaude(t, w, app)
	workingOn(t, app, run, session, "write the essay")

	sendQuickCapture(t, app, sessionMailbox(session), "what did the reviewer say?")
	modEvent(t, run, fakeagent.ModEventHeld, "what did the reviewer say?")
	submittedAtTurnEnd(t, run, "what did the reviewer say?", true)
	sendAgentMessage(t, cli, "reviewer", session, "the review is in")

	replying := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	run.Reply("They approved. <!-- attn:state=idle -->")
	replied := testworld.AwaitStateAfter(app, replying, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(replied.TurnOwed) {
		t.Fatal("the reply to the user's capture stopped without opening the user's turn")
	}
	if got := run.Prompted(); !strings.Contains(got, inboxDoorbell) || run.AsUser {
		t.Fatalf("after the capture's reply the agent took %q (as the user: %t), want the doorbell in attn's voice", got, run.AsUser)
	}
	working := testworld.AwaitStateAfter(app, replied, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	readInbox(t, cli, session, 0)
	if shown := queriedSession(t, cli, session); !protocol.Deref(shown.TurnOwed) || protocol.Deref(shown.TurnOpenedAt) != protocol.Deref(replied.TurnOpenedAt) {
		t.Fatalf("the doorbell's run shows owed=%t opened=%q, want the reply's turn owed from %q",
			protocol.Deref(shown.TurnOwed), protocol.Deref(shown.TurnOpenedAt), protocol.Deref(replied.TurnOpenedAt))
	}
	stopped := runEnds(w, app, run, working, "Read the review. <!-- attn:state=idle -->")
	sendAgentMessage(t, cli, "reviewer", session, "a second review is in")
	if got := run.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("after the first doorbell's run the agent took %q, want the second doorbell", got)
	}
	working = testworld.AwaitStateAfter(app, stopped, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	if shown := queriedSession(t, cli, session); !protocol.Deref(shown.TurnOwed) || protocol.Deref(shown.TurnOpenedAt) != protocol.Deref(replied.TurnOpenedAt) {
		t.Fatalf("the second doorbell's run shows owed=%t opened=%q, want the reply's turn owed from %q",
			protocol.Deref(shown.TurnOwed), protocol.Deref(shown.TurnOpenedAt), protocol.Deref(replied.TurnOpenedAt))
	}
	stopped = runEnds(w, app, run, working, "Read the second review. <!-- attn:state=idle -->")
	if runs := runRecords(t, w, app, session); len(runs) < 2 || runs[len(runs)-2].StartedBy != "ring" || runs[len(runs)-1].StartedBy != "ring" {
		t.Fatalf("run records %v, want both doorbells' runs started by the ring, so auto-settle never counts them as the user's", runs)
	}
	if stopped.State != protocol.SessionStateIdle || !protocol.Deref(stopped.TurnOwed) || protocol.Deref(stopped.TurnByAgent) || protocol.Deref(stopped.TurnOpenedAt) != protocol.Deref(replied.TurnOpenedAt) {
		t.Fatalf("after the doorbell's run the session shows owed=%v by_agent=%v opened=%q, want the capture's reply still owed from %q",
			protocol.Deref(stopped.TurnOwed), protocol.Deref(stopped.TurnByAgent), protocol.Deref(stopped.TurnOpenedAt), protocol.Deref(replied.TurnOpenedAt))
	}
}

func TestTheReplyToACaptureClaudeHeldStaysOwedWhenTheUserSettledTheTurnBeforeIt(t *testing.T) {
	w, _, cli := quietNotificationsWorld(t, true)
	app := w.TrustedApp()
	session, run := moddedClaude(t, w, app)
	workingOn(t, app, run, session, "write the essay")
	working := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	capture := sendQuickCapture(t, app, sessionMailbox(session), "what did the reviewer say?")
	modEvent(t, run, fakeagent.ModEventHeld, "what did the reviewer say?")
	testworld.Await(app, protocol.EventQuickCaptureRead, func(m protocol.QuickCaptureReadMessage) bool { return m.CaptureID == capture.CaptureID })
	submittedAtTurnEnd(t, run, "what did the reviewer say?", true)
	essayDone := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	if !protocol.Deref(essayDone.TurnOwed) {
		t.Fatal("the essay's run stopped without opening the user's turn")
	}
	replying := testworld.AwaitStateAfter(app, essayDone, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	settled := settleFromApp(app, replying)
	run.Reply("They approved. <!-- attn:state=idle -->")
	replied := testworld.AwaitStateAfter(app, settled, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(replied.TurnOwed) || protocol.Deref(replied.TurnOpenedAt) == protocol.Deref(essayDone.TurnOpenedAt) {
		t.Fatalf("the capture's reply shows owed=%t opened=%q, want a new turn after the essay's %q",
			protocol.Deref(replied.TurnOwed), protocol.Deref(replied.TurnOpenedAt), protocol.Deref(essayDone.TurnOpenedAt))
	}

	captureReplyOwedThroughDoorbells(t, w, app, cli, run, session, replied)
}

func TestTheReplyToACaptureThatStopsBeforeItsPlacementReturnsStaysOwed(t *testing.T) {
	w, app, cli := quietNotificationsWorld(t, true)
	app = w.TrustedApp()
	session, agent, owed := owingIdleAgent(t, w, app, cli)
	settled := settleFromApp(app, owed)

	handed := w.PauseAt(pausepoint.SessionInputHanded)
	capture := sendQuickCapture(t, app, sessionMailbox(session), "What is the review status?")
	agent.Prompted()
	handed.Await()
	working := testworld.AwaitStateAfter(app, settled, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	agent.Reply("The review is green. <!-- attn:state=idle -->")
	replied := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(replied.TurnOwed) {
		t.Fatal("the capture's reply stopped without opening the user's turn")
	}
	handed.Release()
	testworld.Await(app, protocol.EventQuickCaptureRead, func(m protocol.QuickCaptureReadMessage) bool { return m.CaptureID == capture.CaptureID })

	captureReplyOwedThroughDoorbells(t, w, app, cli, agent, session, replied)
}

func TestTheReplyToACaptureStaysOwedThroughADoorbellAfterARestart(t *testing.T) {
	w, _, cli := quietNotificationsWorld(t, true)
	app := w.TrustedApp()
	session, agent, owed := owingIdleAgent(t, w, app, cli)
	settled := settleFromApp(app, owed)
	sendQuickCapture(t, app, sessionMailbox(session), "What is the review status?")
	agent.Prompted()
	working := testworld.AwaitStateAfter(app, settled, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	agent.Reply("The review is green. <!-- attn:state=idle -->")
	replied := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(replied.TurnOwed) {
		t.Fatal("the capture's reply stopped without opening the user's turn")
	}

	w.restart()
	app = w.App()
	w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ID = protocol.SessionID(session)
		m.ResumeSessionID = protocol.Ptr(session)
	})
	resumed := w.Launched(session)
	restored := testworld.AwaitSessionFromConnect(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if !protocol.Deref(restored.TurnOwed) || protocol.Deref(restored.TurnOpenedAt) != protocol.Deref(replied.TurnOpenedAt) {
		t.Fatalf("after a restart the capture's reply shows owed=%v opened=%q, want it owed from %q",
			protocol.Deref(restored.TurnOwed), protocol.Deref(restored.TurnOpenedAt), protocol.Deref(replied.TurnOpenedAt))
	}
	registerSessions(t, w, w.Client(), "reviewer")
	captureReplyOwedThroughDoorbells(t, w, app, w.Client(), resumed, session, restored)
}

func captureReplyOwedThroughDoorbells(t *testing.T, w *world, app *testworld.Peer, cli *client.Client, agent *fakeagent.Run, session string, replied protocol.Session) {
	t.Helper()
	want := protocol.Deref(replied.TurnOpenedAt)
	stopped := replied
	for i := range 2 {
		working, batch := notificationRunStarts(t, app, cli, agent, session, stopped, fmt.Sprintf("review round %d is in", i+1))
		if shown := queriedSession(t, cli, session); !protocol.Deref(shown.TurnOwed) || protocol.Deref(shown.TurnOpenedAt) != want {
			t.Fatalf("doorbell %d's run shows owed=%t opened=%q, want the capture's reply owed from %q", i+1, protocol.Deref(shown.TurnOwed), protocol.Deref(shown.TurnOpenedAt), want)
		}
		if protocol.Deref(batch.QuietNotifications) {
			t.Errorf("doorbell %d: attn agent inbox told the agent the capture's reply left the queue", i+1)
		}
		stopped = runEnds(w, app, agent, working, "Nothing for this plot. <!-- attn:state=idle -->")
		if !protocol.Deref(stopped.TurnOwed) || protocol.Deref(stopped.TurnByAgent) || protocol.Deref(stopped.TurnOpenedAt) != want {
			t.Fatalf("after doorbell %d's run the session shows owed=%t by_agent=%t opened=%q, want the capture's reply owed from %q",
				i+1, protocol.Deref(stopped.TurnOwed), protocol.Deref(stopped.TurnByAgent), protocol.Deref(stopped.TurnOpenedAt), want)
		}
	}
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
	restored := testworld.AwaitSessionFromConnect(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
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
	stopped := testworld.AwaitSessionFromConnect(app, session, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateIdle && protocol.Deref(s.TurnOwed)
	})
	if !protocol.Deref(stopped.TurnByAgent) {
		t.Fatal("after a restart the agent's stop opened a turn that is not the agent's")
	}
}
