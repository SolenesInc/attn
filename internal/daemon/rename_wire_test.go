package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func renameFromApp(app *testworld.Peer, cmd any, id string) protocol.RenameResultMessage {
	app.T.Helper()
	return testworld.Request(app, cmd, protocol.EventRenameResult, func(r protocol.RenameResultMessage) bool { return r.ID == id })
}

func TestARenamedSessionKeepsItsNameAcrossRespawn(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Claude, cwd, func(m *protocol.SpawnSessionMessage) { m.Label = protocol.Ptr("original") })
	w.Launched(session)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.Label == "original" })

	blank := renameFromApp(app, protocol.RenameSessionMessage{Cmd: protocol.CmdRenameSession, SessionID: session, Label: "   "}, session)
	if blank.Success {
		t.Error("renaming the session to a blank name was accepted")
	}
	if err := cli.RenameSession(session, strings.Repeat("x", 49)); err == nil || !strings.Contains(err.Error(), "over the 48-character limit") {
		t.Errorf("renaming to 49 characters over the CLI = %v, want a refusal naming the 48-character limit", err)
	}
	if label := queriedSession(t, cli, session).Label; label != "original" {
		t.Errorf("label after the refused renames = %q, want original", label)
	}

	if err := cli.RenameSession(session, "  review store tripwires  "); err != nil {
		t.Fatalf("rename over the CLI: %v", err)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.Label == "review store tripwires" })

	renamed := renameFromApp(app, protocol.RenameSessionMessage{Cmd: protocol.CmdRenameSession, SessionID: session, Label: "renamed"}, session)
	if !renamed.Success {
		t.Fatalf("rename from the app: %s", protocol.Deref(renamed.Error))
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.Label == "renamed" })

	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: session})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
	w.Spawn(app, fakeagent.Claude, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = session
		m.ResumeSessionID = protocol.Ptr(session)
		m.Label = protocol.Ptr("original")
	})
	w.Launched(session)
	if label := queriedSession(t, cli, session).Label; label != "renamed" {
		t.Errorf("label after a respawn carrying the original label = %q, want the user's name renamed", label)
	}
}

func TestARenamedWorkspaceKeepsItsTitle(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	spawned, workspace, _ := w.RequestSpawn(app, fakeagent.Claude, cwd)
	if !spawned.Success {
		t.Fatalf("spawn: %s", protocol.Deref(spawned.Error))
	}
	session := spawned.ID
	run := w.Launched(session)
	app.TypeLine(session, "add a discount field")
	run.Prompted()
	run.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	register := protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: workspace, Title: "Storefront", Directory: cwd,
	}

	renamed := renameFromApp(app, protocol.RenameWorkspaceMessage{Cmd: protocol.CmdRenameWorkspace, WorkspaceID: register.ID, Title: "User Renamed"}, register.ID)
	if !renamed.Success {
		t.Fatalf("rename workspace: %s", protocol.Deref(renamed.Error))
	}
	testworld.Await(app, protocol.EventWorkspaceStateChanged, func(e protocol.WorkspaceStateChangedMessage) bool {
		return e.Workspace.ID == register.ID && e.Workspace.Title == "User Renamed"
	})

	w.restart()
	app = w.App()
	if !slices.ContainsFunc(app.Initial.Workspaces, func(ws protocol.Workspace) bool { return ws.ID == register.ID && ws.Title == "User Renamed" }) {
		t.Errorf("workspaces after a restart = %+v, want %s titled User Renamed", app.Initial.Workspaces, register.ID)
	}

	app.Send(register)
	reregistered := testworld.Await(app, protocol.EventWorkspaceStateChanged, func(e protocol.WorkspaceStateChangedMessage) bool {
		return e.Workspace.ID == register.ID
	})
	if reregistered.Workspace.Title != "User Renamed" {
		t.Errorf("title after the app re-registered the workspace as %q = %q, want User Renamed", register.Title, reregistered.Workspace.Title)
	}
}
