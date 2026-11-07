package daemon_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPushWaitingForTheLinkDoesNotHoldTheDaemonsStop(t *testing.T) {
	cloud := testworld.StartCloud(t)
	w := newWorld(t, fakeagent.Claude)
	t.Setenv(config.CloudURLEnv, cloud.HomeURL)
	app := w.App()
	setSetting(t, app, "asks_enabled", "true")
	setSetting(t, app, "phone_access_enabled", "true")
	linked := func(m protocol.PhonesUpdatedMessage) bool { return m.Link.State == protocol.PhoneLinkStateConnected }
	testworld.Await(app, protocol.EventPhonesUpdated, linked)
	paired := testworld.Request(app, protocol.PhonePairBeginMessage{Cmd: protocol.CmdPhonePairBegin, RequestID: "pair", ProfileID: app.SelectedProfile()},
		protocol.EventPhonePairBeginResult, func(m protocol.PhonePairBeginResultMessage) bool { return m.RequestID == "pair" })
	if !paired.Success {
		t.Fatalf("phone_pair_begin: %s", protocol.Deref(paired.Error))
	}
	qr := testworld.ParsePhoneQR(t, paired.PairingCode.Qr)
	phone := testworld.NewPhone(t, cloud, "iPhone")
	socket, err := phone.DialToPair(qr)
	if err != nil {
		t.Fatal(err)
	}
	if err := socket.Pair(qr); err != nil {
		t.Fatal(err)
	}
	socket.Hello()
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)

	before := len(app.Log())
	cloud.Outage()
	testworld.AwaitSince(app, before, protocol.EventPhonesUpdated, func(m protocol.PhonesUpdatedMessage) bool { return !linked(m) })
	if _, err := w.Client().AskOpen(protocol.SessionID(asker), "", ":::question\nShip during the gap?\n- [ ] Yes\n- [ ] No\n:::\n", "", w.Path("shop")); err != nil {
		t.Fatalf("ask: %v", err)
	}
	w.awaitTimer(daemon.PhonePushLinkWaitTimer)

	w.stop()
}

var phonePushFlows = []struct {
	name         string
	queue, quiet bool
}{
	{"queue flow", true, true},
	{"queue flow with quiet notifications off", true, false},
	{"queue flow off", false, true},
}

type pushedPhone struct {
	t     *testing.T
	cloud *testworld.Cloud
	phone *testworld.Phone
}

func (p *pushedPhone) next() testworld.PushNote {
	p.t.Helper()
	select {
	case push := <-p.cloud.Pushes():
		note, err := p.phone.OpenPush(push)
		if err != nil {
			p.t.Fatalf("the phone could not open its push: %v", err)
		}
		return note
	case <-time.After(fakeagent.HangGuard):
		p.t.Fatalf("no push reached the relay within fakeagent.HangGuard=%s", fakeagent.HangGuard)
		return testworld.PushNote{}
	}
}

func (p *pushedPhone) expect(kind, text string) {
	p.t.Helper()
	if note := p.next(); note.Kind != kind || note.Text != text {
		p.t.Fatalf("the next push is %s %q, want %s %q", note.Kind, note.Text, kind, text)
	}
}

func phonePushWorld(t *testing.T, queue, quiet bool) (*world, *testworld.Peer, *client.Client, *pushedPhone) {
	t.Helper()
	cloud := testworld.StartCloud(t)
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	t.Setenv(config.CloudURLEnv, cloud.HomeURL)
	w.start()
	app, cli := w.TrustedApp(), w.Client()
	if queue {
		setSetting(t, app, "queue_mode_enabled", "true")
	}
	if !quiet {
		setSetting(t, app, "queue_quiet_notifications", "false")
	}
	setSetting(t, app, "auto_approve_enabled", "false")
	setSetting(t, app, "phone_access_enabled", "true")
	testworld.Await(app, protocol.EventPhonesUpdated, func(m protocol.PhonesUpdatedMessage) bool {
		return m.Link.State == protocol.PhoneLinkStateConnected
	})
	pairingCode := testworld.Request(app, protocol.PhonePairBeginMessage{Cmd: protocol.CmdPhonePairBegin, RequestID: "pair", ProfileID: app.SelectedProfile()},
		protocol.EventPhonePairBeginResult, func(m protocol.PhonePairBeginResultMessage) bool { return m.RequestID == "pair" })
	if !pairingCode.Success {
		t.Fatalf("phone_pair_begin: %s", protocol.Deref(pairingCode.Error))
	}
	qr := testworld.ParsePhoneQR(t, pairingCode.PairingCode.Qr)
	phone := testworld.NewPhone(t, cloud, "iPhone")
	socket, err := phone.DialToPair(qr)
	if err != nil {
		t.Fatal(err)
	}
	if err := socket.Pair(qr); err != nil {
		t.Fatal(err)
	}
	socket.Hello()
	socket.RegisterForPush()
	registerSessions(t, w, cli, "reviewer")
	return w, app, cli, &pushedPhone{t: t, cloud: cloud, phone: phone}
}

func notificationRunStops(t *testing.T, app *testworld.Peer, cli *client.Client, agent *fakeagent.Run, session string, before protocol.Session, body, reply string) protocol.Session {
	t.Helper()
	working, _ := notificationRunStarts(t, app, cli, agent, session, before, body)
	agent.Reply(reply)
	return testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
}

func TestAStopANotificationStartedSendsThePhoneNothing(t *testing.T) {
	for _, flow := range phonePushFlows {
		t.Run(flow.name, func(t *testing.T) {
			t.Run("an ordinary agent", func(t *testing.T) {
				w, app, cli, phone := phonePushWorld(t, flow.queue, flow.quiet)
				session, agent, owed := owingIdleAgent(t, w, app, cli)
				stopped := notificationRunStops(t, app, cli, agent, session, settleFromApp(app, owed), "the review is in", "Nothing for this plot. <!-- attn:state=waiting_input -->")
				stopped = notificationRunStops(t, app, cli, agent, session, stopped, "the second review is in", "Still nothing. <!-- attn:state=waiting_input -->")
				userRunStops(app, agent, stopped, "anything to land?", "Should I land this? <!-- attn:state=waiting_input -->")
				phone.expect("waiting", "Should I land this?")
			})
			t.Run("the Chief member", func(t *testing.T) {
				w, app, cli, phone := phonePushWorld(t, flow.queue, flow.quiet)
				turnOnCrewV2(t, w)
				run := wakeConfiguredChief(t, w, cli)
				session := string(chiefSession(t, cli))
				greeted := testworld.AwaitSession(app, session, func(s protocol.Session) bool {
					return s.State == protocol.SessionStateIdle && protocol.Deref(s.TurnOwed)
				})
				stopped := notificationRunStops(t, app, cli, run, session, settleFromApp(app, greeted), "the delegate reported", "Noted in the journal. <!-- attn:state=waiting_input -->")
				userRunStops(app, run, stopped, "what is open?", "Two seeds. <!-- attn:state=waiting_input -->")
				phone.expect("waiting", "Two seeds.")
			})
		})
	}
}

func TestAStopThatAnswersTheUserPushesInEitherFlow(t *testing.T) {
	for _, flow := range phonePushFlows {
		t.Run(flow.name, func(t *testing.T) {
			w, app, cli, phone := phonePushWorld(t, flow.queue, flow.quiet)
			session, agent, owed := owingIdleAgent(t, w, app, cli)
			settled := settleFromApp(app, owed)

			sendQuickCapture(t, app, sessionMailbox(session), "what did the reviewer say?")
			if got := agent.Prompted(); !strings.Contains(got, "what did the reviewer say?") {
				t.Fatalf("the agent was prompted with %q, want the user's question", got)
			}
			working := testworld.AwaitStateAfter(app, settled, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			agent.Reply("They approved. <!-- attn:state=waiting_input -->")
			answered := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
			phone.expect("waiting", "They approved.")

			userRunStops(app, agent, settleFromApp(app, answered), "anything to land?", "Should I land this? <!-- attn:state=waiting_input -->")
			phone.expect("waiting", "Should I land this?")
		})
	}
}

func TestANotificationRunThatNeedsApprovalPushesInEitherFlow(t *testing.T) {
	for _, flow := range phonePushFlows {
		t.Run(flow.name, func(t *testing.T) {
			w, app, cli, phone := phonePushWorld(t, flow.queue, flow.quiet)
			session, agent, owed := owingIdleAgent(t, w, app, cli)
			working, _ := notificationRunStarts(t, app, cli, agent, session, settleFromApp(app, owed), "rebase onto main")
			agent.AskApproval()
			testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
			phone.expect("needs_approval", "")
		})
	}
}

func TestARunThatDiesOnAnErrorPushesThatItFailedWithoutAnEarlierAnswer(t *testing.T) {
	type failingRun struct {
		agent    *fakeagent.Run
		cli      *client.Client
		session  string
		terminal protocol.TerminalID
	}
	starts := []struct {
		name string
		run  func(app *testworld.Peer, r failingRun, before protocol.Session) protocol.Session
	}{
		{"a notification's run", func(app *testworld.Peer, r failingRun, before protocol.Session) protocol.Session {
			working, _ := notificationRunStarts(t, app, r.cli, r.agent, r.session, before, "rebase onto main")
			return working
		}},
		{"the user's run", func(app *testworld.Peer, r failingRun, before protocol.Session) protocol.Session {
			app.TypeLine(r.session, "rebase onto main")
			r.agent.Prompted()
			return testworld.AwaitStateAfter(app, before, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		}},
	}
	failures := []struct {
		name string
		fail func(t *testing.T, r failingRun)
	}{
		{"terminal status", func(_ *testing.T, r failingRun) { r.agent.ProgramStatus("state=error:app=claude-code") }},
		{"harness hook", func(t *testing.T, r failingRun) {
			if err := r.cli.RecordStopFailure(r.terminal, "rate_limit", "usage limit reached"); err != nil {
				t.Fatalf("recording the stop failure: %v", err)
			}
		}},
	}
	for _, flow := range phonePushFlows {
		for _, start := range starts {
			for _, failure := range failures {
				t.Run(flow.name+"/"+start.name+"/"+failure.name, func(t *testing.T) {
					w, app, cli, phone := phonePushWorld(t, flow.queue, flow.quiet)
					session, agent, owed := owingIdleAgent(t, w, app, cli)
					run := failingRun{agent: agent, cli: cli, session: session, terminal: protocol.TerminalID(w.Terminal(session))}
					working := start.run(app, run, settleFromApp(app, owed))
					failure.fail(t, run)
					failed := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
					phone.expect("run_failed", "")

					userRunStops(app, agent, settleFromApp(app, failed), "try again", "Rebased. <!-- attn:state=waiting_input -->")
					phone.expect("waiting", "Rebased.")
				})
			}
		}
	}
}

func TestAStopAfterAttnTurnOpenPushesThoughANotificationStartedTheRun(t *testing.T) {
	for _, flow := range phonePushFlows {
		t.Run(flow.name, func(t *testing.T) {
			w, app, cli, phone := phonePushWorld(t, flow.queue, flow.quiet)
			session, agent, owed := owingIdleAgent(t, w, app, cli)
			settled := settleFromApp(app, owed)
			for i, marker := range []string{"waiting_input", "idle"} {
				working, _ := notificationRunStarts(t, app, cli, agent, session, settled, fmt.Sprintf("migration %d failed", i+1))
				opened, err := cli.AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen)
				if err != nil || opened.Outcome != protocol.AgentTurnOutcomeOpensAtStop {
					t.Fatalf("attn turn open while working = %+v, %v; want it to open at the stop", opened, err)
				}
				text := fmt.Sprintf("Migration %d needs your call.", i+1)
				agent.Reply(text + " <!-- attn:state=" + marker + " -->")
				stopped := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
				phone.expect("waiting", text)
				stopped = notificationRunStops(t, app, cli, agent, session, stopped, fmt.Sprintf("migration %d note", i+1), "Noted. <!-- attn:state="+marker+" -->")
				if closed, err := cli.AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionSettle); err != nil || closed.Outcome != protocol.AgentTurnOutcomeSettled {
					t.Fatalf("attn turn settle = %+v, %v; want settled", closed, err)
				}
				settled = testworld.AwaitSession(app, session, func(s protocol.Session) bool {
					return s.StateSince == stopped.StateSince && !protocol.Deref(s.TurnOwed)
				})
			}
		})
	}
}

func TestAnOpenTurnAnnouncedAfterTheUsersPromptDoesNotPushAgainForANotificationsStop(t *testing.T) {
	for _, flow := range phonePushFlows {
		t.Run(flow.name, func(t *testing.T) {
			w, app, cli, phone := phonePushWorld(t, flow.queue, flow.quiet)
			session, agent, owed := owingIdleAgent(t, w, app, cli)
			captureRun := func(before protocol.Session, capture string) protocol.Session {
				t.Helper()
				sendQuickCapture(t, app, sessionMailbox(session), capture)
				if got := agent.Prompted(); !strings.Contains(got, capture) {
					t.Fatalf("the agent was prompted with %q, want %q", got, capture)
				}
				return testworld.AwaitStateAfter(app, before, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			}
			stops := func(working protocol.Session, reply string) protocol.Session {
				t.Helper()
				agent.Reply(reply + " <!-- attn:state=waiting_input -->")
				return testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
			}

			working := captureRun(settleFromApp(app, owed), "run the migration")
			if opened, err := cli.AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen); err != nil || opened.Outcome != protocol.AgentTurnOutcomeOpensAtStop {
				t.Fatalf("attn turn open while working = %+v, %v; want it to open at the stop", opened, err)
			}
			stopped := stops(working, "The migration needs your call.")
			phone.expect("waiting", "The migration needs your call.")

			stopped = notificationRunStops(t, app, cli, agent, session, stopped, "the review is in", "Noted. <!-- attn:state=waiting_input -->")
			stopped = stops(captureRun(stopped, "still there?"), "Yes.")
			phone.expect("waiting", "Yes.")

			stopped = notificationRunStops(t, app, cli, agent, session, stopped, "the second review is in", "Noted again. <!-- attn:state=waiting_input -->")
			stops(captureRun(stopped, "and now?"), "Still here.")
			phone.expect("waiting", "Still here.")
		})
	}
}

func TestAnAgentANotificationWokeIsToldThePhoneStaysSilentWhenItsStopStillOpensATurn(t *testing.T) {
	for _, flow := range phonePushFlows {
		t.Run(flow.name, func(t *testing.T) {
			w, app, cli, _ := phonePushWorld(t, flow.queue, flow.quiet)
			session, agent, owed := owingIdleAgent(t, w, app, cli)
			_, batch := notificationRunStarts(t, app, cli, agent, session, settleFromApp(app, owed), "the review is in")
			quiet := flow.queue && flow.quiet
			if protocol.Deref(batch.QuietNotifications) != quiet || protocol.Deref(batch.PhoneStaysSilent) == quiet {
				t.Fatalf("the agent was told quiet=%v phone_silent=%v in %s", protocol.Deref(batch.QuietNotifications), protocol.Deref(batch.PhoneStaysSilent), flow.name)
			}
		})
	}
}
