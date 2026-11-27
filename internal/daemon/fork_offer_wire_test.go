package daemon_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type forkedCodex struct {
	w        *world
	app      *testworld.Peer
	codex    *fakeagent.Run
	terminal string
	original string
	fork     string
	tax      string
	row      rowDesktop
}

func sharedCodexForkedBesideAnotherTile(t *testing.T) forkedCodex {
	t.Helper()
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	original, codex, terminal := sharedCodexWaiting(t, w, app)
	tax := w.Spawn(app, fakeagent.Codex, w.Path("tax"))
	profileID := app.SelectedProfile()
	desktop, _ := viewProfile(t, w, profileID).paneOf(t, original)
	f := forkedCodex{w: w, app: app, codex: codex, terminal: terminal, original: original, tax: tax,
		row: rowDesktop{t: t, w: w, app: app, profileID: profileID, desktopID: desktop.ID}}
	f.fork = forkIn(t, app, codex, terminal, original)
	f.row.requireRow(f.fork + " " + tax)
	return f
}

func forkIn(t *testing.T, app *testworld.Peer, codex *fakeagent.Run, terminal, shown string) string {
	t.Helper()
	mark := len(app.Log())
	typeInto(app, terminal, "/fork\r")
	codex.Prompted()
	fork := awaitTerminalSessionChange(app, mark, terminal, shown)
	if fork == shown {
		t.Fatalf("after /fork, terminal %s still shows session %s", terminal, shown)
	}
	return fork
}

func awaitForkOffer(app *testworld.Peer, fork, original string) {
	app.T.Helper()
	testworld.AwaitSession(app, fork, func(s protocol.Session) bool { return string(protocol.Deref(s.ForkOffer)) == original })
}

func awaitNoForkOffer(app *testworld.Peer, fork string) {
	app.T.Helper()
	testworld.AwaitSession(app, fork, func(s protocol.Session) bool { return s.ForkOffer == nil })
}

func openOriginal(app *testworld.Peer, fork string) protocol.ProfileActionResultMessage {
	app.T.Helper()
	id := uuid.NewString()
	return profileRequest(app, protocol.ForkOfferOpenMessage{Cmd: protocol.CmdForkOfferOpen, RequestID: id, SessionID: protocol.SessionID(fork)}, id)
}

func mustOpenOriginal(app *testworld.Peer, fork string) {
	app.T.Helper()
	if opened := openOriginal(app, fork); !opened.Success {
		app.T.Fatalf("opening the original of %s: %s", fork, protocol.Deref(opened.Error))
	}
}

func TestAForkInASharedCodexTerminalOffersItsOriginalAndOpenPutsItFocusedInTheNextColumn(t *testing.T) {
	f := sharedCodexForkedBesideAnotherTile(t)
	awaitForkOffer(f.app, f.fork, f.original)

	mustOpenOriginal(f.app, f.fork)
	f.w.Launched(f.original)
	f.row.requireRow(f.fork + " " + f.original + " " + f.tax)
	f.row.requireActive(f.original)
	awaitNoForkOffer(f.app, f.fork)

	mustOpenOriginal(f.app, f.fork)
	f.row.requireRow(f.fork + " " + f.original + " " + f.tax)
}

func initialForkOffer(t *testing.T, app *testworld.Peer, fork string) *protocol.SessionID {
	t.Helper()
	for _, s := range app.Initial.Sessions {
		if string(s.ID) == fork {
			return s.ForkOffer
		}
	}
	t.Fatalf("a new window lists no session %s", fork)
	return nil
}

func TestNewSideAndResumeInASharedCodexTerminalOfferNothingWhileForkDoes(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID

	mark := len(app.Log())
	typeInto(app, terminal, "/new\r")
	codex.Prompted()
	fresh := awaitTerminalSessionChange(app, mark, terminal, checkout)
	if first := testworld.AwaitSession(app, fresh, func(protocol.Session) bool { return true }); first.ForkOffer != nil {
		t.Errorf("/new opened session %s offering %s", fresh, *first.ForkOffer)
	}

	mark = len(app.Log())
	typeInto(app, terminal, "/resume "+conversation+"\r")
	codex.Prompted()
	if back := awaitTerminalSessionChange(app, mark, terminal, fresh); back != checkout {
		t.Fatalf("after /resume, terminal %s shows %s, want %s", terminal, back, checkout)
	}

	typeInto(app, terminal, "/side\r")
	codex.Prompted()
	if codex.ConversationID == conversation {
		t.Fatalf("/side left the terminal on conversation %s", conversation)
	}
	for _, id := range []string{checkout, fresh} {
		if offer := queriedSession(t, w.Client(), id).ForkOffer; offer != nil {
			t.Errorf("session %s offers %s after /new, /resume and /side", id, *offer)
		}
	}

	typeInto(app, terminal, "/resume "+conversation+"\r")
	codex.Prompted()
	fork := forkIn(t, app, codex, terminal, checkout)
	awaitForkOffer(app, fork, checkout)
}

func TestDismissingAForkOfferEndsItInEveryWindow(t *testing.T) {
	f := sharedCodexForkedBesideAnotherTile(t)
	awaitForkOffer(f.app, f.fork, f.original)
	other := f.w.App()
	if offer := initialForkOffer(t, other, f.fork); protocol.Deref(offer) != protocol.SessionID(f.original) {
		t.Fatalf("a second window sees offer %v on %s, want %s", offer, f.fork, f.original)
	}

	f.app.Send(protocol.ForkOfferDismissMessage{Cmd: protocol.CmdForkOfferDismiss, SessionID: protocol.SessionID(f.fork)})
	awaitNoForkOffer(f.app, f.fork)
	awaitNoForkOffer(other, f.fork)
	if offer := initialForkOffer(t, f.w.App(), f.fork); offer != nil {
		t.Errorf("a window opened after the dismiss sees offer %s on %s", *offer, f.fork)
	}
	f.row.requireRow(f.fork + " " + f.tax)
}

func TestAForkOfferSurvivesAnAppReconnectAndEndsWithADaemonRestart(t *testing.T) {
	f := sharedCodexForkedBesideAnotherTile(t)
	awaitForkOffer(f.app, f.fork, f.original)
	f.app.Close()
	if offer := initialForkOffer(t, f.w.App(), f.fork); protocol.Deref(offer) != protocol.SessionID(f.original) {
		t.Fatalf("after a reconnect, %s offers %v, want %s", f.fork, offer, f.original)
	}

	f.w.restart()
	if offer := initialForkOffer(t, f.w.App(), f.fork); offer != nil {
		t.Errorf("after a daemon restart, %s still offers %s", f.fork, *offer)
	}
}

func TestClosingAForkEndsItsOffer(t *testing.T) {
	f := sharedCodexForkedBesideAnotherTile(t)
	awaitForkOffer(f.app, f.fork, f.original)
	closeFromApp(f.app, f.fork)

	if reopened := reopenOverTheWebSocket(f.app, f.fork); !reopened.Success {
		t.Fatalf("reopening %s: %s", f.fork, protocol.Deref(reopened.Error))
	}
	f.w.Launched(f.fork)
	if offer := testworld.AwaitSession(f.app, f.fork, func(s protocol.Session) bool { return true }).ForkOffer; offer != nil {
		t.Errorf("fork %s, closed and reopened, still offers %s", f.fork, *offer)
	}
}

func TestOpeningTheOriginalOfAForkAfterTheOriginalClosedReopensItBesideTheFork(t *testing.T) {
	f := sharedCodexForkedBesideAnotherTile(t)
	awaitForkOffer(f.app, f.fork, f.original)
	closeFromApp(f.app, f.original)
	testworld.Await(f.app, protocol.EventSessionUnregistered, func(e protocol.SessionUnregisteredMessage) bool { return string(e.Session.ID) == f.original })

	mustOpenOriginal(f.app, f.fork)
	f.w.Launched(f.original)
	f.row.requireRow(f.fork + " " + f.original + " " + f.tax)
	f.row.requireActive(f.original)
	awaitNoForkOffer(f.app, f.fork)
}

func TestOpeningAnOriginalATileAlreadyShowsFocusesItAndAddsNoTile(t *testing.T) {
	f := sharedCodexForkedBesideAnotherTile(t)
	awaitForkOffer(f.app, f.fork, f.original)
	if shown := requestShowSession(f.app, f.original); !shown.Success {
		t.Fatalf("showing %s: %s", f.original, protocol.Deref(shown.Error))
	}
	f.w.Launched(f.original)
	focusAgent(t, f.w, f.app, f.fork)
	before := rowOf(t, f.row.desktop())

	mustOpenOriginal(f.app, f.fork)
	f.row.requireRow(before)
	f.row.requireActive(f.original)
}

func TestTwoOpensSentTogetherAddOneTile(t *testing.T) {
	f := sharedCodexForkedBesideAnotherTile(t)
	awaitForkOffer(f.app, f.fork, f.original)
	first, second := uuid.NewString(), uuid.NewString()
	f.app.Send(protocol.ForkOfferOpenMessage{Cmd: protocol.CmdForkOfferOpen, RequestID: first, SessionID: protocol.SessionID(f.fork)})
	f.app.Send(protocol.ForkOfferOpenMessage{Cmd: protocol.CmdForkOfferOpen, RequestID: second, SessionID: protocol.SessionID(f.fork)})
	for _, id := range []string{first, second} {
		if r := testworld.Await(f.app, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == id }); !r.Success {
			t.Fatalf("open %s: %s", id, protocol.Deref(r.Error))
		}
	}
	f.w.Launched(f.original)
	f.row.requireRow(f.fork + " " + f.original + " " + f.tax)
}

func TestOpenPlacesTheOriginalBesideTheForkOnTheDesktopTheForksTileMovedTo(t *testing.T) {
	f := sharedCodexForkedBesideAnotherTile(t)
	awaitForkOffer(f.app, f.fork, f.original)
	elsewhere := createDesktop(f.app, f.row.profileID)
	if moved := f.row.move(protocol.DesktopMoveLeafMessage{LeafID: f.row.pane(f.fork), TargetDesktopID: protocol.Ptr(elsewhere.ID)}); !moved.Success {
		t.Fatalf("moving the fork's tile: %s", protocol.Deref(moved.Error))
	}
	switchDesktop(f.app, f.row.profileID, f.row.desktopID)

	mustOpenOriginal(f.app, f.fork)
	f.w.Launched(f.original)
	there := rowDesktop{t: t, w: f.w, app: f.app, profileID: f.row.profileID, desktopID: elsewhere.ID}
	there.requireRow(f.fork + " " + f.original)
	there.requireActive(f.original)
	f.row.requireRow(f.tax)
	if current := viewProfile(t, f.w, f.row.profileID).profile.CurrentDesktopID; current != elsewhere.ID {
		t.Errorf("the current desktop is %s, want %s, where the original opened", current, elsewhere.ID)
	}
}

func TestAnOpenFromAnotherProfileIsRefused(t *testing.T) {
	f := sharedCodexForkedBesideAnotherTile(t)
	awaitForkOffer(f.app, f.fork, f.original)
	side := createProfile(f.app, "Side")
	refused := openOriginal(f.w.AppOn(side.ID), f.fork)
	if refused.Success || protocol.Deref(refused.ErrorCode) != protocol.ProfileErrorCodeCrossProfile {
		t.Fatalf("an open from profile %s answered success=%v code=%v: %s", side.ID, refused.Success, protocol.Deref(refused.ErrorCode), protocol.Deref(refused.Error))
	}
	f.row.requireRow(f.fork + " " + f.tax)
	if offer := queriedSession(t, f.w.Client(), f.fork).ForkOffer; protocol.Deref(offer) != protocol.SessionID(f.original) {
		t.Errorf("after the refused open, %s offers %v, want %s", f.fork, offer, f.original)
	}
}

func TestAForkOfAForkOpensTheForkItCameFrom(t *testing.T) {
	f := sharedCodexForkedBesideAnotherTile(t)
	awaitForkOffer(f.app, f.fork, f.original)
	again := forkIn(t, f.app, f.codex, f.terminal, f.fork)
	awaitForkOffer(f.app, again, f.fork)

	mustOpenOriginal(f.app, again)
	f.w.Launched(f.fork)
	f.row.requireRow(again + " " + f.fork + " " + f.tax)
	f.row.requireActive(f.fork)
	if offer := queriedSession(t, f.w.Client(), f.fork).ForkOffer; protocol.Deref(offer) != protocol.SessionID(f.original) {
		t.Errorf("after its own fork opened it, %s offers %v, want %s", f.fork, offer, f.original)
	}
}
