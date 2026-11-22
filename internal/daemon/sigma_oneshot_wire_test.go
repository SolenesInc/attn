package daemon_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func sigmaOnlyWorld(t *testing.T) (*world, *testworld.Peer) {
	t.Helper()
	w := newTitlingWorld(t, fakeagent.Sigma)
	app := w.TrustedApp()
	setSetting(t, app, "sigma_enabled", "true")
	return w, app
}

func sigmaTask(t *testing.T, w *world, asks string) *fakeagent.HeadlessTask {
	t.Helper()
	return sigmaTaskAt(t, w, "low", asks)
}

func sigmaTaskAt(t *testing.T, w *world, effort, asks string) *fakeagent.HeadlessTask {
	t.Helper()
	task := w.HeadlessTask()
	if task.Harness != fakeagent.Sigma || task.Model != "openai/gpt-6-luna" || task.Effort != effort || !strings.Contains(task.Prompt, asks) {
		t.Fatalf("the daemon asked %s %q at effort %q: %q, want Sigma's light default openai/gpt-6-luna at %q asked about %q", task.Harness, task.Model, task.Effort, task.Prompt, effort, asks)
	}
	return task
}

func TestOnAHomeWithOnlySigmaASigmaSessionIsTitledAndItsStopClassifiedBySigma(t *testing.T) {
	w, app := sigmaOnlyWorld(t)
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	promptUntilWorking(app, session, view, server, "the login form rejects valid passwords")
	sigmaTask(t, w, "the login form rejects valid passwords").Answer("Fix login flow")
	awaitLabel(app, session, "Fix login flow")

	server.Reply(view.ConversationID, "The fix is ready. Should I deploy it to staging?")
	sigmaTask(t, w, "Should I deploy it to staging?").Answer(`{"verdict":"WAITING"}`)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

func TestOnAHomeWithOnlySigmaTheActivityLineIsWrittenBySigma(t *testing.T) {
	w, app := sigmaOnlyWorld(t)
	setSetting(t, app, "activity.enabled", "true")
	setSetting(t, app, "activity.config", `{"agent":"sigma"}`)
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma, func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("checkout")
	})
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, DashboardVisible: true})
	serverActivityTurn(t, w, app, view, server, session, "start on the tests", "Starting on the tests.")
	awaitActivityTask(app, session, func(task protocol.Task) bool { return task.State == "done" })

	serverActivityTurn(t, w, app, view, server, session, "run the frontend tests", "The frontend suite is running.")
	sigmaTask(t, w, "The frontend suite is running.").Answer("Running the frontend test suite.")
	awaitActivity(app, session, "Running the frontend test suite")

	serverActivityTurn(t, w, app, view, server, session, "run the whole suite", "The whole suite is running.")
	sigmaTask(t, w, "The whole suite is running.").Fail("not logged in")
	awaitActivityTask(app, session, func(task protocol.Task) bool { return task.State == "failed" })
	if failed := activitySession(t, w.Client(), session); !strings.Contains(protocol.Deref(failed.Error), "authentication failed") || protocol.Deref(failed.Activity) != "Running the frontend test suite" {
		t.Errorf("activity after Sigma failed = error %q beside line %q, want the failure beside the last good line", protocol.Deref(failed.Error), protocol.Deref(failed.Activity))
	}
}

func TestSettingsOfferSigmaForBackgroundCallsAndAnAbandonedDictationStopsItsSigmaRun(t *testing.T) {
	w, app := sigmaOnlyWorld(t)
	if got := pluginDriverSettings(app, "sigma")["sigma_cap_headless_task"]; got != "true" {
		t.Fatalf("sigma_cap_headless_task = %q, want Sigma offered for background model calls", got)
	}
	setSetting(t, app, "activity.config", `{"agent":"sigma"}`)
	setSetting(t, app, "garden.advisor", `{"agent":"sigma"}`)
	setSetting(t, app, "model_calls.agent", "sigma")
	session, _, _ := appServerSession(t, w, app, fakeagent.Sigma, func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("checkout")
	})
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	token := pairSenderInApp(t, w.Client(), app, "Dictation")
	conn, err := w.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(fakeagent.HangGuard)); err != nil {
		t.Fatal(err)
	}
	call := protocol.SenderModelCallMessage{
		Cmd: protocol.CmdSenderModelCall, Input: "so um fix the the login bug", LimitMs: int(fakeagent.HangGuard.Milliseconds()),
		Purpose: "dictation", SenderToken: token.Token, System: "Clean up the dictated text.", Tier: protocol.Ptr(protocol.ModelTierLight),
	}
	if err := json.NewEncoder(conn).Encode(call); err != nil {
		t.Fatal(err)
	}
	sigmaTaskAt(t, w, "", "so um fix the the login bug").Answer("Fix the login bug.")
	var response protocol.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if answer := response.SenderModelCallResult; !response.Ok || answer == nil || answer.Harness != "sigma" || protocol.Deref(answer.Text) != "Fix the login bug." {
		t.Fatalf("the dictation call answered %+v, want Sigma's refined text", response)
	}

	abandoning, err := w.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	call.Input = "and the uh signup page"
	if err := json.NewEncoder(abandoning).Encode(call); err != nil {
		t.Fatal(err)
	}
	abandoned := sigmaTaskAt(t, w, "", "and the uh signup page")
	_ = abandoning.Close()
	abandoned.AwaitExited()
}
