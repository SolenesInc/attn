package daemon_test

import (
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
	qr := testworld.ParsePhoneQR(t, paired.Offer.Qr)
	phone := testworld.NewPhone(t, cloud, "iPhone")
	socket, err := phone.Dial(qr)
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
	offer := testworld.Request(app, protocol.PhonePairBeginMessage{Cmd: protocol.CmdPhonePairBegin, RequestID: "pair", ProfileID: app.SelectedProfile()},
		protocol.EventPhonePairBeginResult, func(m protocol.PhonePairBeginResultMessage) bool { return m.RequestID == "pair" })
	if !offer.Success {
		t.Fatalf("phone_pair_begin: %s", protocol.Deref(offer.Error))
	}
	qr := testworld.ParsePhoneQR(t, offer.Offer.Qr)
	phone := testworld.NewPhone(t, cloud, "iPhone")
	socket, err := phone.Dial(qr)
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

func TestAStopAfterAttnTurnOpenPushesThoughANotificationStartedTheRun(t *testing.T) {
	for _, flow := range phonePushFlows {
		if !flow.queue {
			continue
		}
		t.Run(flow.name, func(t *testing.T) {
			w, app, cli, phone := phonePushWorld(t, flow.queue, flow.quiet)
			session, agent, owed := owingIdleAgent(t, w, app, cli)
			working, _ := notificationRunStarts(t, app, cli, agent, session, settleFromApp(app, owed), "the migration failed")
			opened, err := cli.AgentTurn(protocol.SessionID(session), protocol.AgentTurnActionOpen)
			if err != nil || opened.Outcome != protocol.AgentTurnOutcomeOpensAtStop {
				t.Fatalf("attn turn open while working = %+v, %v; want it to open at the stop", opened, err)
			}
			agent.Reply("The migration needs your call. <!-- attn:state=waiting_input -->")
			testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
			phone.expect("waiting", "The migration needs your call.")
		})
	}
}
