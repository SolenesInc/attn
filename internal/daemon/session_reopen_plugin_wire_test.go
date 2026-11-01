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
	w := newWorld(t, fakeagent.Plug)
	app, cli := w.App(), w.Client()
	pluginDriverSettings(app, "plug")
	session := w.Spawn(app, fakeagent.Plug, w.Path("shop"))
	w.Launched(session)
	closeSession(t, cli, session, "done for now")
	awaitClosed(app, session)

	if verdict := reopenVerdict(t, cli, session); !verdict.Reopenable || !slices.Equal(verdict.Actions, []protocol.SessionReopenAction{protocol.SessionReopenActionReopen}) {
		t.Errorf("the verdict on a plug session whose driver resumes = %+v, want it reopenable", verdict)
	}

	uninstalled := testworld.Request(app, protocol.UninstallPluginMessage{Cmd: protocol.CmdUninstallPlugin, Name: "attn-plug"},
		protocol.EventPluginActionResult, func(m protocol.PluginActionResultMessage) bool { return protocol.Deref(m.Name) == "attn-plug" })
	if !uninstalled.Success {
		t.Fatalf("uninstall attn-plug: %s", protocol.Deref(uninstalled.Error))
	}
	verdict := reopenVerdict(t, cli, session)
	if verdict.Reopenable || len(verdict.Actions) != 0 {
		t.Errorf("the verdict after plug was uninstalled = %+v, want no resume action", verdict)
	}
	for _, want := range []string{`"plug"`, "not installed"} {
		if !strings.Contains(protocol.Deref(verdict.Reason), want) {
			t.Errorf("the reason %q does not mention %s", protocol.Deref(verdict.Reason), want)
		}
	}
}

func TestAClosedSessionOfADriverThatDoesNotResumeRefusesResume(t *testing.T) {
	t.Setenv(fakeagent.PlugWithoutResumeEnv, "1")
	w := newWorld(t, fakeagent.Plug)
	app, cli := w.App(), w.Client()
	pluginDriverSettings(app, "plug")
	session := w.Spawn(app, fakeagent.Plug, w.Path("shop"))
	w.Launched(session)
	closeSession(t, cli, session, "done for now")
	awaitClosed(app, session)

	verdict := reopenVerdict(t, cli, session)
	if verdict.Reopenable || len(verdict.Actions) != 0 {
		t.Errorf("the verdict on a plug session whose driver cannot resume = %+v, want no resume action", verdict)
	}
	for _, want := range []string{`"plug"`, "does not resume"} {
		if !strings.Contains(protocol.Deref(verdict.Reason), want) {
			t.Errorf("the reason %q does not mention %s", protocol.Deref(verdict.Reason), want)
		}
	}
}
