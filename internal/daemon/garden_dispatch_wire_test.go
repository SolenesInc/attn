package daemon_test

import (
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/google/uuid"
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
	firstDay := startCrew(t, cli, "alder", string(fakeagent.Codex)).SessionID
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
		if string(protocol.Deref(shown.DispatcherSessionID)) != session || dispatcherMemberOf(&shown) != member {
			t.Errorf("%s the delegate %s shows dispatcher session %q and member %q, want %q and %q", when, delegate, protocol.Deref(shown.DispatcherSessionID), dispatcherMemberOf(&shown), session, member)
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

	secondDay := startCrew(t, cli, "alder", string(fakeagent.Codex)).SessionID
	w.Launched(string(secondDay))
	shows("once alder woke into a new day", fromAlder, string(secondDay), "alder")

	closePane(app, sessionPane{session: string(plain.ID), desktop: workspace, pane: pane})
	awaitClosed(app, string(plain.ID))
	shows("once its plain dispatcher closed", fromPlain, string(plain.ID), "")
}

func TestALiveDelegateKeepsTheClosedDispatchersAboveItUntilOneReopens(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	top, _, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("top"))
	if !top.Success {
		t.Fatalf("spawn the top of the chain: %s", protocol.Deref(top.Error))
	}
	w.Launched(string(top.ID))
	cwd := w.Path("dispatch")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	codex := []protocol.DelegationChoice{{ID: "default", Name: "Everyday", Selection: protocol.DelegationSelection{Harness: "codex"}}}
	saved := uuid.NewString()
	if roles := preferencesRequest(app, protocol.DelegationPreferencesSaveMessage{
		Cmd: protocol.CmdDelegationPreferencesSave, RequestID: saved,
		Preferences: protocol.DelegationPreferences{
			Enabled:  true,
			Roles:    []protocol.DelegationRole{{ID: "orchestrate", Name: "Orchestrate", Enabled: true, DefaultChoiceID: "default", Choices: codex}},
			Fallback: protocol.DelegationFallback{Selection: protocol.DelegationSelection{Harness: "codex"}},
		},
	}, saved); !roles.Success {
		t.Fatalf("saving the roles: %s", protocol.Deref(roles.Error))
	}
	w.SetCodexFolderTrust(cwd, "trusted")
	orchestrate := brief(cwd, "Orchestrate the sync")
	orchestrate.SourceSessionID = protocol.Ptr(top.ID)
	orchestrate.Label = protocol.Ptr("Orchestrate the sync")
	orchestrate.Role = protocol.Ptr("orchestrate")
	orchestrated, err := cli.Delegate(orchestrate)
	if err != nil {
		t.Fatalf("top delegates the orchestrator: %v", err)
	}
	w.Launched(string(orchestrated.SessionID))
	middle := string(orchestrated.SessionID)
	lower := gardenDispatchDelegate(t, w, cli, middle, cwd, "Build the push")
	leaf := gardenDispatchDelegate(t, w, cli, lower, cwd, "Review the push")

	chainOf := func(s protocol.Session) []string {
		chain := []string{string(protocol.Deref(s.DispatcherSessionID))}
		for _, closed := range s.ClosedDispatchers {
			chain = append(chain, string(closed.Entry.ID)+">"+string(protocol.Deref(closed.DispatcherSessionID)))
		}
		return chain
	}
	wantClosed := []string{lower, lower + ">" + middle, middle + ">" + string(top.ID)}

	mark := len(app.Log())
	for _, closing := range []string{lower, middle} {
		if closed := closeFromApp(app, closing); !closed.Accepted {
			t.Fatalf("close %s: %s", closing, protocol.Deref(closed.Error))
		}
		awaitClosed(app, closing)
	}
	pushed := testworld.AwaitSessionSince(app, mark, leaf, func(s protocol.Session) bool { return slices.Equal(chainOf(s), wantClosed) })
	if orchestrator := pushed.ClosedDispatchers[1]; orchestrator.Entry.Label != "Orchestrate the sync" || protocol.Deref(orchestrator.Entry.ClosedAt) == "" || orchestrator.DelegationRole == nil || orchestrator.DelegationRole.Name != "Orchestrate" {
		t.Errorf("the closed orchestrator above %s is %+v with role %+v, want its ledger row, closed, with the role Orchestrate", leaf, orchestrator.Entry, orchestrator.DelegationRole)
	}
	if got := chainOf(sessionOfDelegate(t, w, leaf)); !slices.Equal(got, wantClosed) {
		t.Errorf("an app connecting after both closed sees %s under %v, want %v", leaf, got, wantClosed)
	}

	mark = len(app.Log())
	reopen := uuid.NewString()
	app.Send(protocol.SessionReopenMessage{Cmd: protocol.CmdSessionReopen, RequestID: protocol.Ptr(reopen), SessionID: protocol.SessionID(middle)})
	if reopened := testworld.Await(app, protocol.EventSessionReopenResult, func(r protocol.SessionReopenResultMessage) bool { return r.RequestID == reopen }); !reopened.Success {
		t.Fatalf("reopen %s: %s", middle, protocol.Deref(reopened.Error))
	}
	w.Launched(middle)
	wantReopened := []string{lower, lower + ">" + middle}
	testworld.AwaitSessionSince(app, mark, leaf, func(s protocol.Session) bool { return slices.Equal(chainOf(s), wantReopened) })
	if reopenedMiddle := sessionOfDelegate(t, w, middle); string(protocol.Deref(reopenedMiddle.DispatcherSessionID)) != string(top.ID) || reopenedMiddle.ClosedDispatchers != nil {
		t.Errorf("the reopened orchestrator names dispatcher %q above closed %v, want %s and none", protocol.Deref(reopenedMiddle.DispatcherSessionID), reopenedMiddle.ClosedDispatchers, top.ID)
	}
}

func TestADelegateKeepsItsDispatcherAcrossARestartAfterManySessionsClosed(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Codex)
	cli := w.Client()
	cwd := w.Path("dispatch")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	alder := startCrew(t, cli, "alder", string(fakeagent.Codex)).SessionID
	w.Launched(string(alder))
	delegate := gardenDispatchDelegate(t, w, cli, string(alder), cwd, "Map the checkout")
	for i := range 100 {
		earlier := fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
		if err := w.InjectSession(earlier, earlier, w.Path("earlier"), "claude"); err != nil {
			t.Fatalf("register earlier session %d: %v", i, err)
		}
		if err := cli.Unregister(protocol.SessionID(earlier)); err != nil {
			t.Fatalf("close earlier session %d: %v", i, err)
		}
	}

	w.restart()

	shown := sessionOfDelegate(t, w, delegate)
	if dispatcherMemberOf(&shown) != "alder" || protocol.Deref(shown.SeedID) == "" {
		t.Errorf("after a restart the delegate shows dispatcher member %q and seed %q, want alder and its seed", dispatcherMemberOf(&shown), protocol.Deref(shown.SeedID))
	}
}

func gardenDispatchDelegate(t *testing.T, w *world, cli *client.Client, dispatcher, cwd, brief string) string {
	t.Helper()
	w.SetCodexFolderTrust(cwd, "trusted")
	request := delegateFrom(dispatcher, cwd, brief, fakeagent.Codex)
	request.Label = protocol.Ptr(brief)
	delegated, err := cli.Delegate(request)
	if err != nil {
		t.Fatalf("%s delegates %q: %v", dispatcher, brief, err)
	}
	w.Launched(string(delegated.SessionID))
	return string(delegated.SessionID)
}
