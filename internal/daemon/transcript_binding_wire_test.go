package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestACopilotReplySettlesTheSessionWhileCopilotKeepsRunning(t *testing.T) {
	w := newWorld(t, withAgents(fakeagent.Copilot))
	app := w.app()

	session := w.spawn(app, fakeagent.Copilot, w.path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("rename the checkout module")
	})
	run := w.launched(session)
	if got := run.Prompted(); got != "rename the checkout module" {
		t.Fatalf("copilot received %q", got)
	}
	run.Reply("Need your input: keep the old import path as an alias? <!-- attn:state=waiting_input -->")

	awaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

func TestCopilotRespawnResumesTheConversationItsTranscriptBound(t *testing.T) {
	w := newWorld(t, withAgents(fakeagent.Copilot))
	app := w.app()
	cwd := w.path("shop")

	session := w.spawn(app, fakeagent.Copilot, cwd, func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("rename the checkout module")
	})
	first := w.launched(session)
	first.Prompted()
	first.Reply("Keep the old import path as an alias? <!-- attn:state=waiting_input -->")
	awaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	app.send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: session})
	await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })

	w.spawn(app, fakeagent.Copilot, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = session
		m.ResumeSessionID = protocol.Ptr(session)
	})
	resumed := w.launched(session)
	if !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("respawn ran copilot %q, want --resume %s", resumed.Argv, first.ConversationID)
	}
	app.typeLine(session, "yes, keep the alias")
	if got := resumed.Prompted(); got != "yes, keep the alias" {
		t.Fatalf("resumed copilot received %q", got)
	}
	resumed.Reply("Renamed, alias kept. <!-- attn:state=idle -->")
	awaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestCopilotSessionsSharingADirectoryEachFollowTheirOwnConversation(t *testing.T) {
	w := newWorld(t, withAgents(fakeagent.Copilot))
	app := w.app()
	cwd := w.path("shop")

	first := w.spawn(app, fakeagent.Copilot, cwd, func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("rename the checkout module")
	})
	firstRun := w.launched(first)
	firstRun.Prompted()
	firstRun.Reply("Keep the old import path as an alias? <!-- attn:state=waiting_input -->")
	awaitSession(app, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	second := w.spawn(app, fakeagent.Copilot, cwd)
	secondRun := w.launched(second)
	if window := messageWindow(app, second); window.Status != protocol.SessionMessageWindowStatusReady || len(window.Messages) != 0 {
		t.Fatalf("a fresh copilot session shows window %s %+v, want its own empty conversation", window.Status, window.Messages)
	}

	app.typeLine(second, "add a discount field to checkout")
	secondRun.Prompted()
	secondRun.Reply("Added the discount field. <!-- attn:state=idle -->")
	awaitSession(app, second, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	app.typeLine(first, "yes, keep the alias")
	firstRun.Prompted()
	firstRun.Reply("Renamed, alias kept. <!-- attn:state=idle -->")
	awaitSession(app, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	app.send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: second})
	await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == second })
	w.spawn(app, fakeagent.Copilot, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = second
		m.ResumeSessionID = protocol.Ptr(second)
	})
	resumed := w.launched(second)
	if !resumed.Resumed || resumed.ConversationID != secondRun.ConversationID {
		t.Fatalf("respawn ran copilot %q, want --resume %s", resumed.Argv, secondRun.ConversationID)
	}
}

func TestACodexSessionWithoutHooksStopsLookingForAnUnboundTranscript(t *testing.T) {
	t.Setenv("ATTN_AGENT_CODEX_HOOKS", "0")
	w := newWorld(t, withAgents(fakeagent.Codex))
	app := w.app()

	session := w.spawn(app, fakeagent.Codex, w.path("shop"))
	w.launched(session)

	if window := messageWindow(app, session); window.Status != protocol.SessionMessageWindowStatusUnavailable {
		t.Fatalf("message window %s, want unavailable once no exact transcript can be bound", window.Status)
	}
}

func messageWindow(app *peer, session string) protocol.SessionMessagesGetResultMessage {
	app.t.Helper()
	await(app, protocol.EventSessionMessagesChanged, func(e protocol.SessionMessagesChangedMessage) bool { return e.SessionID == session })
	return request(app, protocol.SessionMessagesGetMessage{
		Cmd: protocol.CmdSessionMessagesGet, RequestID: "window-" + session, SessionID: session,
	}, protocol.EventSessionMessagesGetResult, func(r protocol.SessionMessagesGetResultMessage) bool { return r.RequestID == "window-"+session })
}
