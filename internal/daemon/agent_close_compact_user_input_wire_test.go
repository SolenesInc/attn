package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	unsentDraft   = "please keep this draft"
	userKeystroke = "keyboard"
)

type compactingHarness struct {
	name        string
	agent       fakeagent.Harness
	inATurn     func(*testing.T, *world, *testworld.Peer) (string, *fakeagent.Run)
	holdNext    func(*world, *fakeagent.Run) (func(), func())
	compacted   func(*world, *fakeagent.Run) int
	takeNotes   func(*testing.T, *world, *fakeagent.Run, string)
	closeInTurn func(*testing.T, *client.Client, *fakeagent.Run, string)
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
			closeInTurn: func(t *testing.T, cli *client.Client, _ *fakeagent.Run, session string) {
				t.Helper()
				closeCompacting(t, cli, session)
			},
			takeNotes: func(t *testing.T, w *world, run *fakeagent.Run, notes string) {
				t.Helper()
				if got := w.CodexServer().Prompted(run.ConversationID); got != notes {
					t.Fatalf("the running turn took %q, want the user's notes", got)
				}
				run.Reply("Covered. <!-- attn:state=idle -->")
			},
		},
		{
			name:    "Claude mod",
			agent:   fakeagent.Claude,
			inATurn: moddedClaudeInATurn,
			holdNext: func(_ *world, run *fakeagent.Run) (func(), func()) {
				return run.ModHoldNextCompact()
			},
			compacted:   func(_ *world, run *fakeagent.Run) int { return run.ModCompactions() },
			closeInTurn: closeCompactingDuringTheTurn,
			takeNotes: func(t *testing.T, _ *world, run *fakeagent.Run, notes string) {
				t.Helper()
				run.Reply("Fixed and closing. <!-- attn:state=idle -->")
				if got := run.Prompted(); got != notes {
					t.Fatalf("the agent took %q, want the user's notes", got)
				}
				run.Reply("Covered. <!-- attn:state=idle -->")
			},
		},
	}
}

func eachCompactingHarness(t *testing.T, test func(*testing.T, compactingHarness)) {
	for _, harness := range compactingHarnesses() {
		t.Run(harness.name, func(t *testing.T) { test(t, harness) })
	}
}

func typeDraft(app *testworld.Peer, session string) {
	app.T.Helper()
	app.Type(session, unsentDraft, userKeystroke)
	app.AwaitScreen(session, unsentDraft)
}

func closedAfterOneCompact(t *testing.T, w *world, app *testworld.Peer, h compactingHarness, run *fakeagent.Run, session string) {
	t.Helper()
	if closed := awaitClosed(app, session); protocol.Deref(closed.CloseReason) != compactReason {
		t.Errorf("the session closed for %q, want %q", protocol.Deref(closed.CloseReason), compactReason)
	}
	if got := h.compacted(w, run); got != 1 {
		t.Errorf("the conversation compacted %d times before the close, want once", got)
	}
}

func TestACompactingCloseFinishesOverADraftTypedBeforeTheCloseWasAsked(t *testing.T) {
	eachCompactingHarness(t, func(t *testing.T, h compactingHarness) {
		w := newWorld(t, h.agent)
		app, cli := w.App(), w.Client()
		session, run := h.inATurn(t, w, app)

		typeDraft(app, session)
		closeCompacting(t, cli, session)
		run.Reply("Fixed and closing. <!-- attn:state=idle -->")

		closedAfterOneCompact(t, w, app, h, run, session)
	})
}

func TestACompactingCloseFinishesOverADraftTypedBeforeTheCompact(t *testing.T) {
	eachCompactingHarness(t, func(t *testing.T, h compactingHarness) {
		w := newWorld(t, h.agent)
		app, cli := w.App(), w.Client()
		session, run := h.inATurn(t, w, app)

		closeCompacting(t, cli, session)
		typeDraft(app, session)
		run.Reply("Fixed and closing. <!-- attn:state=idle -->")

		closedAfterOneCompact(t, w, app, h, run, session)
	})
}

func TestACompactingCloseFinishesOverADraftTypedDuringTheCompact(t *testing.T) {
	eachCompactingHarness(t, func(t *testing.T, h compactingHarness) {
		w := newWorld(t, h.agent)
		app, cli := w.App(), w.Client()
		session, run := h.inATurn(t, w, app)
		arrived, release := h.holdNext(w, run)

		closeCompacting(t, cli, session)
		run.Reply("Fixed and closing. <!-- attn:state=idle -->")
		arrived()
		typeDraft(app, session)
		release()

		closedAfterOneCompact(t, w, app, h, run, session)
	})
}

func TestACompactingCloseFinishesWhenTheUserPressesEnterDuringTheCompact(t *testing.T) {
	eachCompactingHarness(t, func(t *testing.T, h compactingHarness) {
		w := newWorld(t, h.agent)
		app, cli := w.App(), w.Client()
		session, run := h.inATurn(t, w, app)
		arrived, release := h.holdNext(w, run)

		closeCompacting(t, cli, session)
		run.Reply("Fixed and closing. <!-- attn:state=idle -->")
		arrived()
		app.Type(session, "wait, one more thing\r", userKeystroke)
		release()

		closedAfterOneCompact(t, w, app, h, run, session)
	})
}

func TestACompactingCloseFinishesAfterAnAnnotationTheAgentTookFirst(t *testing.T) {
	eachCompactingHarness(t, func(t *testing.T, h compactingHarness) {
		w := newWorld(t, h.agent)
		app, cli := w.App(), w.Client()
		session, run := h.inATurn(t, w, app)
		const notes = "also cover the tax lookup"

		h.closeInTurn(t, cli, run, session)
		if sent := submitSessionAnnotationFeedback(app, session, notes); !sent.success {
			t.Fatalf("the user's notes to the working session = %+v", sent)
		}
		h.takeNotes(t, w, run, notes)

		closedAfterOneCompact(t, w, app, h, run, session)
	})
}

func TestNotesTheUserSendsDuringTheCompactSayTheSessionIsClosingAndTheCloseFinishes(t *testing.T) {
	eachCompactingHarness(t, func(t *testing.T, h compactingHarness) {
		w := newWorld(t, h.agent)
		app, cli := w.App(), w.Client()
		session, run := h.inATurn(t, w, app)
		arrived, release := h.holdNext(w, run)

		closeCompacting(t, cli, session)
		run.Reply("Fixed and closing. <!-- attn:state=idle -->")
		arrived()
		sent := submitSessionAnnotationFeedback(app, session, "wait, one more thing")
		if sent.success || !strings.Contains(sent.err, "closes when that finishes") {
			t.Fatalf("the user's notes during the compact = %+v, want them refused, saying the session is closing", sent)
		}
		release()

		closedAfterOneCompact(t, w, app, h, run, session)
	})
}
