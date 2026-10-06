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

func TestWithSharedCodexOffNewClosesTheSessionItLeft(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(e.Session.ID) == checkout
	})
	if entry := ledgerShowOverTheWebSocket(app, checkout).Entry; entry == nil || protocol.Deref(entry.ClosedAt) == "" || protocol.Deref(entry.Hidden) {
		t.Errorf("the ledger shows %+v, want %s closed", entry, checkout)
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
	if !archivedInCodex(t, w, conversation) {
		t.Errorf("conversation %s was not archived in Codex", conversation)
	}

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
	if !archivedInCodex(t, w, conversation) {
		t.Errorf("closing the hidden session left conversation %s unarchived", conversation)
	}
	codex.Reply("Both added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, sessionShownIn(t, w, app, terminal), func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestShowingAHiddenSharedCodexSessionReplaysTheApprovalItWaitsOn(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
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

func TestAPlainCodexCannotResumeAConversationASharedSessionHolds(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	checkout, codex, _ := sharedCodexWaiting(t, w, app)
	setSetting(t, app, "codex_shared_enabled", "false")

	refused, _, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr(codex.ConversationID)
	})
	if refused.Success || !strings.Contains(protocol.Deref(refused.Error), checkout) {
		t.Errorf("resuming %s in a plain Codex = %+v, want it refused for %s", codex.ConversationID, refused, checkout)
	}
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

func TestAReopenedSharedCodexSessionNamesItsConversationAgainAfterAnotherProfileRenamedIt(t *testing.T) {
	for _, way := range []string{"ledger", "/resume"} {
		t.Run(way, func(t *testing.T) { reopenRenamedSharedCodexSession(t, way) })
	}
}

func reopenRenamedSharedCodexSession(t *testing.T, way string) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	_, sideApp, elsewhere, sideCodex := sideCodexIdle(t, w, app)
	conversation := sideCodex.ConversationID
	renamed := testworld.Request(sideApp, protocol.RenameSessionMessage{Cmd: protocol.CmdRenameSession, SessionID: protocol.SessionID(elsewhere), Label: "Release plan"},
		protocol.EventRenameResult, func(r protocol.RenameResultMessage) bool { return string(r.ID) == elsewhere })
	if !renamed.Success {
		t.Fatalf("rename %s: %s", elsewhere, protocol.Deref(renamed.Error))
	}
	w.CodexServer().AwaitName(conversation, "Release plan")

	here := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(here)
	terminal := app.Terminal(here)
	moveOn(t, app, codex, terminal, "/resume "+conversation, "ship it")
	resumed := sessionShownIn(t, w, app, terminal)
	typeInto(app, terminal, "/rename Release train\r")
	codex.Prompted()
	testworld.AwaitSession(app, resumed, func(s protocol.Session) bool { return s.Label == "Release train" })
	w.CodexServer().AwaitName(conversation, "Release train")

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
			t.Fatalf("reopen %s: %s", elsewhere, protocol.Deref(reopened.Error))
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
	w.CodexServer().AwaitName(conversation, "Release plan")
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

func TestAHiddenSharedCodexSessionThatAsksAQuestionWaitsForTheUser(t *testing.T) {
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
	server.AskQuestion(conversation)
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool {
		return protocol.Deref(s.Hidden) && s.State == protocol.SessionStateWaitingInput
	})
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

// sideCodexIdle runs one turn of a shared Codex session in a second profile and waits for it to settle.
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
