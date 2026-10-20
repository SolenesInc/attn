package daemon_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestACopilotReplySettlesTheSessionWhileCopilotKeepsRunning(t *testing.T) {
	w := newWorld(t, fakeagent.Copilot)
	app := w.App()

	session := w.Spawn(app, fakeagent.Copilot, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("rename the checkout module")
	})
	run := w.Launched(session)
	if got := run.Prompted(); got != "rename the checkout module" {
		t.Fatalf("copilot received %q", got)
	}
	run.Reply("Need your input: keep the old import path as an alias? <!-- attn:state=waiting_input -->")

	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

func TestCopilotRespawnResumesTheConversationItsTranscriptBound(t *testing.T) {
	w := newWorld(t, fakeagent.Copilot)
	app := w.App()
	cwd := w.Path("shop")

	session := w.Spawn(app, fakeagent.Copilot, cwd, func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("rename the checkout module")
	})
	first := w.Launched(session)
	first.Prompted()
	first.Reply("Keep the old import path as an alias? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: protocol.SessionID(session)})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })

	w.Spawn(app, fakeagent.Copilot, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = protocol.SessionID(session)
		m.ResumeSessionID = protocol.Ptr(session)
	})
	resumed := w.Launched(session)
	if !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("respawn ran copilot %q, want --resume %s", resumed.Argv, first.ConversationID)
	}
	app.TypeLine(session, "yes, keep the alias")
	if got := resumed.Prompted(); got != "yes, keep the alias" {
		t.Fatalf("resumed copilot received %q", got)
	}
	resumed.Reply("Renamed, alias kept. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestCopilotSessionsSharingADirectoryEachFollowTheirOwnConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Copilot)
	app := w.App()
	cwd := w.Path("shop")

	first := w.Spawn(app, fakeagent.Copilot, cwd, func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("rename the checkout module")
	})
	firstRun := w.Launched(first)
	firstRun.Prompted()
	firstRun.Reply("Keep the old import path as an alias? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	second := w.Spawn(app, fakeagent.Copilot, cwd)
	secondRun := w.Launched(second)
	if window := messageWindow(app, second); window.Status != protocol.SessionMessageWindowStatusReady || len(window.Messages) != 0 {
		t.Fatalf("a fresh copilot session shows window %s %+v, want its own empty conversation", window.Status, window.Messages)
	}

	app.TypeLine(second, "add a discount field to checkout")
	secondRun.Prompted()
	secondRun.Reply("Added the discount field. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, second, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	app.TypeLine(first, "yes, keep the alias")
	firstRun.Prompted()
	firstRun.Reply("Renamed, alias kept. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: protocol.SessionID(second)})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == second })
	w.Spawn(app, fakeagent.Copilot, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = protocol.SessionID(second)
		m.ResumeSessionID = protocol.Ptr(second)
	})
	resumed := w.Launched(second)
	if !resumed.Resumed || resumed.ConversationID != secondRun.ConversationID {
		t.Fatalf("respawn ran copilot %q, want --resume %s", resumed.Argv, secondRun.ConversationID)
	}
}

func TestCopilotSessionsOfTwoProfilesLaunchedTogetherInADirectoryEachFollowTheirOwnConversation(t *testing.T) {
	w := newWorld(t, fakeagent.Copilot)
	app := w.App()
	work := w.AppOn(createProfile(app, "Work").ID)
	cwd := w.Path("shop")
	first := w.Spawn(app, fakeagent.Copilot, cwd)
	second := w.Spawn(work, fakeagent.Copilot, cwd)
	firstRun, secondRun := w.Launched(first), w.Launched(second)
	for _, run := range []struct {
		peer    *testworld.Peer
		session string
		agent   *fakeagent.Run
		reply   string
	}{{app, first, firstRun, "Renamed the checkout module."}, {work, second, secondRun, "Added the discount field."}} {
		run.peer.TypeLine(run.session, "go ahead")
		run.agent.Prompted()
		run.agent.Reply(run.reply + " <!-- attn:state=idle -->")
		messageWindowShowing(run.peer, run.session, run.reply)
	}
	for _, run := range []struct {
		peer    *testworld.Peer
		session string
		other   string
	}{{app, first, "Added the discount field."}, {work, second, "Renamed the checkout module."}} {
		for _, message := range messageMarkdowns(requestMessageWindow(run.peer, run.session, "check-"+run.session)) {
			if strings.Contains(message, run.other) {
				t.Fatalf("session %s follows the other profile's conversation: %q", run.session, message)
			}
		}
	}
}

func TestACodexSessionWithoutHooksStopsLookingForAnUnboundTranscript(t *testing.T) {
	t.Setenv("ATTN_AGENT_CODEX_HOOKS", "0")
	w := newWorld(t, fakeagent.Codex)
	app := w.App()

	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	w.Launched(session)

	if window := messageWindow(app, session); window.Status != protocol.SessionMessageWindowStatusUnavailable {
		t.Fatalf("message window %s, want unavailable once no exact transcript can be bound", window.Status)
	}
}

func messageWindow(app *testworld.Peer, session string) protocol.SessionMessagesGetResultMessage {
	app.T.Helper()
	testworld.Await(app, protocol.EventSessionMessagesChanged, func(e protocol.SessionMessagesChangedMessage) bool { return string(e.SessionID) == session })
	return testworld.Request(app, protocol.SessionMessagesGetMessage{
		Cmd: protocol.CmdSessionMessagesGet, RequestID: "window-" + session, SessionID: protocol.SessionID(session),
	}, protocol.EventSessionMessagesGetResult, func(r protocol.SessionMessagesGetResultMessage) bool { return r.RequestID == "window-"+session })
}

func TestCopilotNormalExitKeepsItsConversationForResume(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(strconv.FormatBool(direct), func(t *testing.T) {

			w := newWorld(t, fakeagent.Copilot)
			app := w.App()
			if direct {
				setSetting(t, app, "direct_agent_launch_enabled", "true")
			}
			cwd := w.Path("shop")
			session := w.Spawn(app, fakeagent.Copilot, cwd, func(m *protocol.SpawnSessionMessage) { m.InitialPrompt = protocol.Ptr("rename the checkout module") })
			first := w.Launched(session)
			first.Prompted()
			first.Exit(0)
			testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
			awaitClosed(app, session)
			if _, err := w.Client().SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(session)}); err != nil {
				t.Fatalf("resume normally exited Copilot: %v", err)
			}
			resumed := w.Launched(session)
			if !resumed.Resumed || resumed.ConversationID != first.ConversationID {
				t.Fatalf("after normal exit Copilot resumed %q, want %s", resumed.Argv, first.ConversationID)
			}

		})
	}
}
