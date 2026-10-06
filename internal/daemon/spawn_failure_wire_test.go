package daemon_test

import (
	"os"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASpawnWhoseAgentCannotStartRegistersNothing(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	failed, _, _ := w.RequestSpawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Cwd = w.Path("shop", "deleted")
	})
	if failed.Success {
		t.Fatal("a spawn into a deleted directory succeeded")
	}
	if sessions := w.App().Initial.Sessions; len(sessions) != 0 {
		t.Errorf("the spawn whose agent could not start left sessions %+v", sessions)
	}
}

func TestARespawnWhoseAgentCannotStartKeepsTheSessionAndItsLaunch(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Claude, cwd, func(m *protocol.SpawnSessionMessage) {
		m.Model = protocol.Ptr("claude-sonnet-5")
	})
	w.Launched(session)
	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: protocol.SessionID(session)})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })

	if err := os.RemoveAll(cwd); err != nil {
		t.Fatal(err)
	}
	failed := testworld.Request(app, protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: protocol.SessionID(session), Agent: string(fakeagent.Claude), Cwd: cwd,
		ProfileID: app.SelectedProfile(), Cols: 100, Rows: 30, Model: protocol.Ptr("claude-haiku-5"),
	}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return string(r.ID) == session })
	if failed.Success {
		t.Fatal("a respawn into a deleted directory succeeded")
	}
	if !slices.ContainsFunc(w.App().Initial.Sessions, func(s protocol.Session) bool { return string(s.ID) == session }) {
		t.Fatal("the failed respawn took the session away")
	}

	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	launchIntentReload(t, app, session)
	if model, _ := flagValue(w.Launched(session).Argv, "--model"); model != "claude-sonnet-5" {
		t.Errorf("after the failed respawn claude relaunched on model %q, want the claude-sonnet-5 it had", model)
	}
}
