package daemon_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func setChiefOfStaff(app *testworld.Peer, sessionID string, chief bool) protocol.ChiefOfStaffResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.SetChiefOfStaffMessage{Cmd: protocol.CmdSetChiefOfStaff, SessionID: sessionID, ChiefOfStaff: chief},
		protocol.EventChiefOfStaffResult, func(m protocol.ChiefOfStaffResultMessage) bool { return m.SessionID == sessionID })
}

func chiefsOf(w *world) []string {
	var chiefs []string
	for _, session := range w.App().Initial.Sessions {
		if protocol.Deref(session.ChiefOfStaff) {
			chiefs = append(chiefs, session.ID)
		}
	}
	return chiefs
}

func TestChiefOfStaffIsASingleRoleThatTransfers(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	registerSessions(t, w, w.Client(), "first", "second")

	if first := setChiefOfStaff(app, "first", true); !first.Success || first.PreviousSessionID != nil {
		t.Fatalf("making first the chief = %+v", first)
	}
	testworld.AwaitSession(app, "first", func(s protocol.Session) bool { return protocol.Deref(s.ChiefOfStaff) })

	transfer := setChiefOfStaff(app, "second", true)
	if !transfer.Success || protocol.Deref(transfer.PreviousSessionID) != "first" {
		t.Fatalf("making second the chief = %+v; want a transfer from first", transfer)
	}
	testworld.AwaitSession(app, "second", func(s protocol.Session) bool { return protocol.Deref(s.ChiefOfStaff) })
	testworld.AwaitSession(app, "first", func(s protocol.Session) bool { return !protocol.Deref(s.ChiefOfStaff) })

	if unknown := setChiefOfStaff(app, "missing", true); unknown.Success || protocol.Deref(unknown.Error) == "" {
		t.Errorf("making an unknown session the chief = %+v; want a refusal", unknown)
	}
	if staleClear := setChiefOfStaff(app, "first", false); !staleClear.Success {
		t.Errorf("clearing the role from a session that no longer holds it = %+v", staleClear)
	}
	if chiefs := chiefsOf(w); len(chiefs) != 1 || chiefs[0] != "second" {
		t.Fatalf("the chiefs are %v; want only second", chiefs)
	}
}

func TestCreatingASessionAsChiefAssignsTheRoleOnlyWhenItCan(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	asChief := func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) }

	shell := w.Spawn(app, fakeagent.Harness(protocol.AgentShellValue), w.Path("shell"), asChief)
	app.TypeLine(shell, "exit")
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == shell })

	respawned := w.Spawn(app, fakeagent.Claude, w.Path("plain"))
	w.Launched(respawned)
	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: respawned})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == respawned })
	w.Spawn(app, fakeagent.Claude, w.Path("plain"), asChief, func(m *protocol.SpawnSessionMessage) { m.ID = respawned })
	w.Launched(respawned)

	failed := uuid.NewString()
	missingDirectory := w.Path("gone")
	if result := testworld.Request(app, protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: failed, Agent: string(fakeagent.Claude), Cwd: missingDirectory, WorkspaceID: "workspace-plain",
		Cols: 100, Rows: 30, ChiefOfStaff: protocol.Ptr(true),
	}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == failed }); result.Success {
		t.Fatalf("a spawn into missing directory %s succeeded", missingDirectory)
	}

	chief := w.Spawn(app, fakeagent.Claude, w.Path("chief"), asChief)
	w.Launched(chief)
	second := w.Spawn(app, fakeagent.Claude, w.Path("second"), asChief)
	w.Launched(second)

	if chiefs := chiefsOf(w); len(chiefs) != 1 || chiefs[0] != chief {
		t.Fatalf("the chiefs are %v; want only %s, the first new agent created as chief after the failed spawn", chiefs, chief)
	}
}
