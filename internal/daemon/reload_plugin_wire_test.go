package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func reloadPi(app *testworld.Peer, session string) protocol.ReloadSessionResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
}

func converse(t *testing.T, app *testworld.Peer, session string, run *fakeagent.Run, prompt string) {
	t.Helper()
	app.TypeLine(session, prompt)
	if got := run.Prompted(); got != prompt {
		t.Fatalf("pi for %s received %q, want %q", session, got, prompt)
	}
	working := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	run.Reply("Done. <!-- attn:state=idle -->")
	testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestAReloadedPluginSessionResumesItsConversationOnTheLaunchItHad(t *testing.T) {
	w := newWorld(t, fakeagent.Pi)
	app := w.App()
	pluginDriverSettings(app, "pi")
	session := w.Spawn(app, fakeagent.Pi, w.Path("shop"))
	first := w.Launched(session)

	if reloaded := reloadPi(app, session); !reloaded.Success {
		t.Fatalf("reload: %s", protocol.Deref(reloaded.Error))
	}
	second := w.Launched(session)
	if !second.Resumed || second.ConversationID != first.ConversationID || slices.Contains(second.Argv, "--append-system-prompt") {
		t.Errorf("the reload ran pi resuming %v %s with guidance %v, want it resuming %s with no launch instructions its driver does not take",
			second.Resumed, second.ConversationID, slices.Contains(second.Argv, "--append-system-prompt"), first.ConversationID)
	}
	converse(t, app, session, second, "price the checkout")
}

func TestAReloadedPluginChiefResumesWithCurrentChiefGuidanceAndItsPins(t *testing.T) {
	t.Setenv(fakeagent.PiCapabilitiesEnv, "launch_instructions,model_pin,effort_pin,yolo")
	w := newWorld(t, fakeagent.Pi)
	app := w.App()
	pluginDriverSettings(app, "pi")
	configured, err := w.Client().CrewSet("chief", nil, protocol.Ptr("pi"), protocol.Ptr("provider/model"), protocol.Ptr("high"), nil)
	if err != nil || configured.WokeSessionID == nil {
		t.Fatalf("configure Chief=%+v %v", configured, err)
	}
	chief := string(*configured.WokeSessionID)
	first := w.Launched(chief)
	first.Prompted()
	first.Reply("Ready. <!-- attn:state=idle -->")

	if reloaded := reloadPi(app, chief); !reloaded.Success {
		t.Fatalf("reload: %s", protocol.Deref(reloaded.Error))
	}
	second := w.Launched(chief)
	model, _ := flagValue(second.Argv, "--model")
	thinking, _ := flagValue(second.Argv, "--thinking")
	guidance, _ := flagValue(second.Argv, "--append-system-prompt")
	if !second.Resumed || second.ConversationID != first.ConversationID || model != "provider/model" || thinking != "high" {
		t.Errorf("the reload ran pi resuming %v %s on %q at %q (yolo %v), want it resuming %s on provider/model at high effort in yolo",
			second.Resumed, second.ConversationID, model, thinking, second.Yolo, first.ConversationID)
	}
	if !strings.Contains(guidance, "You are this profile's Chief") {
		t.Error("the reloaded chief was launched without the chief guidance")
	}
	converse(t, app, chief, second, "plan the week")
}

func TestAPluginSessionKeepsRunningWhenItsDriverCannotRelaunchIt(t *testing.T) {
	t.Setenv(fakeagent.PiCapabilitiesEnv, "launch_instructions")
	w := newWorld(t, fakeagent.Pi)
	app := w.App()
	pluginDriverSettings(app, "pi")
	chief := configureChiefOn(t, w, app, fakeagent.Pi, "test-model")
	worker := w.Spawn(app, fakeagent.Pi, w.Path("worker"))
	runs := map[string]*fakeagent.Run{chief: w.Launched(chief), worker: w.Launched(worker)}
	runs[chief].Prompted()
	runs[chief].Reply("Ready. <!-- attn:state=idle -->")

	allow := w.RefusePiLaunches("the pi session store is locked")
	if reloaded := reloadPi(app, chief); reloaded.Success || !strings.Contains(protocol.Deref(reloaded.Error), "store is locked") {
		t.Errorf("reloading the chief = %+v, want it failing with the driver's reason", reloaded)
	}
	allow()

	for session, run := range runs {
		converse(t, app, session, run, "still there?")
	}
}
