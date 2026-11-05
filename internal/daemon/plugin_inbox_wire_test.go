package daemon_test

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPluginApprovalReportDoesNotWaitForAnotherSessionsInboxReply(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, cli := w.App(), w.Client()
		driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true, "message_delivery": true})
		awaitDriverAvailable(app, "snipe")
		recipient, recipientRun := spawnDriven(w, app, driver, w.Path("recipient"))
		asking, askingRun := spawnDriven(w, app, driver, w.Path("asking"))
		awaitingInput(t, app, driver, recipient, recipientRun)
		awaitingInput(t, app, driver, asking, askingRun)
		sent := make(chan error, 1)
		go func() { _, err := cli.AgentMsg(recipient, protocol.SessionID(asking), "take a look"); sent <- err }()
		var message deliveredMessage
		held := driver.asked("driver.deliver_message", &message)
		if message.SessionID != recipientRun.SessionID || !strings.Contains(message.Text, inboxDoorbell) {
			t.Fatalf("delivery=%+v", message)
		}
		before := time.Now()
		if err := driver.state(askingRun, 2, protocol.StatePendingApproval); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(before); elapsed != 0 {
			t.Fatalf("approval report waited %s for the held delivery", elapsed)
		}
		testworld.AwaitSession(app, asking, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
		driver.answer(held, map[string]bool{"ok": true})
		if err := <-sent; err != nil {
			t.Fatal(err)
		}
		if got := inboxContents(readInbox(t, cli, recipient, 0).Items); got != "take a look" {
			t.Fatalf("inbox=%q", got)
		}
	})
}

func TestAnAnnotationSentWhileARingIsBeingPlacedArrivesAfterIt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, annotator, cli := w.App(), w.App(), w.Client()
		driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true, "message_delivery": true})
		awaitDriverAvailable(app, "snipe")
		author, _ := spawnDriven(w, app, driver, w.Path("author"))
		recipient, run := spawnDriven(w, app, driver, w.Path("recipient"))
		awaitingInput(t, app, driver, recipient, run)
		sent := make(chan error, 1)
		go func() { _, err := cli.AgentMsg(recipient, protocol.SessionID(author), "take a look"); sent <- err }()
		var ring deliveredMessage
		heldRing := driver.asked("driver.deliver_message", &ring)
		if !strings.Contains(ring.Text, inboxDoorbell) {
			t.Fatalf("first delivery=%+v, want the ring", ring)
		}

		submitted := sendFeedback(annotator, recipient, "check the rounding")
		synctest.Wait()
		driver.answer(heldRing, map[string]bool{"ok": true})
		if err := <-sent; err != nil {
			t.Fatal(err)
		}

		var annotation deliveredMessage
		heldAnnotation := driver.asked("driver.deliver_message", &annotation)
		if annotation.Text != "check the rounding" {
			t.Fatalf("second delivery=%+v, want the annotation", annotation)
		}
		driver.answer(heldAnnotation, map[string]bool{"ok": true})
		if result := feedbackResult(annotator, submitted); !result.Success || result.Status != "delivered" {
			t.Fatalf("the annotation was answered %+v, want it delivered", result)
		}
	})
}

func TestARingWhileAnAnnotationIsBeingPlacedArrivesAfterIt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, annotator, cli := w.App(), w.App(), w.Client()
		driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true, "message_delivery": true})
		awaitDriverAvailable(app, "snipe")
		author, _ := spawnDriven(w, app, driver, w.Path("author"))
		recipient, run := spawnDriven(w, app, driver, w.Path("recipient"))
		awaitingInput(t, app, driver, recipient, run)
		submitted := sendFeedback(annotator, recipient, "check the rounding")
		heldAnnotation := driver.asked("driver.deliver_message", nil)

		sent := make(chan error, 1)
		go func() { _, err := cli.AgentMsg(recipient, protocol.SessionID(author), "take a look"); sent <- err }()
		synctest.Wait()
		before := time.Now()
		driver.answer(heldAnnotation, map[string]bool{"ok": true})
		if result := feedbackResult(annotator, submitted); !result.Success {
			t.Fatalf("the annotation was answered %+v, want it delivered", result)
		}

		var ring deliveredMessage
		heldRing := driver.asked("driver.deliver_message", &ring)
		if elapsed := time.Since(before); elapsed != 0 {
			t.Errorf("the ring waited %s after the annotation was placed", elapsed)
		}
		if !strings.Contains(ring.Text, inboxDoorbell) {
			t.Fatalf("second delivery=%+v, want the ring", ring)
		}
		driver.answer(heldRing, map[string]bool{"ok": true})
		if err := <-sent; err != nil {
			t.Fatal(err)
		}
	})
}

func TestAnInboxHeldForApprovalRingsOnceTheDriverReportsIdle(t *testing.T) {
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
	app.Watch(asking)
	sent := sendAgentMessage(t, cli, author, asking, "take a look")
	if sent.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("approval send=%+v", sent)
	}
	for seq, state := range []string{protocol.StateWorking, protocol.StateIdle} {
		if err := driver.state(run, uint64(seq+2), state); err != nil {
			t.Fatal(err)
		}
	}
	app.AwaitScreen(asking, "attn agent inbox")
	if got := inboxContents(readInbox(t, cli, asking, 0).Items); got != "take a look" {
		t.Fatalf("inbox=%q", got)
	}
}
