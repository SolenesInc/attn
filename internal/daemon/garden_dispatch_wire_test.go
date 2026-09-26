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
	firstDayRun := w.Launched(firstDay)
	fromAlder := gardenDispatchDelegate(t, w, cli, firstDay, cwd, "Map the checkout")
	plain, workspace, pane := w.RequestSpawn(app, fakeagent.Codex, w.Path("plain"))
	if !plain.Success {
		t.Fatalf("spawn the plain dispatcher: %s", protocol.Deref(plain.Error))
	}
	w.Launched(plain.ID)
	fromPlain := gardenDispatchDelegate(t, w, cli, plain.ID, cwd, "Tidy the cart")

	shows := func(when, delegate, session, member string) {
		t.Helper()
		shown := sessionOfDelegate(t, w, delegate)
		if protocol.Deref(shown.DispatcherSessionID) != session || protocol.Deref(shown.DispatcherMember) != member {
			t.Errorf("%s the delegate %s shows dispatcher session %q and member %q, want %q and %q", when, delegate, protocol.Deref(shown.DispatcherSessionID), protocol.Deref(shown.DispatcherMember), session, member)
		}
	}
	shows("while its dispatchers live", fromAlder, firstDay, "alder")
	shows("while its dispatchers live", fromPlain, plain.ID, "")

	watching := w.App()
	firstDayRun.Exit(0)
	testworld.Await(watching, protocol.EventCrewUpdated, func(e protocol.CrewUpdatedMessage) bool {
		return slices.ContainsFunc(e.Members, func(m protocol.CrewMember) bool { return m.ID == "alder" && m.BindingSession == nil })
	})
	shows("once alder's day ended and alder sleeps", fromAlder, "", "alder")

	secondDay := wakeCrew(t, cli, "alder", string(fakeagent.Codex)).SessionID
	w.Launched(secondDay)
	shows("once alder woke into a new day", fromAlder, secondDay, "alder")

	closePane(app, sessionPane{session: plain.ID, workspace: workspace, pane: pane})
	awaitClosed(app, plain.ID)
	shows("once its plain dispatcher closed", fromPlain, "", "")
}

func gardenDispatchDelegate(t *testing.T, w *world, cli *client.Client, dispatcher, cwd, brief string) string {
	t.Helper()
	delegated, err := cli.Delegate(delegateFrom(dispatcher, cwd, brief, fakeagent.Codex))
	if err != nil {
		t.Fatalf("%s delegates %q: %v", dispatcher, brief, err)
	}
	w.Launched(delegated.SessionID)
	return delegated.SessionID
}
