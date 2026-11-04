package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAPluginSessionWhoseAgentDiedWithTheDaemonComesBackResumable(t *testing.T) {
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	pluginDriverSettings(app, "plug")
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Plug, cwd)
	first := w.Launched(session)
	converse(t, app, session, first, "price the checkout")

	w.restartHoldingRevives()
	app = w.App()
	came := protocol.Session{}
	for _, s := range app.Initial.Sessions {
		if string(s.ID) == session {
			came = s
		}
	}
	if came.State != protocol.SessionStateRecoverable {
		t.Fatalf("after its agent died with the daemon the plug session is %q, want recoverable", came.State)
	}
	pluginDriverSettings(app, "plug")
	w.Spawn(app, fakeagent.Plug, cwd, func(m *protocol.SpawnSessionMessage) { m.ID = protocol.SessionID(session) })
	if resumed := w.Launched(session); !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("reopening the plug session resumed=%v conversation %s, want %s", resumed.Resumed, resumed.ConversationID, first.ConversationID)
	}
}

func TestAPlugAgentComesBackOnItsOwnOnceItsDriverRegistersAfterARestart(t *testing.T) {
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	pluginDriverSettings(app, "plug")
	session := w.Spawn(app, fakeagent.Plug, w.Path("shop"))
	first := w.Launched(session)
	converse(t, app, session, first, "price the checkout")

	w.restart()
	if resumed := w.Launched(session); !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("the plug session came back resumed=%v conversation %s, want %s", resumed.Resumed, resumed.ConversationID, first.ConversationID)
	}
}
