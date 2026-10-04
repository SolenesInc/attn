package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestRestartKeepsConversationsThatCanResumeAndPrunesTheRest(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app := w.App()

	codexKept := w.Spawn(app, fakeagent.Codex, w.Path("api"))
	takeTurn(app, w.Launched(codexKept), codexKept)
	codexGone := w.Spawn(app, fakeagent.Codex, w.Path("web"))
	goneRun := w.Launched(codexGone)
	takeTurn(app, goneRun, codexGone)
	rollouts, err := filepath.Glob(filepath.Join(w.Dir, "toolhome", ".codex", "sessions", "*", "*", "*", "rollout-*-"+goneRun.ConversationID+".jsonl"))
	if err != nil || len(rollouts) != 1 {
		t.Fatalf("rollout of %s = %v (%v), want exactly one", goneRun.ConversationID, rollouts, err)
	}
	if err := os.Remove(rollouts[0]); err != nil {
		t.Fatal(err)
	}

	talked := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	first := w.Launched(talked)
	app.TypeLine(talked, "add a discount field to checkout")
	if got := first.Prompted(); got != "add a discount field to checkout" {
		t.Fatalf("claude received %q", got)
	}
	first.Reply("Before or after tax? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, talked, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	untouched := w.Spawn(app, fakeagent.Claude, w.Path("blog"))
	w.Launched(untouched)

	w.restart()
	app = w.App()
	initial := app.Initial
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
	if states[codexKept] != protocol.SessionStateRecoverable {
		t.Errorf("a codex session whose rollout exists came back %q, want recoverable", states[codexKept])
	}
	if _, ok := states[codexGone]; ok {
		t.Error("a codex session whose rollout is gone survived the restart")
	}
	if !slices.ContainsFunc(initial.Warnings, func(w protocol.DaemonWarning) bool { return w.Code == "stale_sessions_pruned" }) {
		t.Fatalf("warnings = %+v, want stale_sessions_pruned", initial.Warnings)
	}

	boot := w.HoldNextBoot()
	w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ID = talked
		m.ResumeSessionID = protocol.Ptr(talked)
	})
	testworld.AwaitSession(app, talked, func(s protocol.Session) bool { return s.State == protocol.SessionStateLaunching })
	boot()
	resumed := w.Launched(talked)
	if !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("respawn ran claude %q, want -r %s", resumed.Argv, first.ConversationID)
	}
	app.TypeLine(talked, "after tax")
	if got := resumed.Prompted(); got != "after tax" {
		t.Fatalf("resumed claude received %q", got)
	}
	resumed.Reply("Applied after tax. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, talked, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}
