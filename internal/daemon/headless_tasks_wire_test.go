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
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	watcher := w.App()

	agent.Reply("The checkout tests pass.")
	settled := testworld.AwaitSession(watcher, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if settled.Label != "shop" {
		t.Errorf("the settled session is labelled %q, want its launch label kept", settled.Label)
	}
	askWhatItRan := func() <-chan error {
		asked := make(chan error, 1)
		go func() {
			_, err := cli.SessionInstructions(session, "What did it run?")
			asked <- err
		}()
		return asked
	}
	if err := <-askWhatItRan(); err == nil || !strings.Contains(err.Error(), "model_unavailable") {
		t.Errorf("with headless tasks off, session instructions = %v, want model_unavailable", err)
	}
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	asked := askWhatItRan()
	w.HeadlessTask().Fail("the model is overloaded")
	if err := <-asked; err == nil || !strings.Contains(err.Error(), "model_unavailable") {
		t.Errorf("with a failing model, session instructions = %v, want model_unavailable", err)
	}
	t.Setenv("ATTN_HEADLESS_TASKS", "off")

	agent.Exit(0)
	testworld.Await(watcher, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.SessionID == session })
}

func TestTheHeadlessTasksSettingIsReportedApartFromItsEnvOverride(t *testing.T) {
	w := newWorld(t)
	t.Setenv("ATTN_HEADLESS_TASKS", "")
	app := w.App()
	const effective, stored, override = "headless_tasks.enabled", "headless_tasks.enabled.stored", "headless_tasks.enabled.override"

	for _, value := range []string{"false", "true"} {
		setSetting(t, app, effective, value)
		settings := testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
			return m.RequestID == nil && protocol.Deref(m.ChangedKey) == effective && m.Settings[stored] == value
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

func TestClaudeSignedInWithoutAnAPIKeyRunsHeadlessTasks(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
		t.Setenv(name, "")
	}
	w := newTitlingWorld(t, fakeagent.Claude)
	app := w.App()
	if got := app.Initial.Settings["claude_cap_headless_task"]; got != "true" {
		t.Fatalf("the app is told Claude can run headless tasks: %v, want true", got)
	}
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("investigate the retry queue")
	})
	agent := w.Launched(session)
	if task := w.HeadlessTask(); task.Harness != fakeagent.Claude {
		t.Fatalf("the title task went to %s, want Claude", task.Harness)
	} else {
		task.Answer("Retry queue investigation")
	}
	awaitLabel(app, session, "Retry queue investigation")
	agent.Prompted()
}
