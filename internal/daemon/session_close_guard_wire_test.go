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
	w.Launched(woken.SessionID)
	layout := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		return slices.ContainsFunc(e.WorkspaceLayout.Panes, func(p protocol.WorkspaceLayoutPane) bool { return protocol.Deref(p.SessionID) == woken.SessionID })
	})
	crewPane := sessionPane{session: woken.SessionID, workspace: woken.WorkspaceID}
	for _, p := range layout.WorkspaceLayout.Panes {
		if protocol.Deref(p.SessionID) == woken.SessionID {
			crewPane.pane = p.PaneID
		}
	}

	protected := []struct {
		pane    sessionPane
		refusal string
	}{
		{chief, "chief of staff is protected from closing; unset the chief role first"},
		{crewPane, "Trellis is protected from closing; put Trellis to sleep first"},
	}
	for _, p := range protected {
		app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: p.pane.session})
		if refused := testworld.Refused(app); protocol.Deref(refused.Cmd) != protocol.CmdUnregister || !strings.Contains(protocol.Deref(refused.Error), p.refusal) {
			t.Errorf("unregister %s refused with %s: %q, want %q", p.pane.session, protocol.Deref(refused.Cmd), protocol.Deref(refused.Error), p.refusal)
		}
		closed := testworld.Request(app, protocol.WorkspaceLayoutClosePaneMessage{
			Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: p.pane.workspace, PaneID: p.pane.pane,
		}, protocol.EventWorkspaceLayoutActionResult, func(r protocol.WorkspaceLayoutActionResultMessage) bool {
			return r.Action == protocol.CmdWorkspaceLayoutClosePane && protocol.Deref(r.PaneID) == p.pane.pane
		})
		if closed.Success {
			t.Errorf("closing the pane of %s was accepted", p.pane.session)
		}
	}

	app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: unregistered.session})
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && e.Session.ID == unregistered.session
	})
	closePane(app, worker)
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && e.Session.ID == worker.session
	})

	view := w.App().Initial
	live := map[string]protocol.Session{}
	for _, s := range view.Sessions {
		live[s.ID] = s
	}
	if !protocol.Deref(live[chief.session].ChiefOfStaff) {
		t.Errorf("after the refused closes %s is no longer a live chief: %+v", chief.session, live[chief.session])
	}
	if got := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession); got != woken.SessionID {
		t.Errorf("trellis is bound to %q after the refused closes, want %s", got, woken.SessionID)
	}
	if _, ok := live[woken.SessionID]; !ok {
		t.Errorf("after the refused closes trellis's session %s is no longer live", woken.SessionID)
	}
	for _, gone := range []string{worker.session, unregistered.session} {
		if _, ok := live[gone]; ok {
			t.Errorf("%s is still live after it was closed", gone)
		}
	}
	for _, p := range []sessionPane{chief, crewPane} {
		if !slices.ContainsFunc(view.Workspaces, func(ws protocol.Workspace) bool {
			return ws.ID == p.workspace && ws.Layout != nil && slices.ContainsFunc(ws.Layout.Panes, func(pane protocol.WorkspaceLayoutPane) bool { return pane.PaneID == p.pane })
		}) {
			t.Errorf("the refused close removed pane %s of %s from its layout", p.pane, p.session)
		}
	}
}
