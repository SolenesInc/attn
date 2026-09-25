package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestCodexRespawnResumesTheConversationItsSessionStartHookReported(t *testing.T) {
	w := newWorld(t, withAgents(fakeagent.Codex))
	app := w.app()
	cwd := w.path("api")

	session := w.spawn(app, fakeagent.Codex, cwd)
	first := w.launched(session)

	app.send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: session})
	await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })

	w.spawn(app, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = session
		m.ResumeSessionID = protocol.Ptr(session)
	})
	resumed := w.launched(session)
	if !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("respawn ran codex %q, want resume %s", resumed.Argv, first.ConversationID)
	}
}
