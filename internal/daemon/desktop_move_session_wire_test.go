package daemon_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASessionMovesItselfAndItsDelegatesBetweenDesktops(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := w.Path("notes")
	profile := app.SelectedProfile()
	s, home, _ := w.RequestSpawn(app, fakeagent.Codex, cwd)
	tt, _, tPane := w.RequestSpawn(app, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("second")
		m.Placement = &protocol.SessionPlacement{DesktopID: protocol.Ptr(home)}
	})
	id := uuid.NewString()
	ops := mustProfileRequest(app, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, RequestID: id, ProfileID: profile, Name: protocol.Ptr("Ops")}, id).Desktops[0]
	id = uuid.NewString()
	web := mustProfileRequest(app, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, RequestID: id, ProfileID: profile, Name: protocol.Ptr("Web")}, id).Desktops[0]

	arrangement := func(when string, holds func(map[string]protocol.Desktop) bool) (protocol.ProfileArrangementChangedMessage, map[string]protocol.Desktop) {
		t.Helper()
		var byID map[string]protocol.Desktop
		got := testworld.Await(app, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
			byID = map[string]protocol.Desktop{}
			for _, desktop := range e.Desktops {
				byID[desktop.ID] = desktop
			}
			return holds(byID)
		})
		if got.Profile.CurrentDesktopID != home {
			t.Errorf("%s: the profile now shows desktop %s; want %s, where the user was", when, got.Profile.CurrentDesktopID, home)
		}
		return got, byID
	}
	holding := func(desktop protocol.Desktop, session string) bool {
		for _, pane := range desktop.Panes {
			if pane.SessionID == session {
				return true
			}
		}
		return false
	}
	move := func(caller, session, ref string) *protocol.DesktopMoveSessionResult {
		t.Helper()
		result, err := cli.MoveSessionToDesktop(caller, session, ref)
		if err != nil {
			t.Fatalf("moving %s to %q as %q: %v", session, ref, caller, err)
		}
		return result
	}

	moved := move(s.ID, s.ID, fmt.Sprint(protocol.Deref(ops.ShortcutSlot)))
	if moved.DesktopID != ops.ID || protocol.Deref(moved.FromDesktopID) != home {
		t.Errorf("by digit, the move reports %+v; want from %s to Ops %s", moved, home, ops.ID)
	}
	event, desktops := arrangement("a session moving itself onto an empty desktop", func(d map[string]protocol.Desktop) bool { return holding(d[ops.ID], s.ID) })
	if event.MovedLeaf == nil {
		event = testworld.Await(app, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool { return e.MovedLeaf != nil })
	}
	if desktops[ops.ID].ActivePaneID != moved.PaneID {
		t.Errorf("the empty desktop's active pane is %q; want its first pane %s", desktops[ops.ID].ActivePaneID, moved.PaneID)
	}
	if desktops[home].ActivePaneID != tPane {
		t.Errorf("the desktop the user sees now shows %q; want %s, unchanged", desktops[home].ActivePaneID, tPane)
	}
	if m := event.MovedLeaf; m == nil || m.FromDesktopID != home || m.ToDesktopID != ops.ID || m.ToLeafID != moved.PaneID {
		t.Errorf("the broadcast names the moved leaf %+v; want %s to %s as %s", m, home, ops.ID, moved.PaneID)
	}

	moved = move(tt.ID, tt.ID, "oPs")
	_, desktops = arrangement("the shown pane moving itself by name", func(d map[string]protocol.Desktop) bool { return holding(d[ops.ID], tt.ID) })
	if desktops[ops.ID].ActivePaneID != moved.PaneID {
		t.Errorf("the pane that was shown moved, but Ops shows %q; want it, %s", desktops[ops.ID].ActivePaneID, moved.PaneID)
	}

	move(s.ID, s.ID, web.ID)
	arrangement("a session moving itself by id", func(d map[string]protocol.Desktop) bool { return holding(d[web.ID], s.ID) })

	request := brief(cwd, "Watch the deploy")
	request.Agent, request.SourceSessionID, request.Label, request.Desktop = protocol.Ptr("codex"), protocol.Ptr(s.ID), protocol.Ptr("watcher"), protocol.Ptr("web")
	delegate, err := cli.Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	if got := move(s.ID, delegate.SessionID, "web"); !protocol.Deref(got.Unchanged) {
		t.Errorf("moving a delegate onto the desktop it is on reports %+v; want unchanged", got)
	}
	_, err = cli.MoveSessionToDesktop(tt.ID, delegate.SessionID, "ops")
	if err == nil || !strings.Contains(err.Error(), "may move itself, the sessions it dispatched") {
		t.Errorf("a session moving another's delegate was answered %v; want a refusal naming who may move it", err)
	}
	move(s.ID, delegate.SessionID, "ops")
	_, desktops = arrangement("a dispatcher moving its delegate", func(d map[string]protocol.Desktop) bool { return holding(d[ops.ID], delegate.SessionID) })
	if desktops[ops.ID].ActivePaneID == protocol.Deref(delegate.PaneID) || !holding(desktops[ops.ID], tt.ID) {
		t.Errorf("a pane that was not shown moved onto Ops and took its active pane %q", desktops[ops.ID].ActivePaneID)
	}

	unplaced := brief(cwd, "Tidy the notes")
	unplaced.Agent, unplaced.Label = protocol.Ptr("codex"), protocol.Ptr("tidier")
	loose, err := cli.Delegate(unplaced)
	if err != nil {
		t.Fatal(err)
	}
	move("", loose.SessionID, "3")
	arrangement("the user placing an unplaced session", func(d map[string]protocol.Desktop) bool { return holding(d[web.ID], loose.SessionID) })

	residentSession, _, resident := w.RequestSpawn(app, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("resident")
		m.Placement = &protocol.SessionPlacement{DesktopID: protocol.Ptr(home)}
	})
	move(s.ID, s.ID, "1")
	_, desktops = arrangement("the shown pane of a background desktop moving onto the one the user sees", func(d map[string]protocol.Desktop) bool {
		return holding(d[home], s.ID) && holding(d[home], residentSession.ID)
	})
	if desktops[home].ActivePaneID != resident {
		t.Errorf("a pane arriving on the desktop the user sees took its active pane %q; want %s, unchanged", desktops[home].ActivePaneID, resident)
	}

	_, err = cli.MoveSessionToDesktop(s.ID, s.ID, "nope")
	if err == nil || !strings.Contains(err.Error(), `unknown desktop "nope"`) || !strings.Contains(err.Error(), "2 Ops") {
		t.Errorf("an unknown desktop was answered %v; want a refusal listing the desktops", err)
	}
}
