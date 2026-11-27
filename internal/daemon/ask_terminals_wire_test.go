package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/homegateway"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAskHoldsAShellWithNoTileUntilItIsAnsweredOrATileTakesIt(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	asked, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatalf("ask: %v", err)
	}

	inAsk := func(p *testworld.Peer, h fakeagent.Harness, askID string, desktop ...string) protocol.SpawnResultMessage {
		t.Helper()
		result, desktopID, paneID := w.RequestSpawn(p, h, w.Path("shell"), func(m *protocol.SpawnSessionMessage) {
			m.Placement = &protocol.SessionPlacement{AskID: protocol.Ptr(askID)}
			if len(desktop) > 0 {
				m.Placement.DesktopID = protocol.Ptr(desktop[0])
			}
		})
		if result.Success && (desktopID != "" || paneID != "") {
			t.Errorf("a shell spawned into ask %s got pane %q on desktop %q; want no pane", askID, paneID, desktopID)
		}
		return result
	}
	held := func(bindings []protocol.TerminalBinding, askID string) []protocol.SessionID {
		var sessions []protocol.SessionID
		for _, binding := range bindings {
			if protocol.Deref(binding.AskID) == askID {
				sessions = append(sessions, binding.SessionID)
			}
		}
		slices.Sort(sessions)
		return sessions
	}
	refused := func(result protocol.SpawnResultMessage, want string) {
		t.Helper()
		if result.Success || !strings.Contains(protocol.Deref(result.Error), want) {
			t.Errorf("the spawn was answered success=%v %q; want a refusal saying %q", result.Success, protocol.Deref(result.Error), want)
		}
	}

	ending := inAsk(app, shellHarness, asked.ID)
	kept := inAsk(app, shellHarness, asked.ID)
	if !ending.Success || !kept.Success {
		t.Fatalf("spawning shells into ask %s: %q, %q", asked.ID, protocol.Deref(ending.Error), protocol.Deref(kept.Error))
	}
	both := []protocol.SessionID{ending.ID, kept.ID}
	slices.Sort(both)
	shown := testworld.Await(app, protocol.EventTerminalBindingsUpdated, func(m protocol.TerminalBindingsUpdatedMessage) bool {
		return slices.Equal(held(m.TerminalBindings, asked.ID), both)
	})
	terminalOf := map[protocol.SessionID]protocol.TerminalID{}
	for _, binding := range shown.TerminalBindings {
		terminalOf[binding.SessionID] = binding.TerminalID
	}
	fresh := w.App()
	for _, desktop := range fresh.Initial.Desktops {
		for _, pane := range desktop.Panes {
			if pane.SessionID == ending.ID || pane.SessionID == kept.ID {
				t.Errorf("desktop %s shows pane %s for session %s, which the ask holds", desktop.ID, pane.PaneID, pane.SessionID)
			}
		}
	}
	if sessions := held(fresh.Initial.TerminalBindings, asked.ID); !slices.Equal(sessions, both) {
		t.Errorf("an app connecting now sees ask %s holding %v; want both shells", asked.ID, sessions)
	}

	err = cli.RunCommand("terminal send", protocol.SessionID(asker), map[string]any{"terminal": string(terminalOf[ending.ID]), "text": "ls"}, new(map[string]any))
	if err == nil || !strings.Contains(err.Error(), "is in ask "+asked.ID) {
		t.Errorf("typing into the ask's shell was answered %v; want a refusal saying it is in the ask", err)
	}
	homeDesktop, _ := placedPane(t, w, asker)
	refused(inAsk(app, shellHarness, asked.ID, homeDesktop.ID), "beside ask_id")
	refused(inAsk(app, fakeagent.Claude, asked.ID), "holds only a shell")
	id := uuid.NewString()
	other := mustProfileRequest(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: id, Name: "Other"}, id).Profile
	refused(inAsk(w.AppOn(other.ID), shellHarness, asked.ID), "is not open")

	moved := terminalDo[protocol.TerminalMoveResult](t, cli, "terminal move", protocol.SessionID(asker), map[string]any{"terminal": string(terminalOf[kept.ID]), "desktop": "1"})
	if moved.PaneID == "" {
		t.Fatalf("moving the ask's shell onto desktop 1 reported %+v; want a pane", moved)
	}
	testworld.Await(app, protocol.EventTerminalBindingsUpdated, func(m protocol.TerminalBindingsUpdatedMessage) bool {
		return slices.Equal(held(m.TerminalBindings, asked.ID), []protocol.SessionID{ending.ID}) &&
			slices.ContainsFunc(m.TerminalBindings, func(b protocol.TerminalBinding) bool { return b.SessionID == kept.ID && b.AskID == nil })
	})
	setSetting(t, app, "phone_access_enabled", "true")
	phone := connectPhone(t, w, "phone", app.SelectedProfile())
	phone.hello(homegateway.APIVersion)
	listed := map[string]bool{}
	for _, s := range phone.sessions() {
		listed[s.ID] = true
	}
	if !listed[string(asker)] || !listed[string(kept.ID)] || listed[string(ending.ID)] {
		t.Errorf("the phone lists sessions %v; want the asker and the shell a tile took, and not shell %s, which the ask holds", listed, ending.ID)
	}

	mark := len(app.Log())
	answerCacheAsk(t, app, asked.ID)
	awaitClosed(app, string(ending.ID))
	testworld.AwaitSince(app, mark, protocol.EventTerminalBindingsUpdated, func(m protocol.TerminalBindingsUpdatedMessage) bool {
		return len(held(m.TerminalBindings, asked.ID)) == 0
	})
	sessions, err := cli.Query("")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(sessions, func(s protocol.Session) bool { return s.ID == kept.ID }) {
		t.Errorf("answering the ask closed shell %s, which a tile took first", kept.ID)
	}
	refused(inAsk(app, shellHarness, asked.ID), "is not open")

	withdrawn, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatalf("second ask: %v", err)
	}
	inWithdrawn := inAsk(app, shellHarness, withdrawn.ID)
	if !inWithdrawn.Success {
		t.Fatalf("spawning a shell into ask %s: %s", withdrawn.ID, protocol.Deref(inWithdrawn.Error))
	}
	if err := cli.AskClose(protocol.SessionID(asker), withdrawn.ID, ""); err != nil {
		t.Fatalf("withdrawing ask %s: %v", withdrawn.ID, err)
	}
	awaitClosed(app, string(inWithdrawn.ID))
}

func TestOnlyClientsOfTheAsksProfileSeeWhichAskHoldsAShell(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	home := app.SelectedProfile()
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	asked, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	id := uuid.NewString()
	other := mustProfileRequest(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: id, Name: "Other"}, id).Profile
	otherApp := w.AppOn(other.ID)
	bindingOf := func(bindings []protocol.TerminalBinding, session protocol.SessionID) (protocol.TerminalBinding, bool) {
		i := slices.IndexFunc(bindings, func(b protocol.TerminalBinding) bool { return b.SessionID == session })
		if i < 0 {
			return protocol.TerminalBinding{}, false
		}
		return bindings[i], true
	}
	withheld := func(path string, bindings []protocol.TerminalBinding, session protocol.SessionID) {
		t.Helper()
		binding, ok := bindingOf(bindings, session)
		if !ok {
			t.Errorf("%s: the other profile's client does not see shell %s's binding", path, session)
		} else if binding.AskID != nil {
			t.Errorf("%s: the other profile's client sees shell %s held by ask %s", path, session, *binding.AskID)
		}
	}

	mark := len(otherApp.Log())
	shell := protocol.SessionID(w.Spawn(app, shellHarness, w.Path("shell"), func(m *protocol.SpawnSessionMessage) {
		m.Placement = &protocol.SessionPlacement{AskID: protocol.Ptr(asked.ID)}
	}))
	awaitAskHolds(app, asked.ID, shell)
	live := testworld.AwaitSince(otherApp, mark, protocol.EventTerminalBindingsUpdated, func(m protocol.TerminalBindingsUpdatedMessage) bool {
		_, ok := bindingOf(m.TerminalBindings, shell)
		return ok
	})
	withheld("terminal_bindings_updated", live.TerminalBindings, shell)

	if !askHolds(w.AppOn(home).Initial.TerminalBindings, asked.ID, shell) {
		t.Errorf("initial_state: a client of the ask's profile does not see ask %s holding shell %s", asked.ID, shell)
	}
	withheld("initial_state", w.AppOn(other.ID).Initial.TerminalBindings, shell)

	mark = len(otherApp.Log())
	otherApp.Send(protocol.ProfileSelectMessage{Cmd: protocol.CmdProfileSelect, RequestID: uuid.NewString(), ProfileID: home})
	testworld.AwaitSince(otherApp, mark, protocol.EventTerminalBindingsUpdated, func(m protocol.TerminalBindingsUpdatedMessage) bool {
		return askHolds(m.TerminalBindings, asked.ID, shell)
	})
}

func TestShowingAShellAnAskHoldsGivesItATileBesideTheAskingAgent(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	askerDesktop, _ := placedPane(t, w, asker)
	id := uuid.NewString()
	elsewhere := mustProfileRequest(app, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, RequestID: id, ProfileID: app.SelectedProfile(), Name: protocol.Ptr("Elsewhere")}, id).Desktops[0]
	w.Spawn(app, fakeagent.Claude, w.Path("elsewhere"))
	asked, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	shell := protocol.SessionID(w.Spawn(app, shellHarness, w.Path("shell"), func(m *protocol.SpawnSessionMessage) {
		m.Placement = &protocol.SessionPlacement{AskID: protocol.Ptr(asked.ID)}
	}))
	awaitAskHolds(app, asked.ID, shell)
	setSetting(t, app, "phone_access_enabled", "true")
	phone := connectPhone(t, w, "phone", app.SelectedProfile())
	phone.hello(homegateway.APIVersion)
	listsShell := func(sessions []homegateway.HomeGatewaySession) bool {
		return slices.ContainsFunc(sessions, func(s homegateway.HomeGatewaySession) bool { return s.ID == string(shell) })
	}
	if listsShell(phone.sessions()) {
		t.Errorf("the phone lists shell %s, which ask %s holds", shell, asked.ID)
	}

	id = uuid.NewString()
	shown := mustProfileRequest(app, protocol.DesktopShowSessionMessage{Cmd: protocol.CmdDesktopShowSession, RequestID: id, SessionID: shell}, id)
	var on string
	for _, desktop := range shown.Desktops {
		if slices.ContainsFunc(desktop.Panes, func(p protocol.DesktopPane) bool { return p.SessionID == shell }) {
			on = desktop.ID
		}
	}
	if on != askerDesktop.ID {
		t.Errorf("showing the shell ask %s holds put it on desktop %q; want %s, beside the asking agent, not %s, the current one", asked.ID, on, askerDesktop.ID, elsewhere.ID)
	}
	testworld.Await(app, protocol.EventTerminalBindingsUpdated, func(m protocol.TerminalBindingsUpdatedMessage) bool {
		return slices.ContainsFunc(m.TerminalBindings, func(b protocol.TerminalBinding) bool { return b.SessionID == shell && b.AskID == nil })
	})
	for !listsShell(phone.sessions()) {
	}
	answerCacheAsk(t, app, asked.ID)
	if !sessionListed(t, cli, shell) {
		t.Errorf("answering ask %s closed shell %s, which its tile took first", asked.ID, shell)
	}
}
