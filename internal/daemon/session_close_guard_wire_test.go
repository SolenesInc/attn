package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheChiefAndCrewMembersCannotBeClosedButTheirNeighboursCan(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	shared := w.Path("shared")

	panes := spawnPanes(w, app, shared, shared, shared)
	chief, worker, unregistered := panes[0], panes[1], panes[2]
	if made := setChiefOfStaff(app, chief.session, true); !made.Success {
		t.Fatalf("make %s the chief: %s", chief.session, protocol.Deref(made.Error))
	}
	woken := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(woken.SessionID))
	crewPane := sessionPane{session: string(woken.SessionID)}

	protected := []struct {
		pane    sessionPane
		refusal string
	}{
		{chief, "chief of staff is protected from closing; unset the chief role first"},
		{crewPane, "Trellis is protected from closing; put Trellis to sleep first"},
	}
	for _, p := range protected {
		app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(p.pane.session)})
		if refused := testworld.Refused(app); protocol.Deref(refused.Cmd) != protocol.CmdUnregister || !strings.Contains(protocol.Deref(refused.Error), p.refusal) {
			t.Errorf("unregister %s refused with %s: %q, want %q", p.pane.session, protocol.Deref(refused.Cmd), protocol.Deref(refused.Error), p.refusal)
		}
		desktop, tile := placedPane(t, w, p.pane.session)
		if refused := closeTileFromApp(app, desktop.ID, tile); refused.Success || !strings.Contains(protocol.Deref(refused.Error), p.refusal) {
			t.Errorf("closing the tile of %s answered success=%v %q, want the refusal %q", p.pane.session, refused.Success, protocol.Deref(refused.Error), p.refusal)
		}
	}

	app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(unregistered.session)})
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(e.Session.ID) == unregistered.session
	})
	if closed := closeTileFromApp(app, worker.desktop, worker.pane); !closed.Success {
		t.Fatalf("close the tile of %s: %s", worker.session, protocol.Deref(closed.Error))
	}
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(e.Session.ID) == worker.session
	})

	view := w.App().Initial
	live := map[string]protocol.Session{}
	for _, s := range view.Sessions {
		live[string(s.ID)] = s
	}
	if !protocol.Deref(live[chief.session].ChiefOfStaff) {
		t.Errorf("after the refused closes %s is no longer a live chief: %+v", chief.session, live[chief.session])
	}
	if got := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession); got != woken.SessionID {
		t.Errorf("trellis is bound to %q after the refused closes, want %s", got, woken.SessionID)
	}
	if _, ok := live[string(woken.SessionID)]; !ok {
		t.Errorf("after the refused closes trellis's session %s is no longer live", woken.SessionID)
	}
	for _, gone := range []string{worker.session, unregistered.session} {
		if _, ok := live[gone]; ok {
			t.Errorf("%s is still live after it was closed", gone)
		}
	}
	if !slices.ContainsFunc(view.Desktops, func(desktop protocol.Desktop) bool {
		return desktop.ID == chief.desktop && slices.ContainsFunc(desktop.Panes, func(pane protocol.DesktopPane) bool { return pane.PaneID == chief.pane })
	}) {
		t.Errorf("the refused close removed pane %s of %s from its desktop", chief.pane, chief.session)
	}
	if slices.ContainsFunc(view.Desktops, func(desktop protocol.Desktop) bool {
		return slices.ContainsFunc(desktop.Panes, func(pane protocol.DesktopPane) bool { return pane.PaneID == worker.pane })
	}) {
		t.Errorf("closing the tile of %s left it on its desktop", worker.session)
	}
}
