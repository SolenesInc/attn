package daemon_test

import (
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheAppsOtherCommandsAnswerWhileASpawnStartsItsSharedCodexAppServer(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	cwd := w.Path("shop")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	w.SetCodexFolderTrust(cwd, "trusted")
	spawn := protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: protocol.SessionID(uuid.NewString()), Agent: string(fakeagent.Codex),
		Cwd: cwd, ProfileID: app.SelectedProfile(), Placement: &protocol.SessionPlacement{}, Cols: 100, Rows: 30,
	}

	boot := w.HoldNextBoot()
	app.Send(spawn)
	w.AwaitHeldBoot()
	appCommandAnswers(app)
	boot()
	if result := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == spawn.ID }); !result.Success {
		t.Fatalf("the spawn that waited for its app-server failed: %s", protocol.Deref(result.Error))
	}
	codex := w.Launched(string(spawn.ID))
	app.TypeLine(string(spawn.ID), "find the flaky checkout test")
	if got := codex.Prompted(); got != "find the flaky checkout test" {
		t.Fatalf("codex received %q", got)
	}
	if closed := closeFromApp(app, string(spawn.ID)); !closed.Accepted {
		t.Fatalf("close %s: %s", spawn.ID, protocol.Deref(closed.Error))
	}
}

func TestTheAppsOtherCommandsAnswerWhileAResumeStartsItsSharedCodexAppServer(t *testing.T) {
	w, app, session, conversation := stoppedSharedCodexSessionWithNoAppServer(t)

	boot := w.HoldNextBoot()
	app.Send(protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30})
	w.AwaitHeldBoot()
	appCommandAnswers(app)
	boot()
	if result := testworld.Await(app, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session }); !result.Success {
		t.Fatalf("the resume that waited for its app-server failed: %s", protocol.Deref(result.Error))
	}
	resumed := w.Launched(session)
	if !resumed.Resumed || resumed.ConversationID != conversation {
		t.Fatalf("the resume ran %q showing %s (resumed=%v), want conversation %s", resumed.Argv, resumed.ConversationID, resumed.Resumed, conversation)
	}
	if closed := closeFromApp(app, session); !closed.Accepted {
		t.Fatalf("close %s: %s", session, protocol.Deref(closed.Error))
	}
}

func TestClosingASessionAnswersWhileItsResumeWaitsForItsSharedCodexAppServer(t *testing.T) {
	for _, way := range []string{"unregister", "desktop_close_tile"} {
		t.Run(way, func(t *testing.T) {
			w, app, session, _ := stoppedSharedCodexSessionWithNoAppServer(t)
			desktop, pane := placedPane(t, w, session)

			boot := w.HoldNextBoot()
			app.Send(protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30})
			w.AwaitHeldBoot()
			if way == "unregister" {
				if closed := closeFromApp(app, session); !closed.Accepted {
					t.Fatalf("close %s while its resume waits: %s", session, protocol.Deref(closed.Error))
				}
			} else if closed := closeTileFromApp(app, desktop.ID, pane); !closed.Success {
				t.Fatalf("close the tile of %s while its resume waits: %s", session, protocol.Deref(closed.Error))
			}
			appCommandAnswers(app)
			boot()
			if result := testworld.Await(app, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session }); result.Success {
				t.Fatal("the resume of a session closed while it waited succeeded")
			}

			after := w.ConnectApp().Initial
			for _, s := range after.Sessions {
				if string(s.ID) == session {
					t.Fatalf("the session closed while its resume waited is open again: %+v", s)
				}
			}
			for _, d := range after.Desktops {
				for _, p := range d.Panes {
					if string(p.SessionID) == session {
						t.Fatalf("the session closed while its resume waited is placed again on desktop %s", d.ID)
					}
				}
			}
		})
	}
}

func TestReopeningASessionClosedWhileItsResumeWaitedKeepsItsConversationLive(t *testing.T) {
	w, app, session, conversation := stoppedSharedCodexSessionWithNoAppServer(t)
	boot := w.HoldNextBoot()
	app.Send(protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30})
	w.AwaitHeldBoot()
	if closed := closeFromApp(app, session); !closed.Accepted {
		t.Fatalf("close %s: %s", session, protocol.Deref(closed.Error))
	}

	reopen := uuid.NewString()
	app.Send(protocol.SessionReopenMessage{Cmd: protocol.CmdSessionReopen, RequestID: protocol.Ptr(reopen), SessionID: protocol.SessionID(session)})
	boot()
	if reopened := testworld.Await(app, protocol.EventSessionReopenResult, func(r protocol.SessionReopenResultMessage) bool { return r.RequestID == reopen }); !reopened.Success {
		t.Fatalf("reopen %s after its close: %s", session, protocol.Deref(reopened.Error))
	}
	resumed := w.Launched(session)
	if !resumed.Resumed || resumed.ConversationID != conversation {
		t.Fatalf("the reopen ran %q showing %s (resumed=%v), want conversation %s", resumed.Argv, resumed.ConversationID, resumed.Resumed, conversation)
	}
	app.TypeLine(session, "pick up where we left off")
	if got := resumed.Prompted(); got != "pick up where we left off" {
		t.Fatalf("the reopened Codex received %q", got)
	}
	if archivedInCodex(t, w, conversation) {
		t.Errorf("conversation %s of the reopened session is archived", conversation)
	}
	if closed := closeFromApp(app, session); !closed.Accepted {
		t.Fatalf("close %s: %s", session, protocol.Deref(closed.Error))
	}
}

func stoppedSharedCodexSessionWithNoAppServer(t *testing.T) (w *world, app *testworld.Peer, session, conversation string) {
	t.Helper()
	w = newWorld(t, fakeagent.Codex)
	app = w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	session = w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	takeTurn(app, codex, session)
	codex.Exit(143)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
	stopped := w.CodexServer()
	w.restart()
	stopped.AwaitStopped()
	return w, w.App(), session, codex.ConversationID
}

func appCommandAnswers(app *testworld.Peer) {
	app.T.Helper()
	ledgerListOverTheWebSocket(app, uuid.NewString(), protocol.SessionListMessage{})
}
