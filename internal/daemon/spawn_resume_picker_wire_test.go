package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestASpawnThatAsksForTheResumePickerOpensItInsteadOfAConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ResumePicker = protocol.Ptr(true)
	})
	run := w.Launched(session)
	if !run.ResumePicker || run.Resumed {
		t.Errorf("claude ran as %q, want it opening the resume picker", run.Argv)
	}
	if _, named := flagValue(run.Argv, "--session-id"); named {
		t.Errorf("claude ran as %q, want no conversation named for the picker to override", run.Argv)
	}
}
