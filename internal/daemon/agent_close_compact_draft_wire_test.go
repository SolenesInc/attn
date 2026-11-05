package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	unsentDraft   = "please keep this draft"
	userKeystroke = "keyboard"
)

type compactingHarness struct {
	name      string
	agent     fakeagent.Harness
	inATurn   func(*testing.T, *world, *testworld.Peer) (string, *fakeagent.Run)
	holdNext  func(*world, *fakeagent.Run) (func(), func())
	compacted func(*world, *fakeagent.Run) int
	mailRang  func(*testing.T, *world, *fakeagent.Run)
}

func compactingHarnesses() []compactingHarness {
	return []compactingHarness{
		{
			name:    "shared Codex",
			agent:   fakeagent.Codex,
			inATurn: sharedCodexInATurn,
			holdNext: func(w *world, _ *fakeagent.Run) (func(), func()) {
				return w.CodexServer().HoldNextCompaction()
			},
			compacted: func(w *world, run *fakeagent.Run) int { return w.CodexServer().Compactions(run.ConversationID) },
			mailRang: func(t *testing.T, w *world, run *fakeagent.Run) {
				t.Helper()
				if got := w.CodexServer().Prompted(run.ConversationID); !strings.Contains(got, inboxDoorbell) {
					t.Fatalf("the agent took %q, want the ring of the mail that waited behind the compact", got)
				}
			},
		},
		{
			name:    "Claude mod",
			agent:   fakeagent.Claude,
			inATurn: moddedClaudeInATurn,
			holdNext: func(_ *world, run *fakeagent.Run) (func(), func()) {
				return run.ModHoldNextCompact()
			},
			compacted: func(_ *world, run *fakeagent.Run) int { return run.ModCompactRequests() },
			mailRang: func(t *testing.T, _ *world, run *fakeagent.Run) {
				t.Helper()
				ringOfTheMailBehindTheCompact(t, run)
			},
		},
	}
}

func eachCompactingHarness(t *testing.T, test func(*testing.T, compactingHarness)) {
	for _, harness := range compactingHarnesses() {
		t.Run(harness.name, func(t *testing.T) { test(t, harness) })
	}
}

func sendDraft(t *testing.T, app *testworld.Peer, run *fakeagent.Run, session string) {
	t.Helper()
	app.Type(session, "\r", userKeystroke)
	if got := run.Prompted(); got != unsentDraft {
		t.Fatalf("the agent took %q when the user pressed Enter, want the draft", got)
	}
}

func idleWithTheCloseCancelled(t *testing.T, w *world, app *testworld.Peer, h compactingHarness, run *fakeagent.Run, session string) {
	t.Helper()
	run.Reply("Fixed and closing. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	sendAgentMessage(t, w.Client(), "sender", session, "the review is in")
	h.mailRang(t, w, run)
	run.Reply("Read it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func typeDraft(app *testworld.Peer, session string) {
	app.T.Helper()
	app.Type(session, unsentDraft, userKeystroke)
	app.AwaitScreen(session, unsentDraft)
}

func TestADraftTypedBeforeTheCloseWasAskedKeepsTheSessionOpenUncompacted(t *testing.T) {
	eachCompactingHarness(t, func(t *testing.T, h compactingHarness) {
		w := newWorld(t, h.agent)
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "sender")
		session, run := h.inATurn(t, w, app)

		typeDraft(app, session)
		closeCompacting(t, cli, session)
		idleWithTheCloseCancelled(t, w, app, h, run, session)

		sendDraft(t, app, run, session)
		if got := h.compacted(w, run); got != 0 {
			t.Errorf("the conversation compacted %d times over the draft, want none", got)
		}
		stillOpen(t, cli, session, "with a draft typed before the close was asked")
	})
}

func TestADraftTypedBeforeTheCompactKeepsTheSessionOpenUncompacted(t *testing.T) {
	eachCompactingHarness(t, func(t *testing.T, h compactingHarness) {
		w := newWorld(t, h.agent)
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "sender")
		session, run := h.inATurn(t, w, app)

		closeCompacting(t, cli, session)
		typeDraft(app, session)
		idleWithTheCloseCancelled(t, w, app, h, run, session)

		sendDraft(t, app, run, session)
		if got := h.compacted(w, run); got != 0 {
			t.Errorf("the conversation compacted %d times over the draft, want none", got)
		}
		stillOpen(t, cli, session, "with a draft typed before the compact")
	})
}

func TestADraftTypedDuringTheCompactKeepsTheSessionOpen(t *testing.T) {
	eachCompactingHarness(t, func(t *testing.T, h compactingHarness) {
		w := newWorld(t, h.agent)
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "sender")
		session, run := h.inATurn(t, w, app)
		arrived, release := h.holdNext(w, run)

		closeCompacting(t, cli, session)
		run.Reply("Fixed and closing. <!-- attn:state=idle -->")
		arrived()
		typeDraft(app, session)
		sendAgentMessage(t, cli, "sender", session, "the review is in")
		release()

		h.mailRang(t, w, run)
		stillOpen(t, cli, session, "with a draft typed during the compact")
		app.AwaitScreen(session, unsentDraft)
	})
}

func TestTerminalTrafficThatIsNotTheUserTypingLetsACompactingCloseFinish(t *testing.T) {
	const focusIn, focusOut, pointerClick = "\x1b[I", "\x1b[O", "\x1b[<0;10;10M\x1b[<0;10;10m"
	traffic := []struct{ name, keys, source string }{
		{"a focus change", focusIn + focusOut, userKeystroke},
		{"a pointer click", pointerClick, userKeystroke},
		{"cycling the permission mode", "\x1b[Z", userKeystroke},
		{"moving the cursor", "\x1b[D\x1b[C", userKeystroke},
		{"a terminal's reply", "\x1b[12;40R", "response"},
		{"an agent's write", "words from an agent", "agent"},
		{"an automation's write", "words from an automation", "automation"},
		{"replayed attach output", "replayed words", "attach_replay"},
	}
	eachCompactingHarness(t, func(t *testing.T, h compactingHarness) {
		for _, sent := range traffic {
			t.Run(sent.name, func(t *testing.T) {
				w := newWorld(t, h.agent)
				app, cli := w.App(), w.Client()
				session, run := h.inATurn(t, w, app)
				arrived, release := h.holdNext(w, run)

				app.Type(session, sent.keys, sent.source)
				closeCompacting(t, cli, session)
				app.Type(session, sent.keys, sent.source)
				run.Reply("Fixed and closing. <!-- attn:state=idle -->")
				arrived()
				app.Type(session, sent.keys, sent.source)
				release()

				if closed := awaitClosed(app, session); protocol.Deref(closed.CloseReason) != compactReason {
					t.Errorf("the session closed for %q, want %q", protocol.Deref(closed.CloseReason), compactReason)
				}
				if got := h.compacted(w, run); got != 1 {
					t.Errorf("the conversation compacted %d times before the close, want once", got)
				}
			})
		}
	})
}
