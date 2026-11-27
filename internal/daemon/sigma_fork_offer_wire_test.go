package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type sigmaTerminal struct {
	w        *world
	app      *testworld.Peer
	tui      *fakeagent.Run
	server   *fakeagent.AppServer
	original string
	held     string
	terminal string
	tax      string
	row      rowDesktop
}

func settledSigmaBesideAnotherTile(t *testing.T) sigmaTerminal {
	t.Helper()
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	original, tui, server := appServerSession(t, w, app, fakeagent.Sigma)
	settleTwoSigmaPrompts(app, original, tui, server)
	tax := w.Spawn(app, fakeagent.Sigma, w.Path("tax"))
	w.Launched(tax)
	profileID := app.SelectedProfile()
	desktop, _ := viewProfile(t, w, profileID).paneOf(t, original)
	s := sigmaTerminal{w: w, app: app, tui: tui, server: server, original: original, held: tui.ConversationID, terminal: app.Terminal(original), tax: tax,
		row: rowDesktop{t: t, w: w, app: app, profileID: profileID, desktopID: desktop.ID}}
	s.row.requireRow(original + " " + tax)
	return s
}

func (s sigmaTerminal) fork() string {
	s.app.T.Helper()
	mark := len(s.app.Log())
	typeInto(s.app, s.terminal, "/fork\r")
	s.tui.Prompted()
	return awaitTerminalSessionChange(s.app, mark, s.terminal, s.original)
}

func (s sigmaTerminal) show(conversation string) string {
	s.app.T.Helper()
	mark := len(s.app.Log())
	s.server.ShowAs(conversation, "attn-terminal:"+s.terminal)
	return awaitTerminalSessionChange(s.app, mark, s.terminal, s.original)
}

func requireNoForkOfferFromTheStart(app *testworld.Peer, session, how string) {
	app.T.Helper()
	if first := testworld.AwaitSession(app, session, func(protocol.Session) bool { return true }); first.ForkOffer != nil {
		app.T.Errorf("%s opened session %s offering %s", how, session, *first.ForkOffer)
	}
}

func TestASigmaForkOffersItsOriginalAndOpenShowsTheHiddenOriginalFocusedBesideTheFork(t *testing.T) {
	s := settledSigmaBesideAnotherTile(t)
	fork := s.fork()
	testworld.AwaitSession(s.app, s.original, func(p protocol.Session) bool { return protocol.Deref(p.Hidden) })
	awaitForkOffer(s.app, fork, s.original)
	s.row.requireRow(fork + " " + s.tax)

	mustOpenOriginal(s.app, fork)
	if shown := s.w.Launched(s.original); shown.ConversationID != s.held {
		t.Errorf("the original's new tile runs conversation %s, want %s", shown.ConversationID, s.held)
	}
	s.row.requireRow(fork + " " + s.original + " " + s.tax)
	s.row.requireActive(s.original)
	awaitNoForkOffer(s.app, fork)
}

func TestASigmaTerminalShowingAForkedWorkerOffersNothing(t *testing.T) {
	s := settledSigmaBesideAnotherTile(t)
	s.server.ForkedWorker(s.held, "shown-worker", "review")
	requireNoForkOfferFromTheStart(s.app, s.show("shown-worker"), "showing a worker forked from the terminal's conversation")
}

func TestASigmaTerminalShowingAnOlderForkOfAnotherConversationOffersNothing(t *testing.T) {
	s := settledSigmaBesideAnotherTile(t)
	s.server.StartedElsewhere("elsewhere", s.w.Path("shop"), "plan the refunds")
	s.server.Fork("elsewhere", "older-fork")
	requireNoForkOfferFromTheStart(s.app, s.show("older-fork"), "showing a fork of another conversation")
}

func TestASigmaForkWhoseListingFailsMovesTheTileAndOffersNothing(t *testing.T) {
	s := settledSigmaBesideAnotherTile(t)
	s.server.RefuseConversationList("ServerUnavailable")
	fork := s.fork()
	requireNoForkOfferFromTheStart(s.app, fork, "a /fork whose conversation/list failed")
	s.row.requireRow(fork + " " + s.tax)
}
