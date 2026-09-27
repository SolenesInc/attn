package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestCodexSessionsSharingADirectoryEachFollowTheirOwnConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	cwd := w.Path("shop")
	turn := func(session string, run *fakeagent.Run, prompt, reply string, want protocol.SessionState) {
		t.Helper()
		app.TypeLine(session, prompt)
		run.Prompted()
		run.Reply(reply + " <!-- attn:state=" + string(want) + " -->")
		testworld.AwaitSession(app, session, func(s protocol.Session) bool {
			return s.State == want && protocol.Deref(s.StateReason) == "classifier_verdict"
		})
	}

	older := w.Spawn(app, fakeagent.Codex, cwd)
	olderRun := w.Launched(older)
	turn(older, olderRun, "rename the checkout module", "Keep the old import path as an alias?", protocol.SessionStateWaitingInput)
	newer := w.Spawn(app, fakeagent.Codex, cwd)
	newerRun := w.Launched(newer)
	turn(newer, newerRun, "add a discount field to checkout", "Added the discount field.", protocol.SessionStateIdle)

	if state := queriedSession(t, w.Client(), older).State; state != protocol.SessionStateWaitingInput {
		t.Fatalf("the newer neighbor's reply moved the older session to %s", state)
	}
	turn(older, olderRun, "yes, keep the alias", "Renamed. Should the alias warn on import?", protocol.SessionStateWaitingInput)
	if state := queriedSession(t, w.Client(), newer).State; state != protocol.SessionStateIdle {
		t.Fatalf("the older session's reply moved the newer neighbor to %s", state)
	}
}
