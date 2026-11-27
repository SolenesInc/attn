package main_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestOpeningTheOriginalOfAnOutpostCodexForkShowsItOnTheOutpostBesideTheFork(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	shareOutpostCodex(t, pair)
	app := pair.App
	original := home.Spawn(app, fakeagent.Codex, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	terminal := app.Terminal(original)
	agent := outpost.LaunchedCarrying(terminal)
	app.TypeLine(original, "work on the outpost")
	agent.Prompted()
	agent.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, original, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })

	app.TypeLine(original, "/fork")
	agent.Prompted()
	fork := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && protocol.Deref(e.Session.Succeeds) == protocol.SessionID(original)
	}).Session
	if protocol.Deref(fork.ForkOffer) != protocol.SessionID(original) {
		t.Fatalf("the outpost fork %s offers %v, want %s", fork.ID, fork.ForkOffer, original)
	}

	request := uuid.NewString()
	opened := testworld.Request(app, protocol.ForkOfferOpenMessage{Cmd: protocol.CmdForkOfferOpen, RequestID: request, SessionID: fork.ID},
		protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == request })
	if !opened.Success {
		t.Fatalf("opening the original of %s: %s", fork.ID, protocol.Deref(opened.Error))
	}
	shown := testworld.AwaitSession(app, original, func(s protocol.Session) bool { return !protocol.Deref(s.Hidden) })
	if protocol.Deref(shown.Host) != pair.Host {
		t.Fatalf("the original runs on %q, want outpost %q", protocol.Deref(shown.Host), pair.Host)
	}
	if resumed := outpost.LaunchedCarrying(app.Terminal(original)); !resumed.Resumed {
		t.Fatalf("the outpost started codex %q for the original, want it to resume its conversation", resumed.Argv)
	}
	desktopOf := map[string]string{}
	active := ""
	for _, desktop := range opened.Desktops {
		for _, pane := range desktop.Panes {
			desktopOf[string(pane.SessionID)] = desktop.ID
			if pane.PaneID == desktop.ActivePaneID {
				active = string(pane.SessionID)
			}
		}
	}
	if desktopOf[original] == "" || desktopOf[original] != desktopOf[string(fork.ID)] || active != original {
		t.Fatalf("the original is on desktop %q and the fork on %q, with %q active; want both on one desktop with the original active", desktopOf[original], desktopOf[string(fork.ID)], active)
	}
	testworld.AwaitSession(app, string(fork.ID), func(s protocol.Session) bool { return s.ForkOffer == nil })
}
