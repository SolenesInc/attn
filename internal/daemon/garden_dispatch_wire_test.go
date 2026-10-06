package daemon_test

import (
	"os"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestADelegateShowsItsDispatcherAcrossTheDispatchersLife(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := w.Path("dispatch")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	firstDay := wakeCrew(t, cli, "alder", string(fakeagent.Codex)).SessionID
	firstDayRun := w.Launched(string(firstDay))
	fromAlder := gardenDispatchDelegate(t, w, cli, string(firstDay), cwd, "Map the checkout")
	plain, workspace, pane := w.RequestSpawn(app, fakeagent.Codex, w.Path("plain"))
	if !plain.Success {
		t.Fatalf("spawn the plain dispatcher: %s", protocol.Deref(plain.Error))
	}
	w.Launched(string(plain.ID))
	fromPlain := gardenDispatchDelegate(t, w, cli, string(plain.ID), cwd, "Tidy the cart")

	shows := func(when, delegate, session, member string) {
		t.Helper()
		shown := sessionOfDelegate(t, w, delegate)
		if string(protocol.Deref(shown.DispatcherSessionID)) != session || protocol.Deref(shown.DispatcherMember) != member {
			t.Errorf("%s the delegate %s shows dispatcher session %q and member %q, want %q and %q", when, delegate, protocol.Deref(shown.DispatcherSessionID), protocol.Deref(shown.DispatcherMember), session, member)
		}
	}
	shows("while its dispatchers live", fromAlder, string(firstDay), "alder")
	shows("while its dispatchers live", fromPlain, string(plain.ID), "")

	watching := w.App()
	firstDayRun.Exit(0)
	testworld.Await(watching, protocol.EventCrewUpdated, func(e protocol.CrewUpdatedMessage) bool {
		return slices.ContainsFunc(e.Members, func(m protocol.CrewMember) bool { return m.ID == "alder" && m.BindingSession == nil })
	})
	shows("once alder's day ended and alder sleeps", fromAlder, "", "alder")

	secondDay := wakeCrew(t, cli, "alder", string(fakeagent.Codex)).SessionID
	w.Launched(string(secondDay))
	shows("once alder woke into a new day", fromAlder, string(secondDay), "alder")

	closePane(app, sessionPane{session: string(plain.ID), desktop: workspace, pane: pane})
	awaitClosed(app, string(plain.ID))
	shows("once its plain dispatcher closed", fromPlain, "", "")
}

func gardenDispatchDelegate(t *testing.T, w *world, cli *client.Client, dispatcher, cwd, brief string) string {
	t.Helper()
	request := delegateFrom(dispatcher, cwd, brief, fakeagent.Codex)
	request.Label = protocol.Ptr(brief)
	delegated, err := cli.Delegate(request)
	if err != nil {
		t.Fatalf("%s delegates %q: %v", dispatcher, brief, err)
	}
	w.Launched(string(delegated.SessionID))
	return string(delegated.SessionID)
}
