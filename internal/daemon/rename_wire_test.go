package daemon_test

import (
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
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.SessionID == session })
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

func TestARenamedDesktopKeepsItsNameAcrossARestart(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	spawned, desktopID, _ := w.RequestSpawn(app, fakeagent.Claude, w.Path("shop"))
	if !spawned.Success {
		t.Fatalf("spawn: %s", protocol.Deref(spawned.Error))
	}
	desktop := desktopOfDelegate(t, w, desktopID)

	renamed := testworld.Request(app, protocol.DesktopRenameMessage{
		Cmd: protocol.CmdDesktopRename, DesktopID: desktop.ID, Name: "User Renamed", ExpectedRevision: desktop.Revision, RequestID: "rename",
	}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "rename" })
	if !renamed.Success {
		t.Fatalf("rename desktop: %s", protocol.Deref(renamed.Error))
	}

	w.restart()
	if got := desktopOfDelegate(t, w, desktop.ID); got.Name != "User Renamed" {
		t.Errorf("desktop after a restart = %+v, want it named User Renamed", got)
	}
}
