package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestWithHeadlessTasksOffNoModelRunsAndSessionsStillSettle(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	if got := app.Initial.Settings["headless_tasks.enabled"]; got != "false" {
		t.Fatalf("the world runs with headless tasks %q, want them off", got)
	}
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	agent := w.Launched(session)
	app.TypeLine(session, "run the checkout tests")
	agent.Prompted()
	createTicket(t, cli, "planner", "Checkout tests", "checkout-tests")
	if _, err := cli.TakeTicket(session, "checkout-tests", false); err != nil {
		t.Fatalf("take the ticket: %v", err)
	}
	taken := showTicket(t, cli, "checkout-tests").Status
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	watcher := w.App()

	agent.Reply("The checkout tests pass.")
	settled := testworld.AwaitSession(watcher, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if settled.Label != "shop" {
		t.Errorf("the settled session is labelled %q, want its launch label kept", settled.Label)
	}
	askWhatItRan := func() {
		t.Helper()
		if _, err := cli.SessionInstructions(session, "What did it run?"); err == nil || !strings.Contains(err.Error(), "model_unavailable") {
			t.Errorf("session instructions = %v, want model_unavailable", err)
		}
	}
	askWhatItRan()
	if got := w.HeadlessTasks(); got != 0 {
		t.Fatalf("with headless tasks off the model ran %d times before the session ended", got)
	}
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	askWhatItRan()
	if got := w.HeadlessTasks(); got != 1 {
		t.Fatalf("with headless tasks on, session instructions ran the model %d times, want 1", got)
	}
	t.Setenv("ATTN_HEADLESS_TASKS", "off")

	agent.Exit(0)
	testworld.Await(watcher, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
	if got := showTicket(t, cli, "checkout-tests").Status; got != taken {
		t.Errorf("the ticket of the ended session is %q, want it left %q with no reconcile model", got, taken)
	}
	if got := w.HeadlessTasks(); got != 1 {
		t.Errorf("with headless tasks off the model ran %d more times", got-1)
	}
}

func TestTheHeadlessTasksSettingIsReportedApartFromItsEnvOverride(t *testing.T) {
	w := newWorld(t)
	t.Setenv("ATTN_HEADLESS_TASKS", "")
	app := w.App()
	const effective, stored, override = "headless_tasks.enabled", "headless_tasks.enabled.stored", "headless_tasks.enabled.override"

	for _, value := range []string{"false", "true"} {
		setSetting(t, app, effective, value)
		settings := testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
			return m.RequestID == nil && m.Settings[stored] == value
		}).Settings
		if settings[effective] != value || settings[stored] != value {
			t.Errorf("after storing %s the settings say effective %q, stored %q", value, settings[effective], settings[stored])
		}
		if got, ok := settings[override]; ok {
			t.Errorf("with no env override the settings name override %q", got)
		}
	}

	t.Setenv("ATTN_HEADLESS_TASKS", "off")
	w.restart()
	settings := w.App().Initial.Settings
	if settings[effective] != "false" || settings[stored] != "true" || settings[override] != "off" {
		t.Errorf("under the env override the settings say effective %q, stored %q, override %q; want false, true, off",
			settings[effective], settings[stored], settings[override])
	}
}
