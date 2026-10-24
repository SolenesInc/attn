package daemon_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSpawningAConversationALiveSharedSessionHoldsOpensThatSessionInANewTerminal(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, first, firstTerminal := sharedCodexWaiting(t, w, app)
	conversation := first.ConversationID

	asked := protocol.SessionID(uuid.NewString())
	spawned := testworld.Request(app, protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: asked, Agent: string(fakeagent.Codex), Cwd: w.Path("shop"), ProfileID: app.SelectedProfile(),
		Placement: &protocol.SessionPlacement{}, Cols: 100, Rows: 30, ResumeSessionID: protocol.Ptr(conversation),
	}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == asked || string(r.ID) == checkout })
	if !spawned.Success || string(spawned.ID) != checkout || protocol.Deref(spawned.PaneID) == "" {
		t.Fatalf("spawning conversation %s answered %+v, want session %s placed in a new tile (error %q)", conversation, spawned, checkout, protocol.Deref(spawned.Error))
	}
	desktop := viewProfile(t, w, app.SelectedProfile()).desktops[protocol.Deref(spawned.DesktopID)]
	var secondTerminal string
	for _, pane := range desktop.Panes {
		if pane.PaneID == protocol.Deref(spawned.PaneID) {
			secondTerminal = string(pane.RuntimeID)
		}
	}
	if secondTerminal == "" || secondTerminal == firstTerminal {
		t.Fatalf("the new tile runs terminal %q, want a terminal of its own beside %s", secondTerminal, firstTerminal)
	}
	for _, terminal := range []string{firstTerminal, secondTerminal} {
		if got := sessionShownIn(t, w, app, terminal); got != checkout {
			t.Fatalf("terminal %s shows session %s, want %s", terminal, got, checkout)
		}
	}
	second := w.LaunchedCarrying(secondTerminal)
	if !second.Resumed || second.ConversationID != conversation {
		t.Fatalf("the new terminal's Codex shows %s (resumed=%v), want %s", second.ConversationID, second.Resumed, conversation)
	}
	if entry := ledgerShowOverTheWebSocket(app, string(asked)).Entry; entry != nil {
		t.Fatalf("the ledger holds the asked-for session %+v, want only %s", entry, checkout)
	}
	if listed, err := w.Client().List(""); err != nil || len(listed.Sessions) != 1 || string(listed.Sessions[0].ID) != checkout {
		t.Fatalf("open sessions = %+v (%v), want only %s", listed, err, checkout)
	}

	before := listedState(t, w, checkout)
	typeInto(app, secondTerminal, "lock it\r")
	if got := second.Prompted(); got != "lock it" {
		t.Fatalf("the new terminal's Codex took %q, want the prompt typed there", got)
	}
	working := testworld.AwaitStateAfter(app, before, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	second.Reply("Locked. <!-- attn:state=idle -->")
	testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	if closed := closeTileFromApp(app, protocol.Deref(spawned.DesktopID), protocol.Deref(spawned.PaneID)); !closed.Success {
		t.Fatalf("close the new tile: %s", protocol.Deref(closed.Error))
	}
	if entry := ledgerShowOverTheWebSocket(app, checkout).Entry; entry == nil || protocol.Deref(entry.ClosedAt) != "" {
		t.Fatalf("closing the new tile left %+v, want %s open in its first tile", entry, checkout)
	}
	if got := sessionShownIn(t, w, app, firstTerminal); got != checkout {
		t.Fatalf("after the new tile closed, the first terminal shows %s, want %s", got, checkout)
	}
	if closed := closeFromApp(app, checkout); !closed.Accepted {
		t.Fatalf("close %s: %s", checkout, protocol.Deref(closed.Error))
	}
	awaitClosed(app, checkout)
	w.CodexServer().AwaitArchived(conversation)
	for _, desktop := range viewProfile(t, w, app.SelectedProfile()).desktops {
		for _, pane := range desktop.Panes {
			if string(pane.RuntimeID) == firstTerminal || string(pane.RuntimeID) == secondTerminal {
				t.Errorf("tile %s still holds terminal %s after its session closed", pane.PaneID, pane.RuntimeID)
			}
		}
	}
	if listed, err := w.Client().List(""); err != nil || len(listed.Sessions) != 0 {
		t.Fatalf("open sessions after the close = %+v (%v), want none", listed, err)
	}
}
