package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestClaudeStopClassifiesATranscriptWrittenAfterTheHook(t *testing.T) {
	w := newWorld(t, withAgents(fakeagent.Claude))
	app := w.app()

	session := w.spawn(app, fakeagent.Claude, w.path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("rename the checkout module")
	})
	run := w.launched(session)
	if got := run.Prompted(); got != "rename the checkout module" {
		t.Fatalf("claude received %q", got)
	}
	run.ReplyAfterStop("Need your input: keep the old import path as an alias? <!-- attn:state=waiting_input -->")

	awaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}
