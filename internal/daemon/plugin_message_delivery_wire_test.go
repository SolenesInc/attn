package daemon_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type deliveredMessage struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	InputID   string `json:"input_id"`
	Text      string `json:"text"`
}

func sendFeedback(app *testworld.Peer, session, text string) string {
	requestID := uuid.NewString()
	app.Send(protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: requestID, SessionID: session, Text: text})
	return requestID
}

func feedbackResult(app *testworld.Peer, requestID string) protocol.SessionAnnotationsSubmitResultMessage {
	app.T.Helper()
	return testworld.Await(app, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool { return r.RequestID == requestID })
}

func awaitingInput(t *testing.T, app *testworld.Peer, driver *driverPeer, run driverLaunch) {
	t.Helper()
	if err := driver.state(run, 1, protocol.StateWaitingInput); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, run.SessionID, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

func settledScreen(t *testing.T, app *testworld.Peer, session string) string {
	t.Helper()
	probe := "probe-" + uuid.NewString()[:8]
	app.TypeLine(session, probe)
	app.AwaitScreen(session, probe)
	screen, err := base64.StdEncoding.DecodeString(protocol.Deref(screenSnapshot(app, session).ScreenSnapshot))
	if err != nil {
		t.Fatal(err)
	}
	return string(screen)
}

func screenLacks(t *testing.T, app *testworld.Peer, session, text string) {
	t.Helper()
	if screen := settledScreen(t, app, session); strings.Contains(screen, text) {
		t.Errorf("the terminal of %s shows %q, want it never typed there:\n%s", session, text, screen)
	}
}

func TestAMessageDeliveringDriverCarriesAttnsInputAndItsReceiptStartsTheTurn(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true, "message_delivery": true})
	awaitDriverAvailable(app, "snipe")
	session, run := spawnDriven(w, app, driver, w.Path("shop"))
	awaitingInput(t, app, driver, run)

	submitted := sendFeedback(app, session, "check the rounding")
	var message deliveredMessage
	driver.answer(driver.asked("driver.deliver_message", &message), map[string]bool{"ok": true})
	if message.SessionID != session || message.RunID != run.RunID || message.Text != "check the rounding" || message.InputID == "" {
		t.Errorf("the driver was handed %+v, want the feedback for run %s with an input id", message, run.RunID)
	}
	if result := feedbackResult(app, submitted); !result.Success || result.Status != "delivered" {
		t.Fatalf("the feedback was answered %+v, want it delivered", result)
	}
	launched := listedState(t, w, session)

	receipt := func(runID string) error {
		return driver.report("session.report_input_taken", map[string]any{"session_id": session, "run_id": runID, "seq": 2, "input_id": message.InputID})
	}
	if err := receipt("run-stale"); !errorSays(err, "does not own active run") {
		t.Errorf("a receipt from another run was answered %v, want an ownership refusal", err)
	}
	if err := receipt(run.RunID); err != nil {
		t.Fatal(err)
	}
	taken := testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return protocol.Deref(s.LastModelRequestAt) != protocol.Deref(launched.LastModelRequestAt)
	})
	if err := receipt(run.RunID); err != nil {
		t.Fatal(err)
	}
	if again := listedState(t, w, session); protocol.Deref(again.LastModelRequestAt) != protocol.Deref(taken.LastModelRequestAt) {
		t.Errorf("a repeated receipt moved the model request from %s to %s", protocol.Deref(taken.LastModelRequestAt), protocol.Deref(again.LastModelRequestAt))
	}
	screenLacks(t, app, session, "check the rounding")
}

func TestADriverWithoutMessageDeliveryHasAttnsInputPastedIntoItsTerminal(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	session, run := spawnDriven(w, app, driver, w.Path("shop"))
	awaitingInput(t, app, driver, run)

	if result := feedbackResult(app, sendFeedback(app, session, "check the rounding")); !result.Success || result.Status != "delivered" {
		t.Fatalf("the feedback was answered %+v, want it delivered", result)
	}
	if screen := settledScreen(t, app, session); !strings.Contains(screen, "check the rounding") {
		t.Errorf("the terminal of %s lacks the pasted feedback:\n%s", session, screen)
	}
}

func TestADeliveryTheDriverDeclinesFailsWithoutTypingIntoTheTerminal(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true, "message_delivery": true})
	awaitDriverAvailable(app, "snipe")
	session, run := spawnDriven(w, app, driver, w.Path("shop"))
	awaitingInput(t, app, driver, run)

	submitted := sendFeedback(app, session, "check the rounding")
	driver.answer(driver.asked("driver.deliver_message", nil), map[string]bool{"ok": false})
	if result := feedbackResult(app, submitted); result.Success || !strings.Contains(protocol.Deref(result.Error), "declined message delivery") {
		t.Errorf("the declined feedback was answered %+v, want a failure saying the plugin declined it", result)
	}
	screenLacks(t, app, session, "check the rounding")
}

func TestTheUsersKeystrokesReachTheTerminalWhileTheDriverHoldsADelivery(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true, "message_delivery": true})
	awaitDriverAvailable(app, "snipe")
	session, run := spawnDriven(w, app, driver, w.Path("shop"))
	awaitingInput(t, app, driver, run)

	submitted := sendFeedback(app, session, "check the rounding")
	held := driver.asked("driver.deliver_message", nil)
	typist := w.App()
	typist.TypeLine(session, "the user's live bytes")
	typist.AwaitScreen(session, "the user's live bytes")
	driver.answer(held, map[string]bool{"ok": true})
	if result := feedbackResult(app, submitted); !result.Success || result.Status != "delivered" {
		t.Errorf("the held feedback was answered %+v, want it delivered once the driver took it", result)
	}
}

func TestAUserTurnCarriedByTheDriverTitlesTheSession(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true, "message_delivery": true})
	awaitDriverAvailable(app, "snipe")
	session, run := spawnDriven(w, app, driver, w.Path("shop"))
	awaitingInput(t, app, driver, run)

	submitted := sendFeedback(app, session, "investigate the retry queue")
	var message deliveredMessage
	driver.answer(driver.asked("driver.deliver_message", &message), map[string]bool{"ok": true})
	feedbackResult(app, submitted)
	driver.mustReport("session.report_input_taken", map[string]any{"session_id": session, "run_id": run.RunID, "seq": 2, "input_id": message.InputID})

	task := w.HeadlessTask()
	if !strings.Contains(task.Prompt, "investigate the retry queue") {
		t.Fatalf("the daemon asked %q, want a title from the user's turn", task.Prompt)
	}
	task.Answer("Retry queue investigation")
	awaitLabel(app, session, "Retry queue investigation")
}
