package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestRestartKeepsConversationsThatCanResumeAndPrunesTheRest(t *testing.T) {
	w := newWorld(t, withAgents(fakeagent.Claude))
	app := w.app()

	talked := w.spawn(app, fakeagent.Claude, w.path("shop"))
	first := w.launched(talked)
	app.typeLine(talked, "add a discount field to checkout")
	if got := first.Prompted(); got != "add a discount field to checkout" {
		t.Fatalf("claude received %q", got)
	}
	first.Reply("Before or after tax? <!-- attn:state=waiting_input -->")
	awaitSession(app, talked, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	untouched := w.spawn(app, fakeagent.Claude, w.path("blog"))
	w.launched(untouched)

	w.restart()
	app = w.app()
	initial := app.initial
	states := map[string]protocol.SessionState{}
	for _, s := range initial.Sessions {
		states[s.ID] = s.State
	}
	if states[talked] != protocol.SessionStateRecoverable {
		t.Fatalf("session with a conversation came back %q, want recoverable", states[talked])
	}
	if _, ok := states[untouched]; ok {
		t.Fatal("a session that never started a conversation survived the restart")
	}
	if !slices.ContainsFunc(initial.Warnings, func(w protocol.DaemonWarning) bool { return w.Code == "stale_sessions_pruned" }) {
		t.Fatalf("warnings = %+v, want stale_sessions_pruned", initial.Warnings)
	}

	w.spawn(app, fakeagent.Claude, w.path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ID = talked
		m.ResumeSessionID = protocol.Ptr(talked)
	})
	resumed := w.launched(talked)
	if !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("respawn ran claude %q, want -r %s", resumed.Argv, first.ConversationID)
	}
	app.typeLine(talked, "after tax")
	if got := resumed.Prompted(); got != "after tax" {
		t.Fatalf("resumed claude received %q", got)
	}
	resumed.Reply("Applied after tax. <!-- attn:state=idle -->")
	awaitSession(app, talked, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}
