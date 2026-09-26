package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestClaudeStopClassifiesATranscriptWrittenAfterTheHook(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()

	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("rename the checkout module")
	})
	run := w.Launched(session)
	if got := run.Prompted(); got != "rename the checkout module" {
		t.Fatalf("claude received %q", got)
	}
	run.ReplyAfterStop("Need your input: keep the old import path as an alias? <!-- attn:state=waiting_input -->")

	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}
