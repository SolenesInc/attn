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
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	takeTurn(app, codex, session)
	conversation := codex.ConversationID
	codex.Exit(143)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
	stopped := w.CodexServer()
	w.restart()
	app = w.App()
	stopped.AwaitStopped()

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

func appCommandAnswers(app *testworld.Peer) {
	app.T.Helper()
	ledgerListOverTheWebSocket(app, uuid.NewString(), protocol.SessionListMessage{})
}
