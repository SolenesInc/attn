package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func talk(app *testworld.Peer, claude *fakeagent.Run, session, prompt string) {
	app.T.Helper()
	app.TypeLine(session, prompt)
	claude.Prompted()
	claude.Reply("Done. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func awaitSharedWith(app *testworld.Peer, mark int, session string, others ...string) protocol.Session {
	app.T.Helper()
	want := make([]protocol.SessionID, 0, len(others))
	for _, other := range others {
		want = append(want, protocol.SessionID(other))
	}
	return testworld.AwaitSessionSince(app, mark, session, func(s protocol.Session) bool {
		return slices.Equal(s.ConversationSharedWith, want)
	})
}

func TestClaudeSessionsShowTheirConversationIsSharedWhileBothProcessesRunIt(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	first := w.Spawn(app, fakeagent.Claude, cwd)
	one := w.Launched(first)
	talk(app, one, first, "plan the release")

	second := w.Spawn(app, fakeagent.Claude, cwd)
	two := w.Launched(second)
	resumeClaude(app, two, second, one.ConversationID)

	awaitSharedWith(app, 0, first, second)
	awaitSharedWith(app, 0, second, first)

	exited := len(app.Log())
	one.Exit(1)
	testworld.AwaitSince(app, exited, protocol.EventSessionsUpdated, func(e protocol.WebSocketEvent) bool {
		var gone, survivor *protocol.Session
		for i := range e.Sessions {
			switch string(e.Sessions[i].ID) {
			case first:
				gone = &e.Sessions[i]
			case second:
				survivor = &e.Sessions[i]
			}
		}
		return gone != nil && survivor != nil && len(gone.ConversationSharedWith) == 0 && len(survivor.ConversationSharedWith) == 0
	})
}

func TestClaudeSessionsStopSharingWhenOneMovesToAnotherConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	first := w.Spawn(app, fakeagent.Claude, cwd)
	one := w.Launched(first)
	talk(app, one, first, "plan the release")
	second := w.Spawn(app, fakeagent.Claude, cwd)
	two := w.Launched(second)
	talk(app, two, second, "look at taxes")
	own := two.ConversationID
	resumeClaude(app, two, second, one.ConversationID)
	awaitSharedWith(app, 0, first, second)

	moved := len(app.Log())
	resumeClaude(app, two, second, own)
	awaitSharedWith(app, moved, first)
	awaitSharedWith(app, moved, second)
}

func TestASessionLaunchedOnAConversationAnotherLiveSessionRunsShowsItShared(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	first := w.Spawn(app, fakeagent.Claude, cwd)
	one := w.Launched(first)
	talk(app, one, first, "plan the release")

	second := w.Spawn(app, fakeagent.Claude, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr(one.ConversationID)
	})
	w.Launched(second)

	awaitSharedWith(app, 0, first, second)
	awaitSharedWith(app, 0, second, first)
}

func TestCodexSessionsOnOneConversationAreNotShownShared(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := w.Path("shop")
	first := w.Spawn(app, fakeagent.Codex, cwd)
	one := w.Launched(first)
	app.TypeLine(first, "plan the release")
	one.Prompted()
	one.Reply("Planned. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	second := w.Spawn(app, fakeagent.Codex, cwd)
	two := w.Launched(second)
	terminal := app.Terminal(second)
	typeInto(app, terminal, "/resume "+one.ConversationID+"\r")
	two.Prompted()
	typeInto(app, terminal, "ship it\r")
	two.Prompted()
	two.Reply("Shipped. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, second, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	if two.ConversationID != one.ConversationID {
		t.Fatalf("the second codex runs conversation %s, want %s", two.ConversationID, one.ConversationID)
	}
	sessions, err := cli.Query("")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if len(s.ConversationSharedWith) > 0 {
			t.Errorf("codex session %s is shown sharing its conversation with %v; codex refuses a second process itself", s.ID, s.ConversationSharedWith)
		}
	}
}
