package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestCommentaryWrittenMidTurnReachesTheMessageWindowWhileTheSessionWorks(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	run := w.Launched(session)
	app.TypeLine(session, "why does the renderer flicker?")
	run.Prompted()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	run.Stream("Checking the current renderer.")
	for {
		window := messageWindow(app, session)
		if slices.ContainsFunc(window.Messages, func(m protocol.SessionMessage) bool { return m.Markdown == "Checking the current renderer." }) {
			break
		}
	}
	if state := queriedSession(t, w.Client(), session).State; state != protocol.SessionStateWorking {
		t.Fatalf("commentary mid-turn moved the session to %s, want it still working", state)
	}
}
