package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestCodexRespawnResumesTheConversationItsSessionStartHookReported(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	cwd := w.Path("api")

	session := w.Spawn(app, fakeagent.Codex, cwd)
	first := w.Launched(session)

	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: session})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })

	w.Spawn(app, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = session
		m.ResumeSessionID = protocol.Ptr(session)
	})
	resumed := w.Launched(session)
	if !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("respawn ran codex %q, want resume %s", resumed.Argv, first.ConversationID)
	}
}
