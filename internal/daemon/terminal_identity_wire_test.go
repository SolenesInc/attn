package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTerminalBindingsIncludeOtherProfilesAndTheirArrangementChanges(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	firstProfile := app.SelectedProfile()
	first := w.Spawn(app, fakeagent.Claude, w.Path("first"))
	w.Launched(first)
	firstTerminal := protocol.TerminalID(app.Terminal(first))
	otherProfile := createProfile(app, "Other")
	otherApp := w.AppOn(otherProfile.ID)
	other := w.Spawn(otherApp, fakeagent.Claude, w.Path("other"))
	w.Launched(other)
	otherTerminal := protocol.TerminalID(otherApp.Terminal(other))
	token, err := os.ReadFile(filepath.Join(w.Dir, "client-token"))
	if err != nil {
		t.Fatal(err)
	}
	observer := w.Connect(protocol.ClientHelloMessage{Cmd: protocol.CmdClientHello, ClientKind: "hub", ClientToken: protocol.Ptr(strings.TrimSpace(string(token))), Version: "protocol-" + protocol.ProtocolVersion, ProfileID: protocol.Ptr(firstProfile)}, nil)
	initial := testworld.Await[protocol.InitialStateMessage](observer, protocol.EventInitialState, nil)
	bindings := map[protocol.TerminalID]protocol.SessionID{}
	for _, binding := range initial.TerminalBindings {
		bindings[binding.TerminalID] = binding.SessionID
	}
	if bindings[firstTerminal] != protocol.SessionID(first) || bindings[otherTerminal] != protocol.SessionID(other) {
		t.Fatalf("bindings omit a profile: %v", bindings)
	}
	for _, desktop := range initial.Desktops {
		if desktop.ProfileID != firstProfile {
			t.Fatalf("desktop outside selected profile: %s", desktop.ProfileID)
		}
	}
	next := w.Spawn(otherApp, fakeagent.Claude, w.Path("next"))
	w.Launched(next)
	nextTerminal := protocol.TerminalID(otherApp.Terminal(next))
	testworld.Await(observer, protocol.EventTerminalBindingsUpdated, func(m protocol.TerminalBindingsUpdatedMessage) bool {
		for _, b := range m.TerminalBindings {
			if b.TerminalID == nextTerminal && b.SessionID == protocol.SessionID(next) {
				return true
			}
		}
		return false
	})
	closeSession(t, w.Client(), next, "finished")
	testworld.Await(observer, protocol.EventTerminalBindingsUpdated, func(m protocol.TerminalBindingsUpdatedMessage) bool {
		for _, b := range m.TerminalBindings {
			if b.TerminalID == nextTerminal {
				return false
			}
		}
		return true
	})
}

func TestAPluginCloseInterruptedAfterItsPaneDisappearsRecoversOnRestart(t *testing.T) {
	s := testworld.NewStack(t)
	s.StartCrashingAt("session-close-before-driver-notification")
	app := s.App()
	driver := dialDriverAt(t, s.DialUnix, "snipe-plugin")
	driver.register("snipe", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	session := s.Spawn(app, scriptedAgent, s.Path("shop"))
	run := driver.launched()
	if run.SessionID == session {
		t.Fatal("fixture did not create distinct terminal and session identities")
	}
	app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(session)})
	s.AwaitCrash()
	s.Start()
	closed := showSession(t, s.Client(), session)
	if protocol.Deref(closed.ClosedAt) == "" {
		t.Fatal("restart lost the committed close")
	}
}

func TestUnplacedAgentsKeepTheirTerminalAcrossPlacementAndRestart(t *testing.T) {
	for _, action := range []string{"show", "move", "place"} {
		t.Run(action, func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			s.Start()
			app := s.App()
			session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
			run := s.Launched(session)
			terminal := protocol.TerminalID(app.Terminal(session))
			profile := app.SelectedProfile()
			view := viewProfile(t, &world{World: s.World}, profile)
			desktop, pane := view.paneOf(t, session)
			request := uuid.NewString()
			mustProfileRequest(app, protocol.DesktopRemoveLeafMessage{Cmd: protocol.CmdDesktopRemoveLeaf, RequestID: request, DesktopID: desktop.ID, LeafID: pane}, request)
			_, caller, err := s.Client().QueryAs(terminal)
			if err != nil || caller != protocol.SessionID(session) {
				t.Fatalf("unplaced caller %s: %v", caller, err)
			}
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: "/clear\r"})
			if got := run.Prompted(); got != "/clear" {
				t.Fatalf("prompt %q", got)
			}
			successor := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
				return e.Session != nil && string(protocol.Deref(e.Session.Succeeds)) == session
			}).Session
			session = string(successor.ID)
			s.Stop()
			s.Start()
			app = s.App()
			_, caller, err = s.Client().QueryAs(terminal)
			if err != nil || caller != successor.ID {
				t.Fatalf("recovered caller %s: %v", caller, err)
			}
			switch action {
			case "show":
				if result := requestShowSession(app, session); !result.Success {
					t.Fatal(protocol.Deref(result.Error))
				}
			case "move":
				if _, err := s.Client().MoveSessionToDesktop("", successor.ID, desktop.ID); err != nil {
					t.Fatal(err)
				}
			case "place":

				request := uuid.NewString()
				mustProfileRequest(app, protocol.DesktopPlaceSessionMessage{Cmd: protocol.CmdDesktopPlaceSession, RequestID: request, DesktopID: desktop.ID, SessionID: successor.ID}, request)
			}
			if got := app.Terminal(session); got != string(terminal) {
				t.Fatalf("placement changed terminal from %s to %s", terminal, got)
			}
			app.TypeLine(session, "same process")
			if got := run.Prompted(); got != "same process" {
				t.Fatalf("recovered prompt %q", got)
			}
			current, leaf := viewProfile(t, &world{World: s.World}, profile).paneOf(t, session)
			request = uuid.NewString()
			mustProfileRequest(app, protocol.DesktopRemoveLeafMessage{Cmd: protocol.CmdDesktopRemoveLeaf, RequestID: request, DesktopID: current.ID, LeafID: leaf}, request)
			closeSession(t, s.Client(), session, "done while unplaced")
			testworld.Await(app, protocol.EventTerminalBindingsUpdated, func(m protocol.TerminalBindingsUpdatedMessage) bool {
				for _, binding := range m.TerminalBindings {
					if binding.TerminalID == terminal {
						return false
					}
				}
				return true
			})
			s.Stop()
			s.Start()
			_, caller, err = s.Client().QueryAs(terminal)
			if err != nil || caller != "" {
				t.Fatalf("closed terminal still maps to %s: %v", caller, err)
			}

		})
	}
}
