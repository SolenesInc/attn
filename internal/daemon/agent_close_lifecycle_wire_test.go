package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestClosingAnAgentKeepsItsNeighbourAndRejectsALateSpawn(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	gone, desktop, _ := w.RequestSpawn(app, fakeagent.Claude, w.Path("gone"))
	if !gone.Success {
		t.Fatalf("spawn the agent to close: %s", protocol.Deref(gone.Error))
	}
	live := w.Spawn(app, fakeagent.Claude, w.Path("live"), func(m *protocol.SpawnSessionMessage) {
		m.Placement.DesktopID = protocol.Ptr(desktop)
	})
	w.Launched(string(gone.ID))
	w.Launched(live)
	if closed := closeFromApp(app, string(gone.ID)); !closed.Accepted {
		t.Fatalf("close the agent: %s", protocol.Deref(closed.Error))
	}
	atClose := awaitClosed(app, string(gone.ID))
	remaining, _ := viewProfile(t, w, app.SelectedProfile()).paneOf(t, live)
	if remaining.ID != desktop || len(remaining.Panes) != 1 || string(remaining.Panes[0].SessionID) != live || remaining.ActivePaneID != remaining.Panes[0].PaneID {
		t.Fatalf("desktop after close = %+v, want its neighbour kept and active", remaining)
	}
	late := testworld.Request(app, protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: gone.ID, Agent: protocol.AgentShellValue,
		Cwd: w.Path("gone"), ProfileID: app.SelectedProfile(), Placement: &protocol.SessionPlacement{}, Cols: 80, Rows: 24,
	}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == gone.ID })
	if late.Success {
		t.Fatal("a late spawn recreated the closed agent")
	}
	after := showSession(t, w.Client(), string(gone.ID))
	if protocol.Deref(after.ClosedAt) != protocol.Deref(atClose.ClosedAt) {
		t.Fatalf("late spawn changed the recorded close: before=%+v after=%+v", atClose, after)
	}
}
