package daemon_test

import (
	"slices"
	"strings"
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

func TestClaudesAssistantWindowKeepsFinalRepliesAcrossTurns(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	run := w.Launched(session)
	replies := []string{"First reply.", "Second reply."}
	for i, reply := range replies {
		app.TypeLine(session, "continue")
		run.Prompted()
		run.Reply(reply + " <!-- attn:state=waiting_input -->")
		for {
			window := messageWindow(app, session)
			if !slices.ContainsFunc(window.Messages, func(m protocol.SessionMessage) bool { return strings.Contains(m.Markdown, reply) }) {
				continue
			}
			if window.Status != protocol.SessionMessageWindowStatusReady || len(window.Messages) != i+1 {
				t.Fatalf("window after turn %d: %+v", i+1, window)
			}
			for j, m := range window.Messages {
				if !strings.Contains(m.Markdown, replies[j]) {
					t.Fatalf("reply %d: %+v", j, m)
				}
			}
			break
		}
	}
}
