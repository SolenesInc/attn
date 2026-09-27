package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAPluginSessionWhoseAgentDiedWithTheDaemonComesBackResumable(t *testing.T) {
	w := newWorld(t, fakeagent.Pi)
	app := w.App()
	pluginDriverSettings(app, "pi")
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Pi, cwd)
	first := w.Launched(session)
	converse(t, app, session, first, "price the checkout")

	w.restart()
	app = w.App()
	came := protocol.Session{}
	for _, s := range app.Initial.Sessions {
		if s.ID == session {
			came = s
		}
	}
	if came.State != protocol.SessionStateRecoverable {
		t.Fatalf("after its agent died with the daemon the pi session is %q, want recoverable", came.State)
	}
	pluginDriverSettings(app, "pi")
	w.Spawn(app, fakeagent.Pi, cwd, func(m *protocol.SpawnSessionMessage) { m.ID = session })
	if resumed := w.Launched(session); !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("reopening the pi session resumed=%v conversation %s, want %s", resumed.Resumed, resumed.ConversationID, first.ConversationID)
	}
}
