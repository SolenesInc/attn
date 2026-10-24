package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestACodexSessionWithoutHooksStopsLookingForAnUnboundTranscript(t *testing.T) {
	t.Setenv("ATTN_AGENT_CODEX_HOOKS", "0")
	w := newWorld(t, fakeagent.Codex)
	app := w.App()

	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	w.Launched(session)

	if window := messageWindow(app, session); window.Status != protocol.SessionMessageWindowStatusUnavailable {
		t.Fatalf("message window %s, want unavailable once no exact transcript can be bound", window.Status)
	}
}

func messageWindow(app *testworld.Peer, session string) protocol.SessionMessagesGetResultMessage {
	app.T.Helper()
	testworld.Await(app, protocol.EventSessionMessagesChanged, func(e protocol.SessionMessagesChangedMessage) bool { return string(e.SessionID) == session })
	return testworld.Request(app, protocol.SessionMessagesGetMessage{
		Cmd: protocol.CmdSessionMessagesGet, RequestID: "window-" + session, SessionID: protocol.SessionID(session),
	}, protocol.EventSessionMessagesGetResult, func(r protocol.SessionMessagesGetResultMessage) bool { return r.RequestID == "window-"+session })
}
