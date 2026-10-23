package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestNewInASharedCodexTerminalLeavesTheSessionItLeftOpenAndHidden(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)

	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	hidden := testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	if hidden.State != protocol.SessionStateWaitingInput {
		t.Errorf("the hidden session is %s, want it still waiting on the user", hidden.State)
	}
	discount := sessionShownIn(t, w, app, terminal)
	if discount == checkout {
		t.Fatalf("terminal %s still shows %s after /new and a prompt", terminal, checkout)
	}
	codex.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, discount, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	entry := ledgerShowOverTheWebSocket(app, checkout).Entry
	if entry == nil || protocol.Deref(entry.ClosedAt) != "" || !protocol.Deref(entry.Hidden) {
		t.Errorf("the ledger shows %+v, want %s live and hidden", entry, checkout)
	}
	if shown := testworld.AwaitSession(app, discount, func(protocol.Session) bool { return true }); protocol.Deref(shown.Hidden) {
		t.Errorf("session %s, which terminal %s shows, is hidden", discount, terminal)
	}
}

func TestASharedCodexTerminalShowsTheSessionOfEachConversationItSwitchesToBeforeAnyPrompt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID
	tax := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	taxCodex := w.Launched(tax)
	app.TypeLine(tax, "look at the tax table")
	taxCodex.Prompted()
	taxCodex.Reply("Looked. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, tax, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	typeInto(app, terminal, "/resume "+taxCodex.ConversationID+"\r")
	codex.Prompted()
	if got := sessionShownIn(t, w, app, terminal); got != tax {
		t.Fatalf("after /resume of the conversation of %s, terminal %s shows session %s", tax, terminal, got)
	}
	if left := queriedSession(t, w.Client(), checkout); !protocol.Deref(left.Hidden) || left.State != protocol.SessionStateWaitingInput {
		t.Errorf("the session the terminal left is %s (hidden=%v), want it hidden and still waiting on the user", left.State, protocol.Deref(left.Hidden))
	}

	typeInto(app, terminal, "/new\r")
	codex.Prompted()
	fresh := sessionShownIn(t, w, app, terminal)
	if fresh == checkout || fresh == tax {
		t.Fatalf("after /new, terminal %s shows session %s, want a new one", terminal, fresh)
	}
	if got := sessionShownIn(t, w, app, app.Terminal(tax)); got != tax {
		t.Errorf("the tile of %s shows session %s after the other terminal left it", tax, got)
	}

	typeInto(app, terminal, "/resume "+conversation+"\r")
	codex.Prompted()
	if got := sessionShownIn(t, w, app, terminal); got != checkout {
		t.Fatalf("after /resume of the conversation of %s, terminal %s shows session %s", checkout, terminal, got)
	}
	if back := queriedSession(t, w.Client(), checkout); protocol.Deref(back.Hidden) || back.State != protocol.SessionStateWaitingInput {
		t.Errorf("the session the terminal came back to is %s (hidden=%v), want it shown and still waiting on the user", back.State, protocol.Deref(back.Hidden))
	}
	if left := queriedSession(t, w.Client(), fresh); !protocol.Deref(left.Hidden) {
		t.Errorf("session %s, which /new opened and the terminal left, is not hidden", fresh)
	}
}

func TestASharedCodexTerminalThatAgentsTakesBackToAnUnpromptedNewConversationShowsItsSessionAtThePrompt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID
	typeInto(app, terminal, "/new\r")
	codex.Prompted()
	discount, blank := sessionShownIn(t, w, app, terminal), codex.ConversationID
	if discount == checkout {
		t.Fatalf("after /new, terminal %s still shows session %s", terminal, checkout)
	}
	typeInto(app, terminal, "/agents "+conversation+"\r")
	codex.Prompted()
	if got := sessionShownIn(t, w, app, terminal); got != checkout {
		t.Fatalf("after /agents to the conversation of %s, terminal %s shows session %s", checkout, terminal, got)
	}

	typeInto(app, terminal, "/agents "+blank+"\r")
	codex.Prompted()
	typeInto(app, terminal, "add a discount field\r")
	if got := codex.Prompted(); got != "add a discount field" || codex.ConversationID != blank {
		t.Fatalf("codex took %q in %s, want the prompt in %s", got, codex.ConversationID, blank)
	}
	if got := sessionShownIn(t, w, app, terminal); got != discount {
		t.Fatalf("after a prompt in the conversation of %s, terminal %s shows session %s", discount, terminal, got)
	}
	if left := queriedSession(t, w.Client(), checkout); !protocol.Deref(left.Hidden) {
		t.Errorf("session %s, which the terminal left, is not hidden", checkout)
	}
}

func TestASharedCodexTerminalShowingASubagentKeepsTheSessionOfTheConversationThatStartedIt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	subagent := w.CodexServer().SpawnSubagent(codex.ConversationID)

	typeInto(app, terminal, "/multi-agents "+subagent+"\r")
	codex.Prompted()
	if got := sessionShownIn(t, w, app, terminal); got != checkout {
		t.Fatalf("showing subagent %s, terminal %s shows session %s, want %s", subagent, terminal, got, checkout)
	}
	listed, err := w.Client().Query("")
	if err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	for _, s := range listed {
		if string(s.ID) != checkout {
			t.Errorf("showing a subagent opened session %s", s.ID)
		}
	}
	if shown := queriedSession(t, w.Client(), checkout); protocol.Deref(shown.Hidden) {
		t.Errorf("session %s, whose subagent the terminal shows, is hidden", checkout)
	}
	if got := strings.TrimSpace(w.CodexServer().RunTool(subagent, `"$ATTN_WRAPPER_PATH" presence`)); got != "running inside attn (session "+checkout+")" {
		t.Errorf("a tool of subagent %s reports %q, want session %s", subagent, got, checkout)
	}

	desktop, tile := tileOf(t, w, app, terminal)
	if closed := closeTileFromApp(app, desktop, tile); !closed.Success {
		t.Fatalf("close the tile: %s", protocol.Deref(closed.Error))
	}
	if entry := ledgerShowOverTheWebSocket(app, checkout).Entry; entry == nil || protocol.Deref(entry.ClosedAt) == "" {
		t.Errorf("closing the last tile of %s while it showed a subagent left %+v, want it closed", checkout, entry)
	}
}

func TestANewSharedCodexTileThatResumesBeforeItsFirstPromptLeavesNoEmptySessionAndCanGoBack(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, _ := sharedCodexWaiting(t, w, app)
	fresh := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	freshCodex := w.Launched(fresh)
	terminal := app.Terminal(fresh)
	blank := freshCodex.ConversationID

	typeInto(app, terminal, "/resume "+codex.ConversationID+"\r")
	freshCodex.Prompted()
	if got := sessionShownIn(t, w, app, terminal); got != checkout {
		t.Fatalf("after /resume of the conversation of %s, the new tile shows session %s", checkout, got)
	}
	if entry := ledgerShowOverTheWebSocket(app, fresh).Entry; entry == nil || protocol.Deref(entry.ClosedAt) == "" {
		t.Errorf("the ledger shows %+v, want %s, which never had a conversation of its own, closed", entry, fresh)
	}

	typeInto(app, terminal, "/agents "+blank+"\r")
	freshCodex.Prompted()
	typeInto(app, terminal, "sketch the coupon form\r")
	if got := freshCodex.Prompted(); got != "sketch the coupon form" || freshCodex.ConversationID != blank {
		t.Fatalf("codex took %q in %s, want the prompt in %s", got, freshCodex.ConversationID, blank)
	}
	if coupon := sessionShownIn(t, w, app, terminal); coupon == checkout || coupon == fresh {
		t.Fatalf("after a prompt in the tile's own unprompted conversation, it shows session %s, want a new one", coupon)
	}
	if shown := queriedSession(t, w.Client(), checkout); protocol.Deref(shown.Hidden) {
		t.Errorf("session %s, which its own tile still shows, is hidden", checkout)
	}
}

func TestASharedCodexToolSpeaksAsItsConversationsSessionAfterItsTerminalMovesOn(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	hidden := codex.ConversationID
	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	shown := sessionShownIn(t, w, app, terminal)

	if got := submitSessionAnnotationFeedback(app, checkout, sessionAnnotationFeedback); !got.success {
		t.Fatalf("feedback to the hidden session = %+v", got)
	}
	server := w.CodexServer()
	server.Prompted(hidden)
	for conversation, want := range map[string]string{hidden: checkout, codex.ConversationID: shown} {
		got := strings.TrimSpace(server.RunTool(conversation, `"$ATTN_WRAPPER_PATH" presence`))
		if got != "running inside attn (session "+want+")" {
			t.Errorf("a tool of conversation %s reports %q, want session %s", conversation, got, want)
		}
	}
}

func TestClosingATileWhoseCodexMovedOnKeepsTheSessionItShowedHiddenAndWaiting(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID
	typeInto(app, terminal, "/new\r")
	codex.Prompted()

	desktop, tile := tileOf(t, w, app, terminal)
	if closed := closeTileFromApp(app, desktop, tile); !closed.Success {
		t.Fatalf("close the tile: %s", protocol.Deref(closed.Error))
	}
	hidden := testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	if hidden.State != protocol.SessionStateWaitingInput || hidden.TerminalExit != nil {
		t.Fatalf("the session the tile showed is %s (exit=%+v), want it hidden, still waiting and without the terminal's exit", hidden.State, hidden.TerminalExit)
	}
	for _, desktop := range viewProfile(t, w, app.SelectedProfile()).desktops {
		for _, pane := range desktop.Panes {
			if string(pane.RuntimeID) == terminal {
				t.Errorf("tile %s still holds terminal %s after its close", pane.PaneID, terminal)
			}
		}
	}
	if got := submitSessionAnnotationFeedback(app, checkout, sessionAnnotationFeedback); !got.success {
		t.Fatalf("feedback to the hidden session = %+v", got)
	}
	if got := w.CodexServer().Prompted(conversation); got != sessionAnnotationFeedback {
		t.Fatalf("the hidden conversation took %q, want the feedback", got)
	}
}

func TestTwoTerminalsShowOneSharedCodexSessionUntilTheLastTileClosesAndArchivesIt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, first, firstTerminal := sharedCodexWaiting(t, w, app)
	conversation := first.ConversationID
	other := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	second := w.Launched(other)
	secondTerminal := app.Terminal(other)
	app.TypeLine(other, "look at the tax table")
	second.Prompted()
	second.Reply("Looked. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, other, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	moveOn(t, app, second, secondTerminal, "/resume "+conversation, "lock it")
	if second.ConversationID != conversation {
		t.Fatalf("the second terminal shows %s, want %s", second.ConversationID, conversation)
	}
	testworld.AwaitSession(app, other, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	if got := sessionShownIn(t, w, app, secondTerminal); got != checkout {
		t.Fatalf("the second terminal shows session %s, want %s", got, checkout)
	}
	if got := sessionShownIn(t, w, app, firstTerminal); got != checkout {
		t.Fatalf("the first terminal shows session %s, want %s", got, checkout)
	}
	reply := "Locked. <!-- attn:state=idle -->"
	second.Reply(reply)
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	desktop, tile := tileOf(t, w, app, firstTerminal)
	if closed := closeTileFromApp(app, desktop, tile); !closed.Success {
		t.Fatalf("close the first tile: %s", protocol.Deref(closed.Error))
	}
	if entry := ledgerShowOverTheWebSocket(app, checkout).Entry; entry == nil || protocol.Deref(entry.ClosedAt) != "" {
		t.Fatalf("closing one of two tiles left %+v, want %s live", entry, checkout)
	}

	desktop, tile = tileOf(t, w, app, secondTerminal)
	if closed := closeTileFromApp(app, desktop, tile); !closed.Success {
		t.Fatalf("close the last tile: %s", protocol.Deref(closed.Error))
	}
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(e.Session.ID) == checkout
	})
	entry := ledgerShowOverTheWebSocket(app, checkout).Entry
	if entry == nil || protocol.Deref(entry.ClosedAt) == "" || entry.Usage == nil || entry.Usage.TotalTokens == 0 {
		t.Fatalf("the ledger shows %+v, want %s closed with its usage", entry, checkout)
	}
	if entry.Usage.MeasurementIncomplete != nil {
		t.Errorf("closing and archiving %s left its usage %+v, want it complete", checkout, entry.Usage)
	}
	w.CodexServer().AwaitArchived(conversation)

	reopened := reopenOverTheWebSocket(app, checkout)
	if !reopened.Success {
		t.Fatalf("resume %s from the ledger: %s", checkout, protocol.Deref(reopened.Error))
	}
	resumed := w.Launched(checkout)
	if !resumed.Resumed || resumed.ConversationID != conversation {
		t.Fatalf("the resumed Codex shows %s (resumed=%v), want conversation %s", resumed.ConversationID, resumed.Resumed, conversation)
	}
	app.TypeLine(checkout, "round the total")
	if got := resumed.Prompted(); got != "round the total" || resumed.ConversationID != conversation {
		t.Errorf("the resumed Codex took %q in %s, want it in %s", got, resumed.ConversationID, conversation)
	}
}

func TestOneSharedTerminalsExitLeavesItsSessionUsableInTheOther(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, first, firstTerminal := sharedCodexWaiting(t, w, app)
	other := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	second := w.Launched(other)
	secondTerminal := app.Terminal(other)
	moveOn(t, app, second, secondTerminal, "/resume "+first.ConversationID, "continue checkout")
	second.Reply("Continued. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	first.Exit(1)
	testworld.Await(app, protocol.EventTerminalBindingsUpdated, func(e protocol.TerminalBindingsUpdatedMessage) bool {
		found := false
		for _, binding := range e.TerminalBindings {
			if string(binding.TerminalID) == firstTerminal {
				return false
			}
			found = found || string(binding.TerminalID) == secondTerminal && string(binding.SessionID) == checkout
		}
		return found
	})
	before := listedState(t, w, checkout)
	if before.TerminalExit != nil || protocol.Deref(before.StateReason) == "process_exited" {
		t.Fatalf("the remaining terminal shows an exited session: %+v", before)
	}
	if result := submitSessionAnnotationFeedback(app, checkout, sessionAnnotationFeedback); !result.success {
		t.Fatalf("feedback to the remaining terminal: %+v", result)
	}
	server := w.CodexServer()
	if prompt := server.Prompted(first.ConversationID); prompt != sessionAnnotationFeedback {
		t.Fatalf("the remaining conversation took %q, want feedback", prompt)
	}
	working := testworld.AwaitStateAfter(app, before, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.Reply(first.ConversationID, "Still running. <!-- attn:state=waiting_input -->")
	testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

func TestClosingADesktopEndsOnlyItsTileOfASharedCodexSessionAnotherDesktopShows(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	profile := app.SelectedProfile()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, first, firstTerminal := sharedCodexWaiting(t, w, app)
	conversation := first.ConversationID
	elsewhere := createDesktop(app, profile)
	switchDesktop(app, profile, elsewhere.ID)
	other := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	second := w.Launched(other)
	secondTerminal := app.Terminal(other)
	moveOn(t, app, second, secondTerminal, "/resume "+conversation, "lock it")
	if shown := sessionShownIn(t, w, app, secondTerminal); shown != checkout {
		t.Fatalf("the second terminal shows session %s, want %s", shown, checkout)
	}

	if result := requestCloseDesktop(app, viewProfile(t, w, profile).desktops[elsewhere.ID]); !result.Success {
		t.Fatalf("close desktop %s: %s", elsewhere.ID, protocol.Deref(result.Error))
	}
	if entry := ledgerShowOverTheWebSocket(app, checkout).Entry; entry == nil || protocol.Deref(entry.ClosedAt) != "" {
		t.Fatalf("closing the desktop of one of two tiles left %+v, want %s live", entry, checkout)
	}
	if shown := sessionShownIn(t, w, app, firstTerminal); shown != checkout {
		t.Errorf("the first terminal shows %s after the other desktop closed, want %s", shown, checkout)
	}
	if archivedInCodex(t, w, conversation) {
		t.Errorf("closing one desktop archived conversation %s, which another desktop still shows", conversation)
	}
}

func TestClosingADesktopRefusesToEndTheChiefOnlyWhereItHoldsTheChiefsLastTile(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	profile := app.SelectedProfile()
	setSetting(t, app, "codex_shared_enabled", "true")
	spawned, home, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("chief"), func(m *protocol.SpawnSessionMessage) {
		m.ChiefOfStaff = protocol.Ptr(true)
	})
	if !spawned.Success {
		t.Fatalf("spawn the chief: %s", protocol.Deref(spawned.Error))
	}
	chief := string(spawned.ID)
	codex := w.Launched(chief)
	app.TypeLine(chief, "plan the week")
	codex.Prompted()
	codex.Reply("Planned. <!-- attn:state=idle -->")
	elsewhere := createDesktop(app, profile)
	switchDesktop(app, profile, elsewhere.ID)
	other := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	second := w.Launched(other)
	secondTerminal := app.Terminal(other)
	moveOn(t, app, second, secondTerminal, "/resume "+codex.ConversationID, "review the plan")
	if shown := sessionShownIn(t, w, app, secondTerminal); shown != chief {
		t.Fatalf("the second terminal shows session %s, want the chief %s", shown, chief)
	}

	if result := requestCloseDesktop(app, viewProfile(t, w, profile).desktops[elsewhere.ID]); !result.Success {
		t.Fatalf("closing a desktop with one of the chief's two tiles: %s", protocol.Deref(result.Error))
	}
	if result := requestCloseDesktop(app, viewProfile(t, w, profile).desktops[home]); result.Success || !strings.Contains(protocol.Deref(result.Error), "protected") {
		t.Fatalf("closing the desktop with the chief's last tile = %+v, want it refused as protected", result)
	}
	if entry := ledgerShowOverTheWebSocket(app, chief).Entry; entry == nil || protocol.Deref(entry.ClosedAt) != "" {
		t.Fatalf("the chief after both desktop closes = %+v, want it live", entry)
	}
}

func TestInputReachesAHiddenSharedCodexSessionWithoutTouchingAnotherTerminalsDraft(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID
	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	codex.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	typeInto(app, terminal, "half a thought")

	if got := submitSessionAnnotationFeedback(app, checkout, sessionAnnotationFeedback); !got.success || got.status != "delivered" {
		t.Fatalf("feedback to the hidden session = %+v, want it delivered", got)
	}
	server := w.CodexServer()
	if got := server.Prompted(conversation); got != sessionAnnotationFeedback {
		t.Fatalf("the hidden conversation took %q, want the feedback", got)
	}
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.Reply(conversation, "Locked it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	typeInto(app, terminal, " more\r")
	if got := codex.Prompted(); got != "half a thought more" {
		t.Errorf("the other terminal sent %q, want its draft untouched", got)
	}

	if closed := closeFromApp(app, checkout); !closed.Accepted {
		t.Fatalf("close the hidden session: %s", protocol.Deref(closed.Error))
	}
	if entry := ledgerShowOverTheWebSocket(app, checkout).Entry; entry == nil || protocol.Deref(entry.ClosedAt) == "" {
		t.Errorf("the ledger shows %+v, want %s closed", entry, checkout)
	}
	server.AwaitArchived(conversation)
	codex.Reply("Both added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, sessionShownIn(t, w, app, terminal), func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestShowingAHiddenSharedCodexSessionReplaysTheApprovalItWaitsOn(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "auto_approve_enabled", "false")
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID
	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	if got := submitSessionAnnotationFeedback(app, checkout, sessionAnnotationFeedback); !got.success {
		t.Fatalf("feedback to the hidden session = %+v", got)
	}
	server := w.CodexServer()
	server.Prompted(conversation)
	server.AskApproval(conversation)
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool {
		return s.State == protocol.SessionStatePendingApproval && protocol.Deref(s.Hidden)
	})

	if shown := requestShowSession(app, checkout); !shown.Success {
		t.Fatalf("show %s: %s", checkout, protocol.Deref(shown.Error))
	}
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return !protocol.Deref(s.Hidden) })
	shown := w.Launched(checkout)
	if !shown.Resumed || shown.ConversationID != conversation {
		t.Fatalf("the new tile's Codex shows %s (resumed=%v), want %s", shown.ConversationID, shown.Resumed, conversation)
	}
	app.AwaitScreen(checkout, "Allow the command to run?")
	app.TypeLine(checkout, "1")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.Reply(conversation, "Migrated. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestASharedCodexSessionAndItsConversationShareOneName(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID

	typeInto(app, terminal, "/rename Checkout race\r")
	codex.Prompted()
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return s.Label == "Checkout race" })

	renamed := testworld.Request(app, protocol.RenameSessionMessage{Cmd: protocol.CmdRenameSession, SessionID: protocol.SessionID(checkout), Label: "Tax lock"},
		protocol.EventRenameResult, func(r protocol.RenameResultMessage) bool { return string(r.ID) == checkout })
	if !renamed.Success {
		t.Fatalf("rename %s: %s", checkout, protocol.Deref(renamed.Error))
	}
	w.CodexServer().AwaitName(conversation, "Tax lock")
}

func TestAPlainCodexThatResumesAHiddenSharedConversationShowsThatSessionAsPlain(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID
	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	setSetting(t, app, "codex_shared_enabled", "false")

	billing := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	plain := w.Launched(billing)
	plainTerminal := app.Terminal(billing)
	moveOn(t, app, plain, plainTerminal, "/resume "+conversation, "carry on")
	if shown := sessionShownIn(t, w, app, plainTerminal); shown != checkout {
		t.Fatalf("/resume %s in a plain Codex shows %s, want %s", conversation, shown, checkout)
	}
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return !protocol.Deref(s.Hidden) })

	moveOn(t, app, plain, plainTerminal, "/new", "write the notes")
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(e.Session.ID) == checkout
	})
}

func TestAConversationResumedInAnotherProfileIsThatProfilesOwnSharedCodexSession(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	side, sideApp, elsewhere, sideCodex := sideCodexIdle(t, w, app)
	conversation := sideCodex.ConversationID

	here := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(here)
	terminal := app.Terminal(here)
	moveOn(t, app, codex, terminal, "/resume "+conversation, "ship it")
	resumed := sessionShownIn(t, w, app, terminal)
	if resumed == elsewhere {
		t.Fatalf("/resume %s here showed %s from profile %s, want a session in this profile", conversation, elsewhere, side.ID)
	}

	typeInto(app, terminal, "/rename Release train\r")
	codex.Prompted()
	testworld.AwaitSession(app, resumed, func(s protocol.Session) bool { return s.Label == "Release train" })
	if other := queriedSession(t, w.Client(), elsewhere); other.Label == "Release train" {
		t.Errorf("renaming the conversation here renamed %s in profile %s", elsewhere, side.ID)
	}

	if closed := closeFromApp(sideApp, elsewhere); !closed.Accepted {
		t.Fatalf("close %s in profile %s: %s", elsewhere, side.ID, protocol.Deref(closed.Error))
	}
	if archivedInCodex(t, w, conversation) {
		t.Errorf("closing %s archived conversation %s, which %s still holds", elsewhere, conversation, resumed)
	}
}

func TestAResumedSharedCodexSessionNamesItsConversationAgainAfterAnotherProfileRenamedIt(t *testing.T) {
	for _, way := range []string{"ledger", "/resume"} {
		t.Run(way, func(t *testing.T) { resumeRenamedSharedCodexSession(t, way) })
	}
}

func resumeRenamedSharedCodexSession(t *testing.T, way string) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	side, sideApp, elsewhere, sideCodex := sideCodexIdle(t, w, app)
	conversation := sideCodex.ConversationID
	renamed := testworld.Request(sideApp, protocol.RenameSessionMessage{Cmd: protocol.CmdRenameSession, SessionID: protocol.SessionID(elsewhere), Label: "Release plan"},
		protocol.EventRenameResult, func(r protocol.RenameResultMessage) bool { return string(r.ID) == elsewhere })
	if !renamed.Success {
		t.Fatalf("rename %s: %s", elsewhere, protocol.Deref(renamed.Error))
	}
	w.CodexServerOf(side.ID).AwaitName(conversation, "Release plan")

	here := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(here)
	terminal := app.Terminal(here)
	moveOn(t, app, codex, terminal, "/resume "+conversation, "ship it")
	resumed := sessionShownIn(t, w, app, terminal)
	typeInto(app, terminal, "/rename Release train\r")
	codex.Prompted()
	testworld.AwaitSession(app, resumed, func(s protocol.Session) bool { return s.Label == "Release train" })
	w.CodexServerOf(app.SelectedProfile()).AwaitName(conversation, "Release train")

	if way == "ledger" {
		for _, c := range []struct {
			app     *testworld.Peer
			session string
		}{{app, resumed}, {sideApp, elsewhere}} {
			if closed := closeFromApp(c.app, c.session); !closed.Accepted {
				t.Fatalf("close %s: %s", c.session, protocol.Deref(closed.Error))
			}
		}
		if reopened := reopenOverTheWebSocket(sideApp, elsewhere); !reopened.Success {
			t.Fatalf("resume %s from the ledger: %s", elsewhere, protocol.Deref(reopened.Error))
		}
	} else {
		if closed := closeFromApp(sideApp, elsewhere); !closed.Accepted {
			t.Fatalf("close %s: %s", elsewhere, protocol.Deref(closed.Error))
		}
		moveOn(t, app, codex, terminal, "/new", "write the notes")
		next := w.Spawn(sideApp, fakeagent.Codex, w.Path("shop"))
		sideTerminal := sideApp.Terminal(next)
		moveOn(t, sideApp, w.Launched(next), sideTerminal, "/resume "+conversation, "carry on")
		if shown := sessionShownIn(t, w, sideApp, sideTerminal); shown != elsewhere {
			t.Fatalf("/resume %s showed %s, want %s back", conversation, shown, elsewhere)
		}
	}
	w.CodexServerOf(side.ID).AwaitName(conversation, "Release plan")
}

func TestAConversationAnotherProfileResumesCountsItsTurnsOnlyThere(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	side, sideApp, elsewhere, sideCodex := sideCodexIdle(t, w, app)
	before := queriedSession(t, w.Client(), elsewhere).Usage.TotalTokens
	conversation := sideCodex.ConversationID
	moveOn(t, sideApp, sideCodex, sideApp.Terminal(elsewhere), "/new", "start the changelog")
	testworld.AwaitSession(sideApp, elsewhere, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })

	here := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(here)
	terminal := app.Terminal(here)
	moveOn(t, app, codex, terminal, "/resume "+conversation, "ship it")
	resumed := sessionShownIn(t, w, app, terminal)
	codex.Reply("Shipped the whole release train to production. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, resumed, func(s protocol.Session) bool { return s.Usage != nil && s.Usage.TotalTokens > 0 })

	if usage := queriedSession(t, w.Client(), elsewhere).Usage; usage == nil || usage.TotalTokens != before {
		t.Errorf("%s in profile %s counts usage %+v after %s ran a turn here, want %d", elsewhere, side.ID, usage, resumed, before)
	}
}

func TestAClosedPlainCodexSessionResumedInASharedTerminalComesBackThereAsShared(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	plain, plainCodex, _ := sharedCodexWaiting(t, w, app)
	conversation := plainCodex.ConversationID
	if closed := closeFromApp(app, plain); !closed.Accepted {
		t.Fatalf("close %s: %s", plain, protocol.Deref(closed.Error))
	}
	setSetting(t, app, "codex_shared_enabled", "true")

	_, codex, terminal := sharedCodexWaiting(t, w, app)
	moveOn(t, app, codex, terminal, "/resume "+conversation, "carry on")
	if resumed := sessionShownIn(t, w, app, terminal); resumed != plain {
		t.Fatalf("/resume %s in a shared terminal shows %s, want %s back", conversation, resumed, plain)
	}
	moveOn(t, app, codex, terminal, "/new", "write the notes")
	testworld.AwaitSession(app, plain, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
}

func TestAClosedSharedCodexSessionResumesFromTheLedgerAsPlainCodexWhileSharedCodexIsOff(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, _ := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID
	if closed := closeFromApp(app, checkout); !closed.Accepted {
		t.Fatalf("close %s: %s", checkout, protocol.Deref(closed.Error))
	}
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(e.Session.ID) == checkout
	})
	setSetting(t, app, "codex_shared_enabled", "false")

	if reopened := reopenOverTheWebSocket(app, checkout); !reopened.Success {
		t.Fatalf("resume %s from the ledger: %s", checkout, protocol.Deref(reopened.Error))
	}
	resumed := w.Launched(checkout)
	if !resumed.Resumed || resumed.ConversationID != conversation {
		t.Fatalf("the resumed Codex shows %s (resumed=%v), want conversation %s", resumed.ConversationID, resumed.Resumed, conversation)
	}
	if archivedInCodex(t, w, conversation) {
		t.Errorf("conversation %s is still archived after its session resumed", conversation)
	}
	w.CodexServer().AwaitStopped()
	moveOn(t, app, resumed, app.Terminal(checkout), "/new", "write the notes")
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(e.Session.ID) == checkout
	})
}

func TestClosingASharedCodexSessionFromTheCLIArchivesItsConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, _ := sharedCodexWaiting(t, w, app)
	server := w.CodexServer()
	if err := w.Client().Unregister(protocol.SessionID(checkout)); err != nil {
		t.Fatalf("unregister %s: %v", checkout, err)
	}
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(e.Session.ID) == checkout
	})
	server.AwaitStopped()
	if !archivedInCodex(t, w, codex.ConversationID) {
		t.Errorf("closing %s from the CLI left conversation %s unarchived", checkout, codex.ConversationID)
	}
}

func TestASharedLaunchRefusesAConversationAnotherProfilesServerHolds(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	side, _, _, sideCodex := sideCodexIdle(t, w, app)

	refused, _, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr(sideCodex.ConversationID)
	})
	if refused.Success || !strings.Contains(protocol.Deref(refused.Error), side.ID) {
		t.Errorf("resuming %s here while profile %s holds it = %+v, want it refused naming that profile", sideCodex.ConversationID, side.ID, refused)
	}
}

func TestAHiddenSharedCodexQuestionWaitsForTheUserUntilItsAppServerExits(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(checkout)
	terminal := app.Terminal(checkout)
	app.TypeLine(checkout, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("Found it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	conversation := codex.ConversationID
	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })

	if got := submitSessionAnnotationFeedback(app, checkout, sessionAnnotationFeedback); !got.success {
		t.Fatalf("feedback to the hidden session = %+v", got)
	}
	server := w.CodexServer()
	server.Prompted(conversation)
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.AskQuestion(conversation)
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool {
		return protocol.Deref(s.Hidden) && s.State == protocol.SessionStatePendingApproval
	})

	w.LogDaemonTail()
	codex.CrashAppServer()
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool {
		return protocol.Deref(s.Hidden) && s.State == protocol.SessionStateIdle
	})
	w.LogDaemonTail()
}

func TestAHiddenSharedCodexSessionWaitingForTheUserKeepsWaitingThroughItsAppServersExit(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID
	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	codex.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	watcher := w.App()
	var states []protocol.SessionState
	for _, s := range watcher.Initial.Sessions {
		if string(s.ID) == checkout {
			states = append(states, s.State)
		}
	}

	codex.CrashAppServer()
	typeInto(app, terminal, "round the total\r")
	codex.Prompted()
	if got := submitSessionAnnotationFeedback(app, checkout, sessionAnnotationFeedback); !got.success {
		t.Fatalf("feedback to the hidden session = %+v", got)
	}
	w.CodexServer().Prompted(conversation)
	testworld.AwaitSession(watcher, checkout, func(s protocol.Session) bool {
		states = append(states, s.State)
		return s.State == protocol.SessionStateWorking
	})
	if states[0] != protocol.SessionStateWaitingInput || slices.Contains(states, protocol.SessionStateIdle) {
		t.Errorf("hidden session %s went %v before its next turn, want it waiting on the user throughout", checkout, states)
	}
}

func TestNewInASharedCodexChiefTerminalStartsAPlainConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	chief := w.Spawn(app, fakeagent.Codex, w.Path("chief"), func(m *protocol.SpawnSessionMessage) {
		m.ChiefOfStaff = protocol.Ptr(true)
	})
	codex := w.Launched(chief)
	terminal := app.Terminal(chief)
	app.TypeLine(chief, "plan the week")
	codex.Prompted()
	codex.Reply("Planned. <!-- attn:state=idle -->")
	server := w.CodexServer()
	if got := server.Instructions(codex.ConversationID); !strings.Contains(got, "You are the chief of staff") {
		t.Fatalf("the chief's conversation started with:\n%s\nwant the chief guidance", got)
	}

	moveOn(t, app, codex, terminal, "/new", "fix the build")
	if got := server.Instructions(codex.ConversationID); strings.Contains(got, "You are the chief of staff") || !strings.Contains(got, agentGuidanceLead) {
		t.Errorf("the conversation /new started in the chief's terminal began with:\n%s\nwant only the agent guidance", got)
	}
}

func TestAHiddenSharedCodexSessionCountsTheTurnsItsInputStarts(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID
	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	codex.Reply("Added. <!-- attn:state=idle -->")
	before := testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	var counted int
	if before.Usage != nil {
		counted = before.Usage.TotalTokens
	}

	if got := submitSessionAnnotationFeedback(app, checkout, sessionAnnotationFeedback); !got.success {
		t.Fatalf("feedback to the hidden session = %+v", got)
	}
	w.CodexServer().Prompted(conversation)
	w.CodexServer().Reply(conversation, "Fixed what the notes asked for. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return s.Usage != nil && s.Usage.TotalTokens > counted })
}

func TestAHiddenSharedCodexConversationReloadsWithItsSessionsContextCap(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	setSetting(t, app, "default_context_window_cap_codex", "200000")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID
	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	codex.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })

	if got := submitSessionAnnotationFeedback(app, checkout, sessionAnnotationFeedback); !got.success {
		t.Fatalf("feedback to the hidden session = %+v", got)
	}
	w.CodexServer().Prompted(conversation)
	if limit := w.CodexServer().CompactLimit(conversation); limit != "200000" {
		t.Errorf("conversation %s reloaded for hidden input with compact limit %q, want 200000", conversation, limit)
	}
}

func TestASharedCodexServerThatOutlivesAttnsConnectionKeepsReportingToIt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)

	w.CodexServer().DropControl()
	typeInto(app, terminal, "/rename Checkout race\r")
	codex.Prompted()
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return s.Label == "Checkout race" })
}

func TestAHiddenSharedCodexTurnEndsWhenItsAppServerExits(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID
	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	codex.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	if got := submitSessionAnnotationFeedback(app, checkout, sessionAnnotationFeedback); !got.success {
		t.Fatalf("feedback to the hidden session = %+v", got)
	}
	w.CodexServer().Prompted(conversation)
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool {
		return protocol.Deref(s.Hidden) && s.State == protocol.SessionStateWorking
	})

	codex.CrashAppServer()
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool {
		return protocol.Deref(s.Hidden) && s.State == protocol.SessionStateIdle
	})
}

func sharedCodexWaiting(t *testing.T, w *world, app *testworld.Peer) (string, *fakeagent.Run, string) {
	t.Helper()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	terminal := app.Terminal(session)
	app.TypeLine(session, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("It races the tax lookup. Lock it? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	return session, codex, terminal
}

func sideCodexIdle(t *testing.T, w *world, app *testworld.Peer) (protocol.Profile, *testworld.Peer, string, *fakeagent.Run) {
	t.Helper()
	side := createProfile(app, "Side")
	sideApp := w.AppOn(side.ID)
	elsewhere := w.Spawn(sideApp, fakeagent.Codex, w.Path("shop"))
	sideCodex := w.Launched(elsewhere)
	sideApp.TypeLine(elsewhere, "plan the release")
	sideCodex.Prompted()
	sideCodex.Reply("Planned. <!-- attn:state=idle -->")
	testworld.AwaitSession(sideApp, elsewhere, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateIdle && s.Usage != nil && s.Usage.TotalTokens > 0
	})
	return side, sideApp, elsewhere, sideCodex
}

func moveOn(t *testing.T, app *testworld.Peer, codex *fakeagent.Run, terminal, command, prompt string) {
	t.Helper()
	typeInto(app, terminal, command+"\r")
	codex.Prompted()
	typeInto(app, terminal, prompt+"\r")
	if got := codex.Prompted(); got != prompt {
		t.Fatalf("codex took %q, want %q", got, prompt)
	}
}

func typeInto(app *testworld.Peer, terminal, text string) {
	app.T.Helper()
	probe := uuid.NewString()
	testworld.Request(app, protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: text, ProbeID: protocol.Ptr(probe)},
		protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == probe })
}

func tileOf(t *testing.T, w *world, app *testworld.Peer, terminal string) (string, string) {
	t.Helper()
	for _, desktop := range viewProfile(t, w, app.SelectedProfile()).desktops {
		for _, pane := range desktop.Panes {
			if string(pane.RuntimeID) == terminal {
				return desktop.ID, pane.PaneID
			}
		}
	}
	t.Fatalf("no tile holds terminal %s", terminal)
	return "", ""
}

func sessionShownIn(t *testing.T, w *world, app *testworld.Peer, terminal string) string {
	t.Helper()
	for _, desktop := range viewProfile(t, w, app.SelectedProfile()).desktops {
		for _, pane := range desktop.Panes {
			if string(pane.RuntimeID) == terminal {
				return string(pane.SessionID)
			}
		}
	}
	t.Fatalf("no tile holds terminal %s", terminal)
	return ""
}

func archivedInCodex(t *testing.T, w *world, conversation string) bool {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(w.Dir, "toolhome", ".codex", "archived_sessions"))
	return slices.ContainsFunc(entries, func(e os.DirEntry) bool { return strings.HasSuffix(e.Name(), conversation+".jsonl") })
}

func TestNewInASharedCodexTerminalKeepsTheSessionsContextCap(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, _, terminal := sharedCodexWaiting(t, w, app)
	pinned := testworld.Request(app, protocol.SetSessionContextWindowCapMessage{Cmd: protocol.CmdSetSessionContextWindowCap, SessionID: protocol.SessionID(checkout), Cap: 300000},
		protocol.EventSessionContextWindowCapResult, func(r protocol.SessionContextWindowCapResultMessage) bool { return string(r.SessionID) == checkout })
	if !pinned.Success {
		t.Fatalf("pin the context cap: %s", protocol.Deref(pinned.Error))
	}
	codex := w.Launched(checkout)

	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	if limit := w.CodexServer().CompactLimit(codex.ConversationID); limit != "300000" {
		t.Errorf("the conversation /new started loaded with compact limit %q, want the session's 300000", limit)
	}
}
