package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func reloadPlug(app *testworld.Peer, session string) protocol.ReloadSessionResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
}

func converse(t *testing.T, app *testworld.Peer, session string, run *fakeagent.Run, prompt string) {
	t.Helper()
	app.TypeLine(session, prompt)
	if got := run.Prompted(); got != prompt {
		t.Fatalf("plug for %s received %q, want %q", session, got, prompt)
	}
	working := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	run.Reply("Done. <!-- attn:state=idle -->")
	testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestAReloadedPluginSessionResumesItsConversationOnTheLaunchItHad(t *testing.T) {
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	pluginDriverSettings(app, "plug")
	session := w.Spawn(app, fakeagent.Plug, w.Path("shop"))
	first := w.Launched(session)

	if reloaded := reloadPlug(app, session); !reloaded.Success {
		t.Fatalf("reload: %s", protocol.Deref(reloaded.Error))
	}
	second := w.Launched(session)
	if !second.Resumed || second.ConversationID != first.ConversationID || slices.Contains(second.Argv, "--append-system-prompt") {
		t.Errorf("the reload ran plug resuming %v %s with guidance %v, want it resuming %s with no launch instructions its driver does not take",
			second.Resumed, second.ConversationID, slices.Contains(second.Argv, "--append-system-prompt"), first.ConversationID)
	}
	converse(t, app, session, second, "price the checkout")
}

func TestAReloadedPluginSessionResumesWithItsPins(t *testing.T) {
	t.Setenv(fakeagent.PlugCapabilitiesEnv, "launch_instructions,model_pin,effort_pin,yolo")
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	pluginDriverSettings(app, "plug")
	session := w.Spawn(app, fakeagent.Plug, w.Path("planner"), func(m *protocol.SpawnSessionMessage) {
		m.Model = protocol.Ptr("provider/model")
		m.Effort = protocol.Ptr("high")
		m.YoloMode = protocol.Ptr(true)
	})
	first := w.Launched(session)

	if reloaded := reloadPlug(app, session); !reloaded.Success {
		t.Fatalf("reload: %s", protocol.Deref(reloaded.Error))
	}
	second := w.Launched(session)
	model, _ := flagValue(second.Argv, "--model")
	effort, _ := flagValue(second.Argv, "--effort")
	if !second.Resumed || second.ConversationID != first.ConversationID || model != "provider/model" || effort != "high" || !second.Yolo {
		t.Errorf("the reload ran plug resuming %v %s on %q at %q (yolo %v), want it resuming %s on provider/model at high effort in yolo",
			second.Resumed, second.ConversationID, model, effort, second.Yolo, first.ConversationID)
	}
	converse(t, app, session, second, "plan the week")
}

func TestAPluginSessionKeepsRunningWhenItsDriverCannotRelaunchIt(t *testing.T) {
	t.Setenv(fakeagent.PlugCapabilitiesEnv, "launch_instructions")
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	pluginDriverSettings(app, "plug")
	planner := w.Spawn(app, fakeagent.Plug, w.Path("planner"))
	worker := w.Spawn(app, fakeagent.Plug, w.Path("worker"))
	runs := map[string]*fakeagent.Run{planner: w.Launched(planner), worker: w.Launched(worker)}

	allow := w.RefusePlugLaunches("the plug session store is locked")
	if reloaded := reloadPlug(app, planner); reloaded.Success || !strings.Contains(protocol.Deref(reloaded.Error), "store is locked") {
		t.Errorf("reloading %s = %+v, want it failing with the driver's reason", planner, reloaded)
	}
	allow()

	for session, run := range runs {
		converse(t, app, session, run, "still there?")
	}
}

func TestReloadingASessionWhoseHarnessIsGoneIsRefusedNamingTheHarness(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	driver := connectDriver(t, w, "attn-house", "house", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "house")
	session, _ := spawnDriven(w, app, driver, w.Path("shop"))
	driver.leave(w)

	if reloaded := reloadPlug(app, session); reloaded.Success || !strings.Contains(protocol.Deref(reloaded.Error), `agent "house" is not available`) {
		t.Fatalf("reloading a session whose harness is gone = %+v, want it refused naming the harness", reloaded)
	}
	if got := queriedSession(t, w.Client(), session); got.Agent != "house" {
		t.Fatalf("the refused reload left the session as agent %q, want house", got.Agent)
	}
}
