package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAClosedPluginSessionReopensOnlyWhileItsDriverCanResumeIt(t *testing.T) {
	w := newWorld(t, fakeagent.Pi)
	app, cli := w.App(), w.Client()
	pluginDriverSettings(app, "pi")
	session := w.Spawn(app, fakeagent.Pi, w.Path("shop"))
	w.Launched(session)
	closeSession(t, cli, session, "done for now")
	awaitClosed(app, session)

	if verdict := reopenVerdict(t, cli, session); !verdict.Reopenable || !slices.Equal(verdict.Actions, []protocol.SessionReopenAction{protocol.SessionReopenActionReopen}) {
		t.Errorf("the verdict on a pi session whose driver resumes = %+v, want it reopenable", verdict)
	}

	uninstalled := testworld.Request(app, protocol.UninstallPluginMessage{Cmd: protocol.CmdUninstallPlugin, Name: "attn-pi"},
		protocol.EventPluginActionResult, func(m protocol.PluginActionResultMessage) bool { return protocol.Deref(m.Name) == "attn-pi" })
	if !uninstalled.Success {
		t.Fatalf("uninstall attn-pi: %s", protocol.Deref(uninstalled.Error))
	}
	verdict := reopenVerdict(t, cli, session)
	if verdict.Reopenable || !slices.Equal(verdict.Actions, []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshSamePlace}) {
		t.Errorf("the verdict after pi was uninstalled = %+v, want only a fresh start in place", verdict)
	}
	for _, want := range []string{`"pi"`, "not installed"} {
		if !strings.Contains(protocol.Deref(verdict.Reason), want) {
			t.Errorf("the reason %q does not mention %s", protocol.Deref(verdict.Reason), want)
		}
	}
}

func TestAClosedSessionOfADriverThatDoesNotResumeOffersOnlyAFreshStart(t *testing.T) {
	t.Setenv(fakeagent.PiWithoutResumeEnv, "1")
	w := newWorld(t, fakeagent.Pi)
	app, cli := w.App(), w.Client()
	pluginDriverSettings(app, "pi")
	session := w.Spawn(app, fakeagent.Pi, w.Path("shop"))
	w.Launched(session)
	closeSession(t, cli, session, "done for now")
	awaitClosed(app, session)

	verdict := reopenVerdict(t, cli, session)
	if verdict.Reopenable || !slices.Equal(verdict.Actions, []protocol.SessionReopenAction{protocol.SessionReopenActionStartFreshSamePlace}) {
		t.Errorf("the verdict on a pi session whose driver cannot resume = %+v, want only a fresh start in place", verdict)
	}
	for _, want := range []string{`"pi"`, "does not resume"} {
		if !strings.Contains(protocol.Deref(verdict.Reason), want) {
			t.Errorf("the reason %q does not mention %s", protocol.Deref(verdict.Reason), want)
		}
	}
}
