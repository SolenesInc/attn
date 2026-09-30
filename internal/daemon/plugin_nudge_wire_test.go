package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPluginApprovalReportDoesNotWaitForAnotherSessionsNudgeReply(t *testing.T) {
	for _, channel := range []string{"countdown", "trigger"} {
		t.Run(channel, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				w.finishStartupWork()
				app, cli := w.App(), w.Client()
				driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true, "message_delivery": true})
				awaitDriverAvailable(app, "snipe")
				recipient, recipientRun := spawnDriven(w, app, driver, w.Path("recipient"))
				asking, askingRun := spawnDriven(w, app, driver, w.Path("asking"))
				awaitingInput(t, app, driver, recipientRun)
				awaitingInput(t, app, driver, askingRun)
				createTicket(t, cli, recipient, "fix the build", "build")
				commentOnTicket(t, cli, "chief", "build", "take a look")
				ticketNudgeAwaitDeadline(t, w, app, recipient, time.Now().Add(ticketNudgeCountdown))
				if channel == "countdown" {
					w.advance(ticketNudgeCountdown)
				} else {
					app.Send(protocol.TriggerNudgeMessage{Cmd: protocol.CmdTriggerNudge, SessionID: recipient})
				}
				var message deliveredMessage
				held := driver.asked("driver.deliver_message", &message)
				if message.SessionID != recipient || !strings.Contains(message.Text, inboxDoorbell) {
					t.Fatalf("the driver received %+v, want the recipient's inbox doorbell", message)
				}

				before := time.Now()
				if err := driver.state(askingRun, 2, protocol.StatePendingApproval); err != nil {
					t.Fatal(err)
				}
				if elapsed := time.Since(before); elapsed != 0 {
					t.Fatalf("the approval report waited %s for another session's held nudge reply", elapsed)
				}
				testworld.AwaitSession(app, asking, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
				driver.answer(held, map[string]bool{"ok": true})
				w.advance(0)
				if inbox := readInbox(t, cli, recipient, 0); len(inbox.Items) != 1 || inbox.Items[0].Content != ticketNudgePromptText {
					t.Fatalf("after the driver replied the inbox was %+v, want one ticket nudge", inbox.Items)
				}
			})
		})
	}
}

func TestANudgeHeldForApprovalRingsOnceTheDriverReportsItsAgentIdle(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	author, _ := spawnDriven(w, app, driver, w.Path("author"))
	asking, run := spawnDriven(w, app, driver, w.Path("asking"))
	if err := driver.state(run, 1, protocol.StatePendingApproval); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, asking, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	app.TypeLine(asking, "yes, allow it")
	app.AwaitScreen(asking, "yes, allow it")

	createTicket(t, cli, asking, "fix the build", "build")
	commentOnTicket(t, cli, author, "build", "take a look")
	testworld.AwaitSession(app, asking, func(s protocol.Session) bool { return protocol.Deref(s.TicketUnread) })
	app.Send(protocol.TriggerNudgeMessage{Cmd: protocol.CmdTriggerNudge, SessionID: asking})
	for seq, state := range []string{protocol.StateWorking, protocol.StateIdle} {
		if err := driver.state(run, uint64(seq+2), state); err != nil {
			t.Fatal(err)
		}
	}

	app.AwaitScreen(asking, "attn agent inbox")
}
